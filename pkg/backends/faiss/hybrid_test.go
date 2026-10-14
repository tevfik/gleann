//go:build cgo && faiss

package faiss

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/tevfik/gleann/modules/hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ─────────────── Graph Extraction ───────────────

func TestExtractTopology(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 500, 64
	embeddings := randomVectors(rng, n, dim)

	m := 32
	index, err := buildFAISSIndex(embeddings, m, gleann.FAISSConfig{})
	if err != nil {
		t.Fatalf("buildFAISSIndex: %v", err)
	}
	defer FreeIndex(index)

	topo, err := ExtractTopology(index, embeddings, m)
	if err != nil {
		t.Fatalf("ExtractTopology: %v", err)
	}

	// Basic validation.
	if topo.NumNodes != n {
		t.Errorf("NumNodes = %d, want %d", topo.NumNodes, n)
	}
	if topo.Dimensions != dim {
		t.Errorf("Dimensions = %d, want %d", topo.Dimensions, dim)
	}
	if topo.EntryPoint < 0 || topo.EntryPoint >= int64(n) {
		t.Errorf("EntryPoint = %d, out of range [0, %d)", topo.EntryPoint, n)
	}

	// Every node should have at least level 0 neighbors.
	emptyNodes := 0
	for i := 0; i < n; i++ {
		if len(topo.Neighbors[i]) == 0 {
			t.Errorf("node %d has no level list", i)
			continue
		}
		if len(topo.Neighbors[i][0]) == 0 {
			emptyNodes++
		}
	}
	// FAISS HNSW: first inserted node may have 0 neighbors.
	if emptyNodes > 1 {
		t.Errorf("%d nodes have 0 level-0 neighbors (expected ≤1)", emptyNodes)
	}

	t.Logf("Extracted: %d nodes, maxLevel=%d, entryPoint=%d", n, topo.MaxLevel, topo.EntryPoint)
}

// ─────────────── Hybrid Build + Search ───────────────

func TestHybridBuildAndSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 1000, 128
	embeddings := randomVectors(rng, n, dim)

	config := gleann.DefaultConfig()
	config.Backend = "faiss-hybrid"
	config.HNSWConfig.M = 32
	config.HNSWConfig.EfSearch = 128
	config.HNSWConfig.PruneEmbeddings = false // keep all for search
	config.HNSWConfig.UseMmap = false

	ctx := context.Background()

	// Build with hybrid (FAISS build → CSR).
	builder := &HybridBuilder{config: config}
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("HybridBuilder.Build: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty index data")
	}

	// Verify it's valid CSR by loading into Go HNSW searcher.
	searcher := &hnsw.Searcher{}
	hnswConfig := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{
			M:               32,
			EfSearch:        128,
			PruneEmbeddings: false,
			UseMmap:         false,
		},
	}
	factory := &hnsw.Factory{}
	hnswSearcher := factory.NewSearcher(hnswConfig)
	_ = searcher

	meta := hnsw.IndexMeta{Dimensions: dim, Backend: "faiss-hybrid"}
	if err := hnswSearcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("HNSW Searcher.Load: %v", err)
	}
	defer hnswSearcher.Close()

	// Search.
	query := embeddings[42] // search for a known vector
	ids, dists, err := hnswSearcher.Search(ctx, query, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(ids) == 0 {
		t.Fatal("no search results")
	}

	// The query vector itself should be the top result (distance ≈ 0).
	if ids[0] != 42 {
		t.Errorf("top result = %d (dist=%.4f), want 42", ids[0], dists[0])
	}

	t.Logf("Search returned %d results, top: id=%d dist=%.4f", len(ids), ids[0], dists[0])
}

// ─────────────── Hybrid with Pruning + Recompute ───────────────

func TestHybridPrunedRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 500, 64
	embeddings := randomVectors(rng, n, dim)

	config := gleann.DefaultConfig()
	config.Backend = "faiss-hybrid"
	config.HNSWConfig.M = 32
	config.HNSWConfig.EfSearch = 128
	config.HNSWConfig.PruneEmbeddings = true
	config.HNSWConfig.PruneKeepFraction = 0.0 // keep only entry point
	config.HNSWConfig.UseMmap = false

	ctx := context.Background()

	builder := &HybridBuilder{config: config}
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Verify the CSR is compact.
	csr, err := hnsw.ReadCSR(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadCSR: %v", err)
	}
	stats := csr.Stats()
	t.Logf("CSR stats: stored=%d, pruned=%d, graph=%d KB, total=%d KB",
		stats.StoredEmbeddings, stats.PrunedEmbeddings,
		stats.GraphSizeBytes/1024, stats.TotalSizeBytes/1024)

	if stats.StoredEmbeddings > 2 { // entry point + maybe 1 more
		t.Errorf("expected ≤2 stored embeddings, got %d", stats.StoredEmbeddings)
	}

	// Load and search with recompute.
	hnswConfig := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{
			M:               32,
			EfSearch:        128,
			PruneEmbeddings: true,
			UseMmap:         false,
		},
	}
	factory := &hnsw.Factory{}
	hnswSearcher := factory.NewSearcher(hnswConfig)
	meta := hnsw.IndexMeta{Dimensions: dim, Backend: "faiss-hybrid"}
	if err := hnswSearcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer hnswSearcher.Close()

	// Create a recompute function that returns the original embeddings.
	recompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		result := make([][]float32, len(ids))
		for i, id := range ids {
			if id >= 0 && id < int64(n) {
				result[i] = embeddings[id]
			} else {
				result[i] = make([]float32, dim) // zero vector fallback
			}
		}
		return result, nil
	}

	query := embeddings[10]
	ids, _, err := hnswSearcher.SearchWithRecompute(ctx, query, 10, recompute)
	if err != nil {
		t.Fatalf("SearchWithRecompute: %v", err)
	}

	if len(ids) == 0 {
		t.Fatal("no results from SearchWithRecompute")
	}
	if ids[0] != 10 {
		t.Errorf("top result = %d, want 10", ids[0])
	}
	t.Logf("SearchWithRecompute: %d results, top=%d", len(ids), ids[0])
}

// ─────────────── Hybrid vs FAISS vs Go HNSW Benchmark ───────────────

