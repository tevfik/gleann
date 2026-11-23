package graphstore

import (
	"reflect"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// FileEdges lists the edges a file owns, of one kind, with their confidence
// and external flag, and nothing another file owns.
func TestFileEdges(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{File: "p/iface.go", Symbols: []Symbol{mkSym("m/p.Doer", "p/iface.go", 1)}})
	mustApply(t, g, FileDelta{
		File:    "p/a.go",
		Symbols: []Symbol{mkSym("m/p.A", "p/a.go", 1), mkSym("m/p.Run", "p/a.go", 9)},
		Edges: []Edge{
			{Src: "m/p.A", Dst: "m/p.Doer", Kind: "IMPLEMENTS", Confidence: "exact"},
			{Src: "m/p.A", Dst: "io.Reader", Kind: "IMPLEMENTS", DstExternal: true},
			{Src: "m/p.Run", Dst: "m/p.Doer", Kind: "CALLS", Confidence: "exact"},
		},
	})
	mustApply(t, g, FileDelta{
		File:    "p/b.go",
		Symbols: []Symbol{mkSym("m/p.B", "p/b.go", 1)},
		Edges:   []Edge{{Src: "m/p.B", Dst: "m/p.Doer", Kind: "IMPLEMENTS", Confidence: "exact"}},
	})

	got, err := g.FileEdges("p/a.go", "IMPLEMENTS")
	if err != nil {
		t.Fatal(err)
	}
	want := []Edge{
		{Src: "m/p.A", Dst: "io.Reader", Kind: "IMPLEMENTS", DstExternal: true, Confidence: "extracted"},
		{Src: "m/p.A", Dst: "m/p.Doer", Kind: "IMPLEMENTS", Confidence: "exact"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FileEdges(p/a.go, IMPLEMENTS) = %+v, want %+v", got, want)
	}
	calls, err := g.FileEdges("p/a.go", "CALLS")
	if err != nil || len(calls) != 1 || calls[0].Src != "m/p.Run" {
		t.Errorf("FileEdges(p/a.go, CALLS) = %+v, %v; want the one call of Run", calls, err)
	}
	if none, err := g.FileEdges("p/missing.go", "IMPLEMENTS"); err != nil || len(none) != 0 {
		t.Errorf("FileEdges of an unknown file = %+v, %v; want none", none, err)
	}
}

// A damaged database is an error, not an empty list: the caller would
// otherwise drop the edges it meant to carry over.
func TestFileEdgesReportsADamagedDatabase(t *testing.T) {
	seed := func(t *testing.T) (*SQLite, *store.Store) {
		t.Helper()
		g, s := openTestGraph(t)
		mustApply(t, g, FileDelta{File: "p/iface.go", Symbols: []Symbol{mkSym("m/p.Doer", "p/iface.go", 1)}})
		mustApply(t, g, FileDelta{
			File:    "p/a.go",
			Symbols: []Symbol{mkSym("m/p.A", "p/a.go", 1)},
			Edges:   []Edge{{Src: "m/p.A", Dst: "m/p.Doer", Kind: "IMPLEMENTS", Confidence: "exact"}},
		})
		return g, s
	}
	t.Run("query", func(t *testing.T) {
		g, s := seed(t)
		if _, err := s.WriterDB().Exec(`ALTER TABLE graph_edges RENAME COLUMN confidence TO zz`); err != nil {
			t.Fatal(err)
		}
		if _, err := g.FileEdges("p/a.go", "IMPLEMENTS"); err == nil {
			t.Error("no error with a renamed column")
		}
	})
	for _, tc := range []struct{ name, table, col, kind string }{
		{"error mid-scan", "graph_edges", "dst_external", "error"},
		{"NULL where a value is expected", "symbols", "fqn", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, s := seed(t)
			poisonColumn(t, s, tc.table, tc.col, tc.kind)
			if edges, err := g.FileEdges("p/a.go", "IMPLEMENTS"); err == nil {
				t.Errorf("FileEdges = %+v, nil; want an error", edges)
			}
		})
	}
}
