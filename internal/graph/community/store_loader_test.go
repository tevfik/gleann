package community

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
)

func TestLoadCallGraphFromStoreIsCallsOnly(t *testing.T) {
	g := openLoaderStore(t)
	delta := graphstore.FileDelta{
		File: "a.go",
		Symbols: []graphstore.Symbol{
			testSym("p.A", "A", "function", "a.go"),
			testSym("p.B", "B", "function", "a.go"),
			testSym("p.I", "I", "interface", "a.go"),
		},
		Edges: []graphstore.Edge{
			{Src: "p.A", Dst: "p.B"},
			{Src: "p.B", Dst: "p.I", Kind: "IMPLEMENTS"},
			{Src: "p.A", Dst: "fmt.Println", DstExternal: true},
		},
	}
	if err := g.ApplyFileDelta(context.Background(), delta); err != nil {
		t.Fatalf("ApplyFileDelta: %v", err)
	}
	graph, err := LoadCallGraphFromStore(g)
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeCount() != 3 || graph.GetNode("a.go") != nil {
		t.Fatalf("want the 3 symbols and no file node, got %d nodes", graph.NodeCount())
	}
	edges := graph.Edges()
	if len(edges) != 1 || edges[0].From != "p.A" || edges[0].To != "p.B" {
		t.Fatalf("want the single internal CALLS edge A->B, got %+v", edges)
	}
	res, err := FromStore(g, 5, 20)
	if err != nil || res.NodeCount != 4 {
		t.Fatalf("FromStore: res=%+v err=%v, want 4 nodes (3 symbols + file)", res, err)
	}
}