func TestHybridComparison(t *testing.T) {
	configs := []struct {
		name string
		n    int
		dim  int
	}{
		{"1K×64d", 1000, 64},
		{"1K×128d", 1000, 128},
		{"5K×128d", 5000, 128},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			embeddings := randomVectors(rng, cfg.n, cfg.dim)
			ctx := context.Background()
			m := 32

			// ── FAISS build ──
			faissStart := time.Now()
			faissIndex, err := buildFAISSIndex(embeddings, m, gleann.FAISSConfig{})
			if err != nil {
				t.Fatalf("FAISS build: %v", err)
			}
			faissBuildTime := time.Since(faissStart)
			faissData, err := serializeIndex(faissIndex)
			if err != nil {
				FreeIndex(faissIndex)
				t.Fatalf("FAISS serialize: %v", err)
			}
			FreeIndex(faissIndex)

			// ── Hybrid build (FAISS → CSR) ──
			hybridConfig := gleann.DefaultConfig()
			hybridConfig.HNSWConfig.M = m
			hybridConfig.HNSWConfig.PruneEmbeddings = true
			hybridConfig.HNSWConfig.PruneKeepFraction = 0.0
			hybridConfig.HNSWConfig.UseMmap = false

			hybridStart := time.Now()
			hybridBuilder := &HybridBuilder{config: hybridConfig}
			hybridData, err := hybridBuilder.Build(ctx, embeddings)
			if err != nil {
				t.Fatalf("Hybrid build: %v", err)
			}
			hybridBuildTime := time.Since(hybridStart)

			// ── Pure Go build ──
			goConfig := hnsw.Config{
				HNSWConfig: hnsw.HNSWConfig{
					M:                 m,
					EfConstruction:    200,
					EfSearch:          128,
					PruneEmbeddings:   true,
					PruneKeepFraction: 0.0,
					UseMmap:           false,
				},
			}
			goFactory := &hnsw.Factory{}
			goBuilder := goFactory.NewBuilder(goConfig)

			goStart := time.Now()
			goData, err := goBuilder.Build(ctx, embeddings)
			if err != nil {
				t.Fatalf("Go build: %v", err)
			}
			goBuildTime := time.Since(goStart)

			// ── Report ──
			t.Logf("")
			t.Logf("╭──────────────────────────────────────────────────────────────╮")
			t.Logf("│  %-60s│", cfg.name)
			t.Logf("├──────────────┬──────────────┬──────────────┬────────────────┤")
			t.Logf("│              │ %-12s │ %-12s │ %-14s │", "FAISS", "Hybrid", "Pure Go")
			t.Logf("├──────────────┼──────────────┼──────────────┼────────────────┤")
			t.Logf("│ Build time   │ %12s │ %12s │ %14s │",
				faissBuildTime.Round(time.Millisecond),
				hybridBuildTime.Round(time.Millisecond),
				goBuildTime.Round(time.Millisecond))
			t.Logf("│ Index size   │ %10d KB │ %10d KB │ %12d KB │",
				len(faissData)/1024, len(hybridData)/1024, len(goData)/1024)
			t.Logf("│ Pruning      │ %12s │ %12s │ %14s │", "❌", "✅", "✅")
			t.Logf("│ Recompute    │ %12s │ %12s │ %14s │", "❌", "✅", "✅")
			t.Logf("│ Mmap         │ %12s │ %12s │ %14s │", "❌", "✅", "✅")
			t.Logf("╰──────────────┴──────────────┴──────────────┴────────────────╯")

			hybridSpeedup := float64(goBuildTime) / float64(hybridBuildTime)
			t.Logf("Hybrid build speedup over Pure Go: %.1fx", hybridSpeedup)

			storageSaving := 100.0 * (1.0 - float64(len(hybridData))/float64(len(faissData)))
			t.Logf("Hybrid storage saving vs FAISS: %.1f%%", storageSaving)
		})
	}
}

// ─────────────── Recall validation ───────────────

func TestHybridRecall(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 2000, 128
	embeddings := randomVectors(rng, n, dim)

	ctx := context.Background()
	m := 32
	topK := 10
	nQueries := 100
	queries := randomVectors(rng, nQueries, dim)

	// Build hybrid.
	hybridConfig := gleann.DefaultConfig()
	hybridConfig.HNSWConfig.M = m
	hybridConfig.HNSWConfig.EfSearch = 128
	hybridConfig.HNSWConfig.PruneEmbeddings = false
	hybridConfig.HNSWConfig.UseMmap = false

	builder := &HybridBuilder{config: hybridConfig}
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Search with Go HNSW.
	factory := &hnsw.Factory{}
	hnswConfig := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{M: m, EfSearch: 128, UseMmap: false},
	}
	searcher := factory.NewSearcher(hnswConfig)
	meta := hnsw.IndexMeta{Dimensions: dim}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	// Collect results.
	hybridResults := make([][]int64, nQueries)
	for i, q := range queries {
		ids, _, err := searcher.Search(ctx, q, topK)
		if err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		hybridResults[i] = ids
	}

	// Also build and search with standard FAISS for comparison.
	faissIndex, err := buildFAISSIndex(embeddings, m, gleann.FAISSConfig{})
	if err != nil {
		t.Fatalf("FAISS build: %v", err)
	}
	defer FreeIndex(faissIndex)

	faissSearcher := &Searcher{config: hybridConfig}
	faissData, _ := serializeIndex(faissIndex)
	if err := faissSearcher.Load(ctx, faissData, gleann.IndexMeta{Dimensions: dim}); err != nil {
		t.Fatalf("FAISS load: %v", err)
	}
	defer faissSearcher.Close()

	faissResults := make([][]int64, nQueries)
	for i, q := range queries {
		ids, _, err := faissSearcher.Search(ctx, q, topK)
		if err != nil {
			t.Fatalf("FAISS search %d: %v", i, err)
		}
		faissResults[i] = ids
	}

	// Compute recall vs brute force.
	hybridRecall := computeRecall(embeddings, queries, hybridResults, topK)
	faissRecall := computeRecall(embeddings, queries, faissResults, topK)

	// Compute overlap between hybrid and FAISS.
	overlap := computeOverlap(hybridResults, faissResults, topK)

	t.Logf("Recall@%d — hybrid: %.1f%%, FAISS: %.1f%%, overlap: %.1f%%",
		topK, hybridRecall*100, faissRecall*100, overlap*100)

	if hybridRecall < 0.90 {
		t.Errorf("hybrid recall@%d = %.1f%%, want ≥90%%", topK, hybridRecall*100)
	}
}

