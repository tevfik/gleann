package graphstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// A substring that matches more symbols than the trigram join may sort
// (ftsJoinLimit) falls back to the fqn-ordered scan, which still returns the
// first symbolSearchLimit matches in fqn order.
func TestSymbolsCommonSubstringFallsBackToOrderedScan(t *testing.T) {
	g, _ := openTestGraph(t)
	const n = ftsJoinLimit + 10
	syms := make([]Symbol, n)
	for i := range syms {
		syms[i] = mkSym(fmt.Sprintf("big.CommonName%05d", i), "big.go", int64(i+1))
	}
	mustApply(t, g, FileDelta{File: "big.go", GraphHash: "v1", Symbols: syms})

	got, err := g.Symbols("commonname")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != symbolSearchLimit {
		t.Fatalf("got %d symbols, want the cap of %d", len(got), symbolSearchLimit)
	}
	fqns := make([]string, len(got))
	for i, s := range got {
		fqns[i] = s.FQN
	}
	if !sort.StringsAreSorted(fqns) || fqns[0] != "big.CommonName00000" {
		t.Fatalf("not the first matches in fqn order: %v ...", fqns[:3])
	}
}

func TestResolveSymbolsBlankQuery(t *testing.T) {
	g, _ := openTestGraph(t)
	seedFaultGraph(t, g)
	for _, q := range []string{"", "   ", "\t\n"} {
		if got, err := g.ResolveSymbols(q, DirectionCallers); err != nil || got != nil {
			t.Errorf("ResolveSymbols(%q) = %v, %v, want nothing", q, got, err)
		}
	}
}

// Path says there is none, rather than failing, when either end is unknown;
// the same symbol is trivially its own path.
func TestPathWithUnknownEnds(t *testing.T) {
	g, _ := openTestGraph(t)
	seedFaultGraph(t, g)
	if steps, err := g.Path("a.One", "a.One"); err != nil || steps != nil {
		t.Fatalf("same symbol = %v, %v", steps, err)
	}
	for _, c := range [][2]string{{"no.Such", "a.One"}, {"b.Caller", "no.Such"}} {
		_, err := g.Path(c[0], c[1])
		if err == nil || !strings.Contains(err.Error(), "no path") {
			t.Errorf("Path(%q, %q) err = %v, want a no-path error", c[0], c[1], err)
		}
	}
}

// A bulk load keeps the test flag and defaults a symbol without a file to the
// delta's file.
func TestReplaceAllKeepsTestFlagAndDefaultsFile(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	sym := mkSym("p.TestThing", "p_test.go", 1)
	sym.IsTest = true
	noFile := mkSym("p.Helper", "", 20)
	if err := g.ReplaceAll(ctx, []FileDelta{{File: "p_test.go", GraphHash: "h", Symbols: []Symbol{sym, noFile}}}); err != nil {
		t.Fatal(err)
	}
	all, err := g.AllSymbols()
	if err != nil {
		t.Fatal(err)
	}
	byFQN := map[string]Symbol{}
	for _, s := range all {
		byFQN[s.FQN] = s
	}
	if !byFQN["p.TestThing"].IsTest || byFQN["p.Helper"].IsTest {
		t.Fatalf("test flags = %v / %v", byFQN["p.TestThing"].IsTest, byFQN["p.Helper"].IsTest)
	}
	if byFQN["p.Helper"].File != "p_test.go" {
		t.Fatalf("a symbol without a file was anchored in %q, want the delta's file", byFQN["p.Helper"].File)
	}
}

// A document with no name is titled by its path.
func TestDocumentTOCUntitledDocument(t *testing.T) {
	g, _ := openTestGraph(t)
	d := docDelta("docs/plain.md")
	d.Nodes[1].Name = ""
	if err := g.ApplyDocDelta(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	toc, err := g.DocumentTOC("docs/plain.md")
	if err != nil {
		t.Fatal(err)
	}
	if toc.Title != "docs/plain.md" {
		t.Fatalf("Title = %q, want the path", toc.Title)
	}
}

func TestGraphFixtureClampsSizes(t *testing.T) {
	f := NewGraphFixture(0, -1, 0)
	if f.NSyms != 1 || f.NFiles != 1 || len(f.FQNs) != 1 {
		t.Fatalf("sizes below one must clamp to one: %d symbols, %d files, %d FQNs", f.NSyms, f.NFiles, len(f.FQNs))
	}
}
