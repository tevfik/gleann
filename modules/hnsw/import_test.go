package hnsw

import (
	"testing"
)

func TestNewGraphFromTopology_Basic(t *testing.T) {
	// Build a small 5-node topology manually.
	topo := GraphTopology{
		NumNodes:   5,
		Dimensions: 3,
		M:          4,
		MaxLevel:   1,
		EntryPoint: 0,
		NodeLevels: []int{1, 0, 0, 0, 0}, // node 0 is at level 1
		Embeddings: [][]float32{
			{1, 0, 0},
			{0, 1, 0},
			{0, 0, 1},
			{1, 1, 0},
			{0, 1, 1},
		},
		Neighbors: [][][]int64{
			{{1, 3}, {4}}, // node 0: level 0 = [1,3], level 1 = [4]
			{{0, 3, 4}},   // node 1: level 0
			{{4}},         // node 2
			{{0, 1}},      // node 3
			{{1, 2}},      // node 4
		},
	}

	g := NewGraphFromTopology(topo, L2DistanceSquared)

	if g.Size() != 5 {
		t.Fatalf("Size = %d, want 5", g.Size())
	}
	if g.MaxLevel() != 1 {
		t.Fatalf("MaxLevel = %d, want 1", g.MaxLevel())
	}
	if g.GetEntryPoint() != 0 {
		t.Fatalf("EntryPoint = %d, want 0", g.GetEntryPoint())
	}
	if g.Dimensions() != 3 {
		t.Fatalf("Dimensions = %d, want 3", g.Dimensions())
	}

	// Verify node data.
	node, ok := g.GetNode(0)
	if !ok {
		t.Fatal("node 0 not found")
	}
	if node.Level != 1 {
		t.Errorf("node 0 level = %d, want 1", node.Level)
	}
	if len(node.Neighbors) != 2 {
		t.Errorf("node 0 neighbor levels = %d, want 2", len(node.Neighbors))
	}

	// Search should work.
	results := g.Search([]float32{1, 0, 0}, 3, 32)
	if len(results) == 0 {
		t.Fatal("empty search results")
	}
	// Closest to {1,0,0} should be node 0 (exact match).
	if results[0].ID != 0 {
		t.Errorf("top result = %d, want 0", results[0].ID)
	}
}

func TestNewGraphFromTopology_CSRRoundTrip(t *testing.T) {
	// Build a normal graph, convert to topology, rebuild, convert to CSR.
	g := NewGraph(16, 100, 32)
	for i := 0; i < 100; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = float32(i*32+j) / 3200.0
		}
		g.Insert(int64(i), vec)
	}

	// Convert original graph to CSR.
	csr1 := ConvertToCSR(g)

	// Now extract topology from the graph and rebuild.
	allIDs := g.AllNodeIDs()
	topo := GraphTopology{
		NumNodes:   len(allIDs),
		Dimensions: g.Dimensions(),
		M:          g.m,
		MaxLevel:   g.MaxLevel(),
		EntryPoint: g.GetEntryPoint(),
		NodeLevels: make([]int, len(allIDs)),
		Embeddings: make([][]float32, len(allIDs)),
		Neighbors:  make([][][]int64, len(allIDs)),
	}

	for idx, id := range allIDs {
		node, _ := g.GetNode(id)
		topo.NodeLevels[idx] = node.Level
		topo.Embeddings[idx] = node.Vector
		topo.Neighbors[idx] = node.Neighbors
	}

	g2 := NewGraphFromTopology(topo, L2DistanceSquared)
	csr2 := ConvertToCSR(g2)

	if csr1.NumNodes != csr2.NumNodes {
		t.Errorf("NumNodes mismatch: %d vs %d", csr1.NumNodes, csr2.NumNodes)
	}
	if csr1.MaxLevel != csr2.MaxLevel {
		t.Errorf("MaxLevel mismatch: %d vs %d", csr1.MaxLevel, csr2.MaxLevel)
	}
}
