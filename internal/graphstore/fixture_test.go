package graphstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// TestGraphFixtureDeterministic guards the shared generator (fixture.go): the
// same arguments produce the identical graph, every edge is owned by the file
// of its source symbol, and a bulk load stores exactly len(Edges) CALLS rows
// (the generator already drops the duplicate draws INSERT OR IGNORE would).
func TestGraphFixtureDeterministic(t *testing.T) {
	a := graphstore.NewGraphFixture(500, 2000, 10)
	b := graphstore.NewGraphFixture(500, 2000, 10)
	if len(a.Edges) != len(b.Edges) || len(a.FQNs) != 500 {
		t.Fatalf("fixture not deterministic: %d vs %d edges", len(a.Edges), len(b.Edges))
	}
	fileOf := make(map[string]string, len(a.FQNs))
	for i, fqn := range a.FQNs {
		fileOf[fqn] = a.Files[i]
	}
	for i, e := range a.Edges {
		if e.Src != b.Edges[i].Src || e.Dst != b.Edges[i].Dst {
			t.Fatalf("edge %d differs between two builds", i)
		}
		if e.File != fileOf[e.Src] {
			t.Fatalf("edge %q->%q owned by %q, want source file %q", e.Src, e.Dst, e.File, fileOf[e.Src])
		}
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "idx", "idx.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	g := graphstore.New(s)
	if err := g.ReplaceAll(context.Background(), a.Deltas()); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	st, err := g.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.CallEdges != len(a.Edges) || st.Symbols != 500 || st.Files != 10 {
		t.Fatalf("Stats = %+v; want %d CALLS, 500 symbols, 10 files", st, len(a.Edges))
	}
}
