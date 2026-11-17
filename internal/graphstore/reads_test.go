package graphstore

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

// seedReadGraph is a small graph over two files with every edge kind and an
// external target:
//
//	p/a.go: p.A calls p.B, p.A implements p.I, p.A references p.T,
//	        p.A calls fmt.Println (external)
//	p/b.go: p.B, p.I, p.T, p.T::Method (a tree-sitter member name), q.Other
//	        calls p.A (test caller)
func seedReadGraph(t *testing.T, g *SQLite) {
	t.Helper()
	member := mkSym("p.T.Method", "p/b.go", 30)
	member.Name = "T::Method"
	mustApply(t, g, FileDelta{
		File: "p/b.go", GraphHash: "b1",
		Symbols: []Symbol{
			mkSym("p.B", "p/b.go", 1), mkSym("p.I", "p/b.go", 10), mkSym("p.T", "p/b.go", 20), member,
		},
	})
	mustApply(t, g, FileDelta{
		File: "p/a.go", GraphHash: "a1",
		Symbols: []Symbol{mkSym("p.A", "p/a.go", 1)},
		Edges: []Edge{
			{Src: "p.A", Dst: "p.B"},
			{Src: "p.A", Dst: "p.I", Kind: "IMPLEMENTS"},
			{Src: "p.A", Dst: "p.T", Kind: "REFERENCES"},
			{Src: "p.A", Dst: "fmt.Println", DstExternal: true},
		},
	})
	test := mkSym("q.TestA", "q/a_test.go", 1)
	test.IsTest = true
	mustApply(t, g, FileDelta{
		File: "q/a_test.go", GraphHash: "q1",
		Symbols: []Symbol{test},
		Edges:   []Edge{{Src: "q.TestA", Dst: "p.A"}},
	})
}

func TestSymbolsInFile(t *testing.T) {
	g, _ := openTestGraph(t)
	seedReadGraph(t, g)

	got, err := g.SymbolsInFile("p/b.go")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	if want := []string{"p.B", "p.I", "p.T", "p.T.Method"}; !reflect.DeepEqual(fqnsOf(got), want) {
		t.Fatalf("SymbolsInFile(p/b.go) = %v, want %v (ordered by FQN)", fqnsOf(got), want)
	}
	if got[0].File != "p/b.go" || got[0].StartLine != 1 {
		t.Fatalf("anchor of p.B = %+v, want file p/b.go line 1", got[0])
	}
	// A Windows-style path finds the same rows.
	win, err := g.SymbolsInFile(`p\b.go`)
	if err != nil || len(win) != 4 {
		t.Fatalf("SymbolsInFile(backslash) = %d symbols, %v; want 4", len(win), err)
	}
	none, err := g.SymbolsInFile("nope.go")
	if err != nil || len(none) != 0 {
		t.Fatalf("SymbolsInFile(unknown) = %v, %v; want empty", none, err)
	}
}

func TestAllSymbolsAndCodeFileCount(t *testing.T) {
	g, _ := openTestGraph(t)
	n, err := g.CodeFileCount()
	if err != nil || n != 0 {
		t.Fatalf("CodeFileCount on empty graph = %d, %v; want 0", n, err)
	}
	seedReadGraph(t, g)

	all, err := g.AllSymbols()
	if err != nil {
		t.Fatalf("AllSymbols: %v", err)
	}
	want := []string{"p.A", "p.B", "p.I", "p.T", "p.T.Method", "q.TestA"}
	if !reflect.DeepEqual(fqnsOf(all), want) {
		t.Fatalf("AllSymbols = %v, want %v", fqnsOf(all), want)
	}
	if !all[len(all)-1].IsTest {
		t.Fatalf("q.TestA lost its is_test flag: %+v", all[len(all)-1])
	}

	// A file with passages but no symbols does not count as a code file.
	if err := g.MarkFileDirty(context.Background(), "README.md"); err != nil {
		t.Fatal(err)
	}
	n, err = g.CodeFileCount()
	if err != nil || n != 3 {
		t.Fatalf("CodeFileCount = %d, %v; want 3", n, err)
	}
	st, err := g.Stats()
	if err != nil || st.Files != 4 {
		t.Fatalf("Stats.Files = %d, %v; want 4 (the manifest includes README.md)", st.Files, err)
	}
}