// computeOverlap measures what fraction of results are shared.
func computeOverlap(a, b [][]int64, k int) float64 {
	totalHits := 0
	total := 0
	for qi := range a {
		bSet := make(map[int64]bool, len(b[qi]))
		for _, id := range b[qi] {
			bSet[id] = true
		}
		for _, id := range a[qi] {
			if bSet[id] {
				totalHits++
			}
		}
		total += k
	}
	if total == 0 {
		return 0
	}
	return float64(totalHits) / float64(total)
}

// ─────────────── NewGraphFromTopology ───────────────

func TestNewGraphFromTopology(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 200, 32
	embeddings := randomVectors(rng, n, dim)

	m := 16
	index, err := buildFAISSIndex(embeddings, m, gleann.FAISSConfig{})
	if err != nil {
		t.Fatalf("buildFAISSIndex: %v", err)
	}
	defer FreeIndex(index)

	topo, err := ExtractTopology(index, embeddings, m)
	if err != nil {
		t.Fatalf("ExtractTopology: %v", err)
	}

	graph := hnsw.NewGraphFromTopology(topo, hnsw.L2DistanceSquared)

	// Verify the graph can be searched.
	query := embeddings[5]
	results := graph.Search(query, 5, 64)
	if len(results) == 0 {
		t.Fatal("no search results from imported graph")
	}
	if results[0].ID != 5 {
		t.Errorf("top result = %d, want 5", results[0].ID)
	}

	// Verify CSR conversion works.
	csr := hnsw.ConvertToCSR(graph)
	if csr.NumNodes != int64(n) {
		t.Errorf("CSR NumNodes = %d, want %d", csr.NumNodes, n)
	}

	t.Logf("Graph: %d nodes, maxLevel=%d, search OK", graph.Size(), graph.MaxLevel())
}

// ─────────────── Helper ───────────────

// BenchmarkHybridBuild benchmarks the hybrid build path.
func BenchmarkHybridBuild(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 1000, 128
	embeddings := randomVectors(rng, n, dim)

	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 32
	config.HNSWConfig.PruneEmbeddings = true
	config.HNSWConfig.PruneKeepFraction = 0.0
	config.HNSWConfig.UseMmap = false

	ctx := context.Background()
	builder := &HybridBuilder{config: config}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := builder.Build(ctx, embeddings)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHybridExtract benchmarks just the graph extraction step.
func BenchmarkHybridExtract(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	n, dim := 5000, 128
	embeddings := randomVectors(rng, n, dim)

	m := 32
	index, err := buildFAISSIndex(embeddings, m, gleann.FAISSConfig{})
	if err != nil {
		b.Fatal(err)
	}
	defer FreeIndex(index)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ExtractTopology(index, embeddings, m)
		if err != nil {
			b.Fatal(err)
		}
	}
	_ = fmt.Sprintf("") // suppress unused import if needed
}
