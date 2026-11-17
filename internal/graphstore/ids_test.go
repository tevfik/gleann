package graphstore

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// TestGraphLabelIDs pins the kind ids written as literals into the queries to
// the ids the store's graph_ids step seeds.
func TestGraphLabelIDs(t *testing.T) {
	for lit, id := range map[string]int{
		labelCalls:      store.GraphLabelCalls,
		labelImplements: store.GraphLabelImplements,
		labelReferences: store.GraphLabelReferences,
	} {
		if lit != strconv.Itoa(id) {
			t.Errorf("query literal %s, store seeds %d", lit, id)
		}
	}
}

func countQuery(t *testing.T, g *SQLite, q string, args ...any) int {
	t.Helper()
	var n int
	if err := g.reader.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// TestExternalTargetDeclaredLater: an edge classified external keeps pointing
// at the same node when a later file declares the FQN (its graph_nodes row
// moves into symbols, the id stays), so callers and FilesWithExternalCalls
// see it exactly as the text-keyed schema did; when the declaration goes
// away again, the edge is retracted with it and its owner marked dirty.
func TestExternalTargetDeclaredLater(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
		Edges: []Edge{{Src: "a.Caller", Dst: "b.Later", DstExternal: true}}})
	if n := countQuery(t, g, `SELECT count(*) FROM graph_nodes WHERE fqn = 'b.Later'`); n != 1 {
		t.Fatalf("external node rows = %d, want 1", n)
	}

	mustApply(t, g, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("b.Later", "b.go", 1)}})
	if n := countQuery(t, g, `SELECT count(*) FROM graph_nodes WHERE fqn = 'b.Later'`); n != 0 {
		t.Fatalf("external node rows after the declaration = %d, want 0 (moved into symbols)", n)
	}
	hops, err := g.Callers("b.Later", 1)
	if err != nil || len(hops) != 1 || hops[0].FQN != "a.Caller" {
		t.Fatalf("Callers(b.Later) = %v, %v; want a.Caller", hops, err)
	}
	files, err := g.FilesWithExternalCalls([]string{"b.Later"})
	if err != nil || !reflect.DeepEqual(files, []string{"a.go"}) {
		t.Fatalf("FilesWithExternalCalls = %v, %v; want [a.go]", files, err)
	}
	if n := edgeCount(t, g, "a.Caller", "b.Later"); n != 1 {
		t.Fatalf("edge a.Caller -> b.Later = %d, want 1", n)
	}

	// The declaration goes away: the inbound (external) edge goes with it.
	mustApply(t, g, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("b.Other", "b.go", 1)}})
	if n := edgeCount(t, g, "a.Caller", "b.Later"); n != 0 {
		t.Fatalf("edge a.Caller -> b.Later after removal = %d, want 0", n)
	}
	if dirty, err := g.DirtyFiles(); err != nil || !reflect.DeepEqual(dirty, []string{"a.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [a.go]", dirty, err)
	}
	if n := countQuery(t, g, `SELECT count(*) FROM graph_edges`); n != 0 {
		t.Fatalf("graph_edges rows = %d, want 0", n)
	}
	// Re-extracting a.go (still external) creates the node again.
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
		Edges: []Edge{{Src: "a.Caller", Dst: "b.Later", DstExternal: true}}})
	if n := countQuery(t, g, `SELECT count(*) FROM graph_nodes WHERE fqn = 'b.Later'`); n != 1 {
		t.Fatalf("external node rows after re-extraction = %d, want 1", n)
	}
	if err := g.RemoveFile(ctx, "a.go"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	if n := countQuery(t, g, `SELECT count(*) FROM graph_nodes`); n != 0 {
		t.Fatalf("graph_nodes rows after removing the only referrer = %d, want 0", n)
	}
}