func TestHasData(t *testing.T) {
	ctx := context.Background()

	g, _ := openTestGraph(t)
	if ok, err := g.HasData(); err != nil || ok {
		t.Fatalf("HasData on a fresh store = %v, %v; want false", ok, err)
	}
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.F", "a.go", 1)}})
	if ok, err := g.HasData(); err != nil || !ok {
		t.Fatalf("HasData with symbols = %v, %v; want true", ok, err)
	}
	if err := g.RemoveFile(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.HasData(); err != nil || ok {
		t.Fatalf("HasData after removing the only file = %v, %v; want false", ok, err)
	}

	// Documents alone are graph data too.
	if err := g.ReplaceDocs(ctx, []DocDelta{docDelta("docs/guide.md")}); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.HasData(); err != nil || !ok {
		t.Fatalf("HasData with only documents = %v, %v; want true", ok, err)
	}
}

func TestNeighbors(t *testing.T) {
	g, _ := openTestGraph(t)
	seedReadGraph(t, g)

	edgeKey := func(es []GraphEdge) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.From+" -"+e.Relation+"-> "+e.To+" ("+e.TargetKind+")")
		}
		sort.Strings(out)
		return out
	}

	got, err := g.Neighbors("p.A", 0) // 0 means depth 1
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	want := []string{
		"p.A -CALLS-> p.B (function)",
		"p.A -IMPLEMENTS-> p.I (function)",
		"p.A -REFERENCES-> p.T (function)",
		"q.TestA -CALLS-> p.A (function)",
	}
	if !reflect.DeepEqual(edgeKey(got), want) {
		t.Fatalf("Neighbors(p.A, 1) =\n%v\nwant\n%v (the external fmt.Println must not appear)", edgeKey(got), want)
	}
	for _, e := range got {
		if e.Confidence != "extracted" {
			t.Fatalf("edge %+v: confidence %q, want extracted", e, e.Confidence)
		}
	}

	// From p.B, depth 2 reaches p.A backwards, then p.A's other edges; the
	// edge back to p.B is emitted again (kuzu semantics) but p.B is not
	// re-queued. Depths above 5 are capped.
	got, err = g.Neighbors("p.B", 99)
	if err != nil {
		t.Fatalf("Neighbors depth 99: %v", err)
	}
	keys := edgeKey(got)
	for _, w := range []string{"p.A -CALLS-> p.B (function)", "p.A -IMPLEMENTS-> p.I (function)", "q.TestA -CALLS-> p.A (function)"} {
		if !contains(keys, w) {
			t.Fatalf("Neighbors(p.B, 99) = %v, missing %q", keys, w)
		}
	}

	// An unknown symbol has no neighbours.
	got, err = g.Neighbors("nope", 3)
	if err != nil || len(got) != 0 {
		t.Fatalf("Neighbors(unknown) = %v, %v; want none", got, err)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestPackageMembers(t *testing.T) {
	g, _ := openTestGraph(t)
	seedReadGraph(t, g)
	other := mkSym("r.U.Run", "r/u.go", 1)
	other.Name = "U::Run"
	mustApply(t, g, FileDelta{File: "r/u.go", Symbols: []Symbol{other}})
	// "pp" shares the string prefix "p" but is another package.
	sibling := mkSym("pp.V.X", "pp/v.go", 1)
	sibling.Name = "V::X"
	mustApply(t, g, FileDelta{File: "pp/v.go", Symbols: []Symbol{sibling}})

	got, err := g.PackageMembers("p")
	if err != nil {
		t.Fatalf("PackageMembers: %v", err)
	}
	if want := []string{"p.T.Method"}; !reflect.DeepEqual(fqnsOf(got), want) {
		t.Fatalf("PackageMembers(p) = %v, want %v (plain declarations and package pp are not members of p)", fqnsOf(got), want)
	}
	all, err := g.PackageMembers("")
	if err != nil {
		t.Fatalf("PackageMembers(\"\"): %v", err)
	}
	if want := []string{"p.T.Method", "pp.V.X", "r.U.Run"}; !reflect.DeepEqual(fqnsOf(all), want) {
		t.Fatalf("PackageMembers(\"\") = %v, want %v", fqnsOf(all), want)
	}
	// A type-qualified prefix narrows to that type's members.
	none, err := g.PackageMembers("r.U")
	if err != nil || len(none) != 1 {
		t.Fatalf("PackageMembers(r.U) = %v, %v; want r.U.Run", fqnsOf(none), err)
	}
	none, err = g.PackageMembers("x")
	if err != nil || len(none) != 0 {
		t.Fatalf("PackageMembers(x) = %v, %v; want none", fqnsOf(none), err)
	}
}

func TestFilesWithExternalCalls(t *testing.T) {
	g, _ := openTestGraph(t)
	seedReadGraph(t, g)
	// A second file calls the same external target, a third a different one.
	mustApply(t, g, FileDelta{
		File:    "z.go",
		Symbols: []Symbol{mkSym("z.Z", "z.go", 1)},
		Edges:   []Edge{{Src: "z.Z", Dst: "fmt.Println", DstExternal: true}, {Src: "z.Z", Dst: "os.Exit", DstExternal: true}},
	})
	got, err := g.FilesWithExternalCalls([]string{"fmt.Println", "nope"})
	if err != nil {
		t.Fatalf("FilesWithExternalCalls: %v", err)
	}
	if want := []string{"p/a.go", "z.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FilesWithExternalCalls(fmt.Println) = %v, want %v", got, want)
	}
	// An internal call to a declared symbol is not an external call.
	got, err = g.FilesWithExternalCalls([]string{"p.B"})
	if err != nil || len(got) != 0 {
		t.Fatalf("FilesWithExternalCalls(p.B) = %v, %v; want none", got, err)
	}
	// More targets than one IN-list chunk.
	many := make([]string, 0, 1100)
	for i := 0; i < 1099; i++ {
		many = append(many, "missing.F"+strconv.Itoa(i))
	}
	many = append(many, "os.Exit")
	got, err = g.FilesWithExternalCalls(many)
	if err != nil {
		t.Fatalf("FilesWithExternalCalls(chunked): %v", err)
	}
	if want := []string{"z.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FilesWithExternalCalls(chunked) = %v, want %v", got, want)
	}
}

