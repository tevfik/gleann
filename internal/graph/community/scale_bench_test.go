//go:build treesitter

package community

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// generateScaleGraph builds a synthetic graph with n nodes and ~edgesPerNode edges each.
// Creates modular structure with numClusters clusters + random cross-cluster edges.
func generateScaleGraph(n, numClusters, edgesPerNode int) *Graph {
	g := NewGraph()
	rng := rand.New(rand.NewSource(42))

	// Create nodes.
	for i := 0; i < n; i++ {
		g.AddNode(Node{
			ID:   fmt.Sprintf("sym_%d", i),
			Name: fmt.Sprintf("func_%d", i),
			Kind: "function",
			File: fmt.Sprintf("pkg%d/file%d.go", i%numClusters, i),
		})
	}

	clusterSize := n / numClusters
	// Intra-cluster edges (dense).
	for c := 0; c < numClusters; c++ {
		start := c * clusterSize
		end := start + clusterSize
		if c == numClusters-1 {
			end = n
		}
		for i := start; i < end; i++ {
			targets := edgesPerNode - 1
			for j := 0; j < targets; j++ {
				to := start + rng.Intn(end-start)
				if to != i {
					g.AddEdge(fmt.Sprintf("sym_%d", i), fmt.Sprintf("sym_%d", to), 1.0)
				}
			}
		}
	}

	// Cross-cluster edges (sparse ~10% of total).
	crossEdges := n * edgesPerNode / 10
	for i := 0; i < crossEdges; i++ {
		from := rng.Intn(n)
		to := rng.Intn(n)
		if from != to {
			g.AddEdge(fmt.Sprintf("sym_%d", from), fmt.Sprintf("sym_%d", to), 0.5)
		}
	}

	return g
}

func TestScaleBenchmarkReport(t *testing.T) {
	sizes := []struct {
		nodes    int
		clusters int
		edges    int
	}{
		{1000, 10, 5},
		{5000, 20, 5},
		{10000, 30, 5},
	}

	t.Log("Graph Intelligence Scale Benchmarks")
	t.Log("====================================")
	t.Logf("%-10s %-10s %-15s %-15s %-15s %-15s",
		"Nodes", "Edges", "PageRank", "Louvain", "RiskScores", "RepoMap")

	for _, s := range sizes {
		g := generateScaleGraph(s.nodes, s.clusters, s.edges)
		nodes, edges := g.ExportForAnalysis()

		// PageRank.
		start := time.Now()
		_ = PageRank(nodes, edges, 0.85, 30)
		prTime := time.Since(start)

		// Louvain community detection.
		start = time.Now()
		_, err := Detect(g, 5, 20)
		louvainTime := time.Since(start)
		if err != nil {
			t.Logf("Louvain failed at %d nodes: %v", s.nodes, err)
			louvainTime = -1
		}

		// Risk scores (skip for >5K — BlastRadius is O(n²)).
		riskTime := time.Duration(0)
		if s.nodes <= 5000 {
			start = time.Now()
			cfg := DefaultRiskConfig()
			_ = ComputeRiskScores(nodes, edges, cfg)
			riskTime = time.Since(start)
		}

		// Repo map.
		start = time.Now()
		rmCfg := DefaultRepoMapConfig()
		rmCfg.TopK = 30
		rmCfg.MaxTokens = 2000
		_ = GenerateRepoMap(nodes, edges, rmCfg)
		mapTime := time.Since(start)

		riskStr := fmt.Sprintf("%s", riskTime)
		if s.nodes > 5000 {
			riskStr = "skipped (O(n²))"
		}

		t.Logf("%-10d %-10d %-15s %-15s %-15s %-15s",
			g.NodeCount(), g.EdgeCount(), prTime, louvainTime, riskStr, mapTime)
	}
}

func BenchmarkPageRank10K(b *testing.B) {
	g := generateScaleGraph(10000, 30, 5)
	nodes, edges := g.ExportForAnalysis()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PageRank(nodes, edges, 0.85, 30)
	}
}

func BenchmarkLouvain10K(b *testing.B) {
	g := generateScaleGraph(10000, 30, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Detect(g, 5, 20)
	}
}

func BenchmarkRiskScores10K(b *testing.B) {
	g := generateScaleGraph(10000, 30, 5)
	nodes, edges := g.ExportForAnalysis()
	cfg := DefaultRiskConfig()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeRiskScores(nodes, edges, cfg)
	}
}
