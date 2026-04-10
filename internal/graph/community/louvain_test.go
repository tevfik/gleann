//go:build treesitter

package community

import (
	"testing"
)

func TestLouvainBasic(t *testing.T) {
	g := NewGraph()

	// Create two clear clusters: A-B-C and D-E-F with one cross edge.
	g.AddNode(Node{ID: "A", Name: "A", Kind: "function"})
	g.AddNode(Node{ID: "B", Name: "B", Kind: "function"})
	g.AddNode(Node{ID: "C", Name: "C", Kind: "function"})
	g.AddNode(Node{ID: "D", Name: "D", Kind: "function"})
	g.AddNode(Node{ID: "E", Name: "E", Kind: "function"})
	g.AddNode(Node{ID: "F", Name: "F", Kind: "function"})

	// Cluster 1: A-B, A-C, B-C (dense)
	g.AddEdge("A", "B", 1.0)
	g.AddEdge("A", "C", 1.0)
	g.AddEdge("B", "C", 1.0)

	// Cluster 2: D-E, D-F, E-F (dense)
	g.AddEdge("D", "E", 1.0)
	g.AddEdge("D", "F", 1.0)
	g.AddEdge("E", "F", 1.0)

	// One weak cross-cluster edge.
	g.AddEdge("C", "D", 0.1)

	result, err := Detect(g, 3, 10)
	if err != nil {
		t.Fatal(err)
	}

	if result.NodeCount != 6 {
		t.Errorf("expected 6 nodes, got %d", result.NodeCount)
	}

	// Should detect at least 2 communities.
	if len(result.Communities) < 2 {
		t.Errorf("expected at least 2 communities, got %d", len(result.Communities))
	}

	// Modularity should be positive for a clearly modular graph.
	if result.Modularity <= 0 {
		t.Errorf("expected positive modularity, got %f", result.Modularity)
	}
}

func TestGodNodeDetection(t *testing.T) {
	g := NewGraph()

	// Hub node connected to many others.
	g.AddNode(Node{ID: "hub", Name: "hub", Kind: "function"})
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		g.AddNode(Node{ID: id, Name: id, Kind: "function"})
		g.AddEdge("hub", id, 1.0)
	}

	result, err := Detect(g, 5, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.GodNodes) == 0 {
		t.Error("expected at least one god node")
	}
	if result.GodNodes[0].ID != "hub" {
		t.Errorf("expected 'hub' as top god node, got %q", result.GodNodes[0].ID)
	}
}

func TestSurprisingEdges(t *testing.T) {
	g := NewGraph()

	// Two clusters with a cross-cluster edge.
	g.AddNode(Node{ID: "a1", Name: "a1", Kind: "function"})
	g.AddNode(Node{ID: "a2", Name: "a2", Kind: "function"})
	g.AddNode(Node{ID: "b1", Name: "b1", Kind: "function"})
	g.AddNode(Node{ID: "b2", Name: "b2", Kind: "function"})

	g.AddEdge("a1", "a2", 1.0)
	g.AddEdge("b1", "b2", 1.0)
	g.AddEdge("a1", "b1", 0.5) // cross-cluster

	result, err := Detect(g, 10, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.SurprisingEdges) == 0 {
		t.Error("expected at least one surprising edge")
	}
}

func TestEmptyGraph(t *testing.T) {
	g := NewGraph()
	result, err := Detect(g, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeCount != 0 {
		t.Errorf("expected 0 nodes, got %d", result.NodeCount)
	}
}

func TestExtractPackage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"github.com/org/repo/pkg/server.Handler", "server"},
		{"internal/graph/kuzu/db.go", "db"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := extractPackage(tt.input)
		if got != tt.expected {
			t.Errorf("extractPackage(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