func TestAllEdgesKeepsExternalFlag(t *testing.T) {
	g, _ := openTestGraph(t)
	seedReadGraph(t, g)
	edges, err := g.AllEdges()
	if err != nil {
		t.Fatalf("AllEdges: %v", err)
	}
	want := []Edge{
		{Src: "p.A", Dst: "fmt.Println", Kind: "CALLS", DstExternal: true, Confidence: "extracted"},
		{Src: "p.A", Dst: "p.B", Kind: "CALLS", Confidence: "extracted"},
		{Src: "p.A", Dst: "p.I", Kind: "IMPLEMENTS", Confidence: "extracted"},
		{Src: "p.A", Dst: "p.T", Kind: "REFERENCES", Confidence: "extracted"},
		{Src: "q.TestA", Dst: "p.A", Kind: "CALLS", Confidence: "extracted"},
	}
	if !reflect.DeepEqual(edges, want) {
		t.Fatalf("AllEdges =\n%+v\nwant\n%+v", edges, want)
	}
}

// TestRetractSourceTx: retracting a deleted source in the caller's
// transaction removes both its code rows and its document rows, marks the
// referrers dirty, and becomes visible only on commit.
func TestRetractSourceTx(t *testing.T) {
	g, s := openTestGraph(t)
	ctx := context.Background()
	seedReadGraph(t, g)
	if err := g.ApplyDocDelta(ctx, docDelta("p/b.go")); err != nil {
		t.Fatal(err)
	}
	if err := g.ApplyDocDelta(ctx, docDelta("docs/keep.md")); err != nil {
		t.Fatal(err)
	}
	nodesBefore := docNodeCount(t, g)

	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RetractSourceTx(ctx, tx, "p/b.go"); err != nil {
		tx.Rollback()
		t.Fatalf("RetractSourceTx: %v", err)
	}
	// Not committed yet: readers still see the old rows.
	if syms, _ := g.SymbolsInFile("p/b.go"); len(syms) != 4 {
		t.Fatalf("uncommitted retraction visible to readers: %d symbols", len(syms))
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if syms, err := g.SymbolsInFile("p/b.go"); err != nil || len(syms) != 0 {
		t.Fatalf("symbols of p/b.go after retraction = %v, %v", fqnsOf(syms), err)
	}
	if n := edgeCount(t, g, "p.A", "p.B"); n != 0 {
		t.Fatalf("inbound edge p.A -> p.B survived retraction")
	}
	dirty, err := g.DirtyFiles()
	if err != nil || !reflect.DeepEqual(dirty, []string{"p/a.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [p/a.go]", dirty, err)
	}
	// The document nodes of p/b.go (document, heading, chunk) are gone; the
	// shared folder and the other document stay.
	if got := docNodeCount(t, g); got != nodesBefore-3 {
		t.Fatalf("doc_nodes = %d, want %d", got, nodesBefore-3)
	}
	if _, err := g.DocumentTOC("docs/keep.md"); err != nil {
		t.Fatalf("the other document went with the retraction: %v", err)
	}
	st, err := g.Stats()
	if err != nil {
		t.Fatal(err)
	}
	// p.A, q.TestA left; p.A's internal edges to p/b.go are gone, the
	// external call and the test call remain.
	if st.Symbols != 2 || st.CallEdges != 2 || st.ImplementsEdges != 0 {
		t.Fatalf("Stats after retraction = %+v, want 2 symbols, 2 calls, 0 implements", st)
	}
}