// TestRetractedIDNotReused: a delta that drops the symbol holding the highest
// id and declares a new one must not hand the new symbol the old id, or the
// inbound edge to the dropped symbol would be restored onto the new one.
func TestRetractedIDNotReused(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.Max", "c.go", 1)}})
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
		Edges: []Edge{{Src: "a.Caller", Dst: "c.Max"}}})
	// c.Max must hold the highest id for the test to mean anything.
	if n := countQuery(t, g, `SELECT count(*) FROM symbols WHERE id > (SELECT id FROM symbols WHERE fqn = 'c.Max')`); n != 1 {
		t.Skipf("unexpected id layout")
	}
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
		Edges: []Edge{{Src: "a.Caller", Dst: "c.Max"}}}) // a.Caller re-declared: same id
	mustApply(t, g, FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}})
	if n := edgeCount(t, g, "a.Caller", "c.New"); n != 0 {
		t.Fatalf("inbound edge restored onto the new symbol c.New")
	}
	if n := countQuery(t, g, `SELECT count(*) FROM graph_edges`); n != 0 {
		t.Fatalf("graph_edges rows = %d, want 0", n)
	}
	if dirty, err := g.DirtyFiles(); err != nil || !reflect.DeepEqual(dirty, []string{"a.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [a.go]", dirty, err)
	}
}

// TestPathTieBreakByFQN: of two shortest paths, Path reports the one the
// FQN-ordered hop order reaches first, whatever the node ids are. Here the
// later FQN (p.Zmid) gets the smaller id because its file is applied first.
func TestPathTieBreakByFQN(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{File: "z.go", Symbols: []Symbol{mkSym("p.Zmid", "z.go", 1)}})
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("p.Amid", "a.go", 1)}})
	mustApply(t, g, FileDelta{File: "t.go", Symbols: []Symbol{mkSym("p.T", "t.go", 1)}})
	mustApply(t, g, FileDelta{File: "z.go", Symbols: []Symbol{mkSym("p.Zmid", "z.go", 1)}, Edges: []Edge{{Src: "p.Zmid", Dst: "p.T"}}})
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("p.Amid", "a.go", 1)}, Edges: []Edge{{Src: "p.Amid", Dst: "p.T"}}})
	mustApply(t, g, FileDelta{File: "s.go", Symbols: []Symbol{mkSym("p.S", "s.go", 1)},
		Edges: []Edge{{Src: "p.S", Dst: "p.Zmid"}, {Src: "p.S", Dst: "p.Amid"}}})
	steps, err := g.Path("p.S", "p.T")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := []Step{{From: "p.S", To: "p.Amid", Relation: "CALLS"}, {From: "p.Amid", To: "p.T", Relation: "CALLS"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Path = %+v, want %+v", steps, want)
	}
}

// TestReplaceAllExternalDeclared: in a rebuild, an external edge whose target
// some delta declares points at that symbol (no graph_nodes row), as the
// per-file path resolves it.
func TestReplaceAllExternalDeclared(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	if err := g.ReplaceAll(ctx, []FileDelta{
		{File: "a.go", Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
			Edges: []Edge{{Src: "a.Caller", Dst: "b.Later", DstExternal: true}, {Src: "a.Caller", Dst: "x.Lib", DstExternal: true}}},
		{File: "b.go", Symbols: []Symbol{mkSym("b.Later", "b.go", 1)}},
	}); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	var fqns string
	if err := g.reader.QueryRow(`SELECT group_concat(fqn) FROM graph_nodes`).Scan(&fqns); err != nil || fqns != "x.Lib" {
		t.Fatalf("graph_nodes = %q, %v; want x.Lib", fqns, err)
	}
	hops, err := g.Callers("b.Later", 1)
	if err != nil || len(hops) != 1 || hops[0].FQN != "a.Caller" {
		t.Fatalf("Callers(b.Later) = %v, %v; want a.Caller", hops, err)
	}
	edges, err := g.AllEdges()
	if err != nil {
		t.Fatalf("AllEdges: %v", err)
	}
	want := []Edge{
		{Src: "a.Caller", Dst: "b.Later", Kind: "CALLS", DstExternal: true, Confidence: "extracted"},
		{Src: "a.Caller", Dst: "x.Lib", Kind: "CALLS", DstExternal: true, Confidence: "extracted"},
	}
	if !reflect.DeepEqual(edges, want) {
		t.Fatalf("AllEdges = %+v, want %+v", edges, want)
	}
}
