package community

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// The untagged counterpart of LoadGraphFromKuzu (RFC 003 section 6.2): it
// reads symbols and edges (by kind) from the format-2 tables into the same
// in-memory Graph value louvain.go and pagerank.go consume, in the lite build.

func openLoaderStore(t *testing.T) *graphstore.SQLite {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return graphstore.New(st)
}

func testSym(fqn, name, kind, file string) graphstore.Symbol {
	return graphstore.Symbol{
		FQN: fqn, Name: name, Kind: kind, File: file,
		StartLine: 1, EndLine: 2, StartCol: 1, ByteOffset: 1,
	}
}

func TestLoadGraphFromStore(t *testing.T) {
	g := openLoaderStore(t)
	delta := graphstore.FileDelta{
		File: "a.go",
		Symbols: []graphstore.Symbol{
			testSym("p.A", "A", "function", "a.go"),
			testSym("p.B", "B", "function", "a.go"),
			testSym("p.I", "I", "interface", "a.go"),
		},
		Edges: []graphstore.Edge{
			{Src: "p.A", Dst: "p.B"},                            // CALLS 1.0
			{Src: "p.B", Dst: "p.I", Kind: "IMPLEMENTS"},        // 0.8
			{Src: "p.A", Dst: "fmt.Println", DstExternal: true}, // external: skipped
			{Src: "p.A", Dst: "p.I", Kind: "REFERENCES"},        // not loaded (kuzu loader ignored it)
		},
	}
	if err := g.ApplyFileDelta(context.Background(), delta); err != nil {
		t.Fatalf("ApplyFileDelta: %v", err)
	}

	graph, err := LoadGraphFromStore(g)
	if err != nil {
		t.Fatalf("LoadGraphFromStore: %v", err)
	}

	// 3 symbols + the declaring file node.
	if graph.NodeCount() != 4 {
		t.Fatalf("NodeCount = %d, want 4", graph.NodeCount())
	}
	if n := graph.GetNode("a.go"); n == nil || n.Kind != "file" {
		t.Fatalf("missing file node a.go (kind file): %+v", n)
	}
	if n := graph.GetNode("p.A"); n == nil || n.Kind != "function" {
		t.Fatalf("missing symbol node p.A: %+v", n)
	}

	// CALLS(1.0) + IMPLEMENTS(0.8) + three DECLARES(0.3 each); the external
	// CALLS and the REFERENCES edge are not loaded.
	if diff := math.Abs(graph.totalWeight - 2.7); diff > 1e-9 {
		t.Fatalf("totalWeight = %v, want 2.7", graph.totalWeight)
	}
	// Edges: A→B, B→I, plus a.go→A/B/I.
	if graph.EdgeCount() != 5 {
		t.Fatalf("EdgeCount = %d, want 5", graph.EdgeCount())
	}
	if _, ok := graph.neighbors["p.A"]["fmt.Println"]; ok {
		t.Fatal("external CALLS target fmt.Println must not be loaded")
	}

	// The loaded graph must feed the existing louvain path unchanged.
	if _, err := Detect(graph, 5, 20); err != nil {
		t.Fatalf("Detect on the loaded graph: %v", err)
	}
}
