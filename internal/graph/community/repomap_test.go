//go:build treesitter

package community

import (
	"strings"
	"testing"
)

func TestGenerateRepoMap_Simple(t *testing.T) {
	nodes := []Node{
		{ID: "pkg.Handler", Name: "Handler", Kind: "function", File: "pkg/handler.go"},
		{ID: "pkg.Service", Name: "Service", Kind: "type", File: "pkg/service.go"},
		{ID: "main.main", Name: "main", Kind: "function", File: "main.go"},
	}
	edges := []Edge{
		{From: "main.main", To: "pkg.Handler"},
		{From: "pkg.Handler", To: "pkg.Service"},
	}

	cfg := DefaultRepoMapConfig()
	result := GenerateRepoMap(nodes, edges, cfg)
	if result == "" {
		t.Fatal("expected non-empty map")
	}
	if !strings.Contains(result, "Repository Map") {
		t.Error("expected header")
	}
	if !strings.Contains(result, "pkg.Handler") {
		t.Error("expected Handler in map")
	}
	if !strings.Contains(result, "pkg.Service") {
		t.Error("expected Service in map")
	}
}

func TestGenerateRepoMap_Empty(t *testing.T) {
	result := GenerateRepoMap(nil, nil, DefaultRepoMapConfig())
	if result != "" {
		t.Error("expected empty string for no nodes")
	}
}

func TestGenerateRepoMap_TokenLimit(t *testing.T) {
	nodes := make([]Node, 100)
	edges := make([]Edge, 0)
	for i := 0; i < 100; i++ {
		nodes[i] = Node{
			ID:   strings.Repeat("x", 50) + string(rune('A'+i%26)) + string(rune('0'+i%10)),
			Name: "func" + string(rune('A'+i%26)),
			Kind: "function",
			File: "file" + string(rune('0'+i%10)) + ".go",
		}
	}
	for i := 0; i < 99; i++ {
		edges = append(edges, Edge{From: nodes[i].ID, To: nodes[i+1].ID})
	}

	cfg := DefaultRepoMapConfig()
	cfg.MaxTokens = 100 // very small budget
	result := GenerateRepoMap(nodes, edges, cfg)
	// Should be truncated.
	tokens := len(result) / 4
	if tokens > 200 { // allow some slack
		t.Errorf("expected result within token budget, got ~%d tokens (%d bytes)", tokens, len(result))
	}
}

func TestGenerateRepoMapFromGraph(t *testing.T) {
	g := NewGraph()
	g.AddNode(Node{ID: "A", Name: "A", Kind: "function", File: "a.go"})
	g.AddNode(Node{ID: "B", Name: "B", Kind: "function", File: "b.go"})
	g.AddEdge("A", "B", 1.0)

	cfg := DefaultRepoMapConfig()
	result := GenerateRepoMapFromGraph(g, cfg)
	if result == "" {
		t.Fatal("expected non-empty map from graph")
	}
}
