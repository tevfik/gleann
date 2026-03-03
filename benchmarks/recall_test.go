// Package benchmarks provides performance and quality benchmarks for gleann.
//
// This file implements retrieval quality evaluation, modeled after
// Python LEANN's benchmarks/enron_emails/evaluate_enron_emails.py.
//
// Unlike speed-only benchmarks, these tests validate that the HNSW graph
// returns the SAME results as brute-force (flat) search — measuring Recall@K.
//
// Run:
//
//	go test -v -run TestRecall ./benchmarks/
//	go test -v -run TestRecallReport ./benchmarks/
package benchmarks

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/tevfik/gleann-hnsw"
)

// ────────────────────────────────────────────────────────────────────────────
// Recall computation
// ────────────────────────────────────────────────────────────────────────────

// recallAtK computes Recall@K: |HNSW ∩ Flat| / K
func recallAtK(hnswIDs, flatIDs []int64, k int) float64 {
	if k > len(flatIDs) {
		k = len(flatIDs)
	}
	flatSet := make(map[int64]bool, k)
	for i := 0; i < k; i++ {
		flatSet[flatIDs[i]] = true
	}
	match := 0
	limit := k
	if limit > len(hnswIDs) {
		limit = len(hnswIDs)
	}
	for i := 0; i < limit; i++ {
		if flatSet[hnswIDs[i]] {
			match++
		}
	}
	return float64(match) / float64(k)
}

// ────────────────────────────────────────────────────────────────────────────
// Synthetic dataset generators
// ────────────────────────────────────────────────────────────────────────────

// generateClusteredData creates a synthetic dataset with clear cluster structure.
// Each cluster has a centroid and points are Gaussian-distributed around it.
// This is more realistic than uniform random data.
func generateClusteredData(rng *rand.Rand, n, dims, nClusters int) [][]float32 {
	vectors := make([][]float32, n)
	centroids := make([][]float32, nClusters)

	// Create cluster centroids (well-separated).
	for c := 0; c < nClusters; c++ {
		centroids[c] = make([]float32, dims)
		for d := 0; d < dims; d++ {
			centroids[c][d] = float32(rng.NormFloat64() * 5.0)
		}
	}

	// Assign each vector to a random cluster with Gaussian noise.
	for i := 0; i < n; i++ {
		cluster := rng.Intn(nClusters)
		vectors[i] = make([]float32, dims)
		for d := 0; d < dims; d++ {
			vectors[i][d] = centroids[cluster][d] + float32(rng.NormFloat64()*0.5)
		}
	}
	return vectors
}

// generateClusteredQueries creates query vectors sampled from the same
// distribution as the data (perturbed versions of existing vectors + random).
func generateClusteredQueries(rng *rand.Rand, vectors [][]float32, nQueries int) [][]float32 {
	dims := len(vectors[0])
	queries := make([][]float32, nQueries)

	for i := 0; i < nQueries; i++ {
		queries[i] = make([]float32, dims)
		if i < nQueries*3/4 {
			// 75% of queries: perturbed version of an existing vector.
			base := vectors[rng.Intn(len(vectors))]
			for d := 0; d < dims; d++ {
				queries[i][d] = base[d] + float32(rng.NormFloat64()*0.3)
			}
		} else {
			// 25% of queries: random (to test out-of-distribution).
			for d := 0; d < dims; d++ {
				queries[i][d] = float32(rng.NormFloat64() * 3.0)
			}
		}
	}
	return queries
}

// ────────────────────────────────────────────────────────────────────────────
// Helper: extract IDs from HNSW Candidates
// ────────────────────────────────────────────────────────────────────────────

func candidateIDs(candidates []hnsw.Candidate) []int64 {
	ids := make([]int64, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
	}
	return ids
}

// ────────────────────────────────────────────────────────────────────────────
// Core Recall Tests
// ────────────────────────────────────────────────────────────────────────────

// TestRecallBasic validates HNSW Recall@10 ≥ 90% on 1K vectors.
func TestRecallBasic(t *testing.T) {
	const (
		n        = 1000
		dims     = 64
		clusters = 10
		nQueries = 100
		topK     = 10
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	graph := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graph.Insert(int64(i), v)
	}

	var totalRecall float64
	for _, q := range queries {
		hnswIDs := candidateIDs(graph.Search(q, topK, 128))
		flatIDs := bruteForceKNN(q, vectors, topK)
		totalRecall += recallAtK(hnswIDs, flatIDs, topK)
	}

	avgRecall := totalRecall / float64(nQueries)
	t.Logf("Recall@%d: %.2f%% (n=%d, dims=%d, clusters=%d)",
		topK, avgRecall*100, n, dims, clusters)

	if avgRecall < 0.90 {
		t.Errorf("Recall@%d = %.2f%%, expected ≥ 90%%", topK, avgRecall*100)
	}
}

// TestRecallHighDim validates recall at 384 dimensions (typical for bge-m3).
func TestRecallHighDim(t *testing.T) {
	const (
		n        = 2000
		dims     = 384
		clusters = 20
		nQueries = 100
		topK     = 10
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	graph := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graph.Insert(int64(i), v)
	}

	var totalRecall float64
	for _, q := range queries {
		hnswIDs := candidateIDs(graph.Search(q, topK, 128))
		flatIDs := bruteForceKNN(q, vectors, topK)
		totalRecall += recallAtK(hnswIDs, flatIDs, topK)
	}

	avgRecall := totalRecall / float64(nQueries)
	t.Logf("Recall@%d: %.2f%% (n=%d, dims=%d, clusters=%d)",
		topK, avgRecall*100, n, dims, clusters)

	if avgRecall < 0.85 {
		t.Errorf("Recall@%d = %.2f%%, expected ≥ 85%%", topK, avgRecall*100)
	}
}

// TestRecallAtMultipleK tests Recall@1, @3, @5, @10, @20.
func TestRecallAtMultipleK(t *testing.T) {
	const (
		n        = 2000
		dims     = 128
		clusters = 15
		nQueries = 200
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	graph := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graph.Insert(int64(i), v)
	}

	ks := []int{1, 3, 5, 10, 20}
	thresholds := map[int]float64{
		1:  0.80,
		3:  0.85,
		5:  0.87,
		10: 0.90,
		20: 0.92,
	}

	for _, k := range ks {
		var totalRecall float64
		for _, q := range queries {
			hnswIDs := candidateIDs(graph.Search(q, k, 128))
			flatIDs := bruteForceKNN(q, vectors, k)
			totalRecall += recallAtK(hnswIDs, flatIDs, k)
		}
		avgRecall := totalRecall / float64(nQueries)

		t.Logf("Recall@%d = %.2f%%", k, avgRecall*100)
		if avgRecall < thresholds[k] {
			t.Errorf("Recall@%d = %.2f%%, expected ≥ %.0f%%",
				k, avgRecall*100, thresholds[k]*100)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Recall with Pruned Embeddings (Recomputation)
// ────────────────────────────────────────────────────────────────────────────

// TestRecallWithRecompute validates that pruned graphs achieve similar recall
// when using the recomputation path (the core GLEANN innovation).
func TestRecallWithRecompute(t *testing.T) {
	const (
		n        = 1000
		dims     = 64
		clusters = 10
		nQueries = 100
		topK     = 10
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	// Build and prune.
	graph := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graph.Insert(int64(i), v)
	}

	csr := hnsw.ConvertToCSR(graph)
	hnsw.PruneEmbeddings(graph, csr, 0.0) // Remove ALL embeddings.

	// Verify embeddings are actually pruned.
	prunedCount := 0
	for _, id := range graph.AllNodeIDs() {
		node, _ := graph.GetNode(id)
		if len(node.Vector) == 0 {
			prunedCount++
		}
	}
	t.Logf("Pruned %d/%d embeddings (%.0f%%)", prunedCount, n,
		float64(prunedCount)/float64(n)*100)

	// Create recompute function from stored vectors.
	recomputeFn := func(ids []int64) [][]float32 {
		result := make([][]float32, len(ids))
		for i, id := range ids {
			if id >= 0 && id < int64(len(vectors)) {
				result[i] = vectors[id]
			}
		}
		return result
	}

	// Build separate unpruned graph for baseline.
	fullGraph := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		fullGraph.Insert(int64(i), v)
	}

	var recallVsFlat float64
	var recallVsFull float64
	for _, q := range queries {
		flatIDs := bruteForceKNN(q, vectors, topK)
		fullIDs := candidateIDs(fullGraph.Search(q, topK, 128))
		recomputeIDs := candidateIDs(graph.SearchWithRecompute(q, topK, 128, recomputeFn))

		recallVsFlat += recallAtK(recomputeIDs, flatIDs, topK)
		recallVsFull += recallAtK(recomputeIDs, fullIDs, topK)
	}

	avgRecallVsFlat := recallVsFlat / float64(nQueries)
	avgRecallVsFull := recallVsFull / float64(nQueries)

	t.Logf("Recompute Recall@%d vs Flat:      %.2f%%", topK, avgRecallVsFlat*100)
	t.Logf("Recompute Recall@%d vs Full HNSW: %.2f%%", topK, avgRecallVsFull*100)

	if avgRecallVsFlat < 0.75 {
		t.Errorf("Recompute Recall@%d vs Flat = %.2f%%, expected ≥ 75%%",
			topK, avgRecallVsFlat*100)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// EfSearch Complexity Sweep
// ────────────────────────────────────────────────────────────────────────────

// TestRecallComplexitySweep finds the minimum efSearch needed for target recall.
// This mirrors Python LEANN's Stage 3 (binary search for complexity).
func TestRecallComplexitySweep(t *testing.T) {
	const (
		n        = 2000
		dims     = 128
		clusters = 15
		nQueries = 200
		topK     = 10
		target   = 0.95
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	graph := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graph.Insert(int64(i), v)
	}

	efValues := []int{16, 32, 64, 96, 128, 192, 256, 384, 512}
	bestEf := -1

	for _, ef := range efValues {
		var totalRecall float64
		for _, q := range queries {
			hnswIDs := candidateIDs(graph.Search(q, topK, ef))
			flatIDs := bruteForceKNN(q, vectors, topK)
			totalRecall += recallAtK(hnswIDs, flatIDs, topK)
		}
		avgRecall := totalRecall / float64(nQueries)

		t.Logf("  efSearch=%3d → Recall@%d = %.2f%%", ef, topK, avgRecall*100)
		if avgRecall >= target && bestEf == -1 {
			bestEf = ef
		}
	}

	if bestEf == -1 {
		t.Logf("Could not achieve %.0f%% Recall@%d with tested efSearch values",
			target*100, topK)
	} else {
		t.Logf("Minimum efSearch for %.0f%% Recall@%d: %d", target*100, topK, bestEf)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CSR Serialization Recall (round-trip)
// ────────────────────────────────────────────────────────────────────────────

// TestRecallAfterCSRRoundtrip ensures recall is preserved after
// Graph → CSR → serialize → deserialize → Graph round-trip.
func TestRecallAfterCSRRoundtrip(t *testing.T) {
	const (
		n        = 1000
		dims     = 64
		clusters = 10
		nQueries = 100
		topK     = 10
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	// Build original graph.
	original := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		original.Insert(int64(i), v)
	}

	// Round-trip through backend Build → Load.
	ctx := context.Background()
	factory := &hnsw.Factory{}
	config := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{
			M:                 32,
			EfConstruction:    200,
			EfSearch:          128,
			PruneEmbeddings:   false,
			PruneKeepFraction: 1.0,
		},
	}
	builder := factory.NewBuilder(config)
	indexData, err := builder.Build(ctx, vectors)
	if err != nil {
		t.Fatal(err)
	}

	searcher := factory.NewSearcher(config)
	if err := searcher.Load(ctx, indexData, hnsw.IndexMeta{}); err != nil {
		t.Fatal(err)
	}

	// Compare recall: original graph vs round-tripped graph.
	var recallOriginal, recallRoundtrip float64
	for _, q := range queries {
		flatIDs := bruteForceKNN(q, vectors, topK)

		origIDs := candidateIDs(original.Search(q, topK, 128))

		rtIDs, _, err := searcher.Search(ctx, q, topK)
		if err != nil {
			t.Fatal(err)
		}

		recallOriginal += recallAtK(origIDs, flatIDs, topK)
		recallRoundtrip += recallAtK(rtIDs, flatIDs, topK)
	}

	avgOriginal := recallOriginal / float64(nQueries)
	avgRoundtrip := recallRoundtrip / float64(nQueries)

	t.Logf("Original graph Recall@%d:     %.2f%%", topK, avgOriginal*100)
	t.Logf("CSR round-trip Recall@%d:     %.2f%%", topK, avgRoundtrip*100)

	diff := math.Abs(avgOriginal - avgRoundtrip)
	if diff > 0.05 {
		t.Errorf("Recall degradation after CSR round-trip: %.2f%% → %.2f%% (Δ=%.2f%%)",
			avgOriginal*100, avgRoundtrip*100, diff*100)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// M parameter sweep
// ────────────────────────────────────────────────────────────────────────────

// TestRecallMParameterSweep explores the recall–speed trade-off across M values.
func TestRecallMParameterSweep(t *testing.T) {
	const (
		n        = 2000
		dims     = 128
		clusters = 15
		nQueries = 200
		topK     = 10
		ef       = 128
	)

	rng := rand.New(rand.NewSource(42))
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	// Precompute ground truth.
	gt := make([][]int64, nQueries)
	for i, q := range queries {
		gt[i] = bruteForceKNN(q, vectors, topK)
	}

	mValues := []int{8, 16, 32, 48, 64}

	for _, m := range mValues {
		graph := hnsw.NewGraph(m, 200, dims)
		buildStart := time.Now()
		for i, v := range vectors {
			graph.Insert(int64(i), v)
		}
		buildDur := time.Since(buildStart)

		var totalRecall float64
		searchStart := time.Now()
		for i, q := range queries {
			hnswIDs := candidateIDs(graph.Search(q, topK, ef))
			totalRecall += recallAtK(hnswIDs, gt[i], topK)
		}
		searchDur := time.Since(searchStart)
		avgRecall := totalRecall / float64(nQueries)

		t.Logf("M=%2d → Recall@%d=%.2f%% | Build: %v | Search: %v (%.1fµs/query)",
			m, topK, avgRecall*100, buildDur.Round(time.Millisecond),
			searchDur.Round(time.Millisecond),
			float64(searchDur.Microseconds())/float64(nQueries))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Full Recall Report (mirrors Python LEANN's 5-stage evaluation)
// ────────────────────────────────────────────────────────────────────────────

// TestRecallReport generates a comprehensive retrieval quality report.
// Stages:
//  1. Build + baseline
//  2. Recall@K vs flat (multiple K values)
//  3. Complexity sweep (find min efSearch for 95% recall)
//  4. Compact vs non-compact comparison (storage saving + recall)
//  5. M parameter exploration
func TestRecallReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping comprehensive recall report in short mode")
	}

	const (
		n        = 5000
		dims     = 128
		clusters = 30
		nQueries = 500
	)

	rng := rand.New(rand.NewSource(42))
	t.Log("Generating synthetic dataset...")
	vectors := generateClusteredData(rng, n, dims, clusters)
	queries := generateClusteredQueries(rng, vectors, nQueries)

	// ── Stage 1: Build indexes ─────────────────────────────────────────
	t.Log("")
	t.Log("╔══════════════════════════════════════════════════════════╗")
	t.Log("║            GLEANN Retrieval Quality Report              ║")
	t.Log("╚══════════════════════════════════════════════════════════╝")
	t.Logf("\nDataset: %d vectors × %d dims, %d clusters, %d queries", n, dims, clusters, nQueries)

	t.Log("\n── Stage 1: Building Indexes ──")

	// Non-compact (all embeddings stored).
	buildStart := time.Now()
	graphFull := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graphFull.Insert(int64(i), v)
	}
	buildFullDur := time.Since(buildStart)
	t.Logf("  Full graph built in %v", buildFullDur.Round(time.Millisecond))

	// Compact (pruned embeddings).
	buildStart = time.Now()
	graphCompact := hnsw.NewGraph(32, 200, dims)
	for i, v := range vectors {
		graphCompact.Insert(int64(i), v)
	}
	csrCompact := hnsw.ConvertToCSR(graphCompact)
	hnsw.PruneEmbeddings(graphCompact, csrCompact, 0.0)
	buildCompactDur := time.Since(buildStart)
	t.Logf("  Compact graph built in %v (pruned to 0%%)", buildCompactDur.Round(time.Millisecond))

	// Count pruned.
	pruned := 0
	for _, id := range graphCompact.AllNodeIDs() {
		node, _ := graphCompact.GetNode(id)
		if len(node.Vector) == 0 {
			pruned++
		}
	}
	t.Logf("  Pruned embeddings: %d/%d (%.1f%%)", pruned, n,
		float64(pruned)/float64(n)*100)

	// Recompute function.
	recomputeFn := func(ids []int64) [][]float32 {
		result := make([][]float32, len(ids))
		for i, id := range ids {
			if id >= 0 && id < int64(len(vectors)) {
				result[i] = vectors[id]
			}
		}
		return result
	}

	// Precompute ground truth once (max K=50) to avoid repeated O(N) scans.
	maxK := 50
	groundTruth := make([][]int64, nQueries) // groundTruth[i] = top-50 IDs for query i
	t.Log("  Precomputing brute-force ground truth (top-50)...")
	gtStart := time.Now()
	for i, q := range queries {
		groundTruth[i] = bruteForceKNN(q, vectors, maxK)
	}
	t.Logf("  Ground truth computed in %v", time.Since(gtStart).Round(time.Millisecond))

	// ── Stage 2: Recall@K (full graph) ─────────────────────────────────
	t.Log("\n── Stage 2: Recall@K (Full Graph vs Flat) ──")

	ks := []int{1, 3, 5, 10, 20, 50}
	ef := 128

	t.Log("  ┌──────────┬──────────────┐")
	t.Log("  │    K     │  Recall@K    │")
	t.Log("  ├──────────┼──────────────┤")

	for _, k := range ks {
		var totalRecall float64
		for i, q := range queries {
			hnswIDs := candidateIDs(graphFull.Search(q, k, ef))
			gt := groundTruth[i]
			if k < len(gt) {
				gt = gt[:k]
			}
			totalRecall += recallAtK(hnswIDs, gt, k)
		}
		avgRecall := totalRecall / float64(nQueries)
		t.Logf("  │  %5d   │   %6.2f%%    │", k, avgRecall*100)
	}
	t.Log("  └──────────┴──────────────┘")

	// ── Stage 3: Complexity sweep ──────────────────────────────────────
	t.Log("\n── Stage 3: EfSearch Complexity Sweep ──")

	efValues := []int{16, 32, 48, 64, 96, 128, 192, 256, 384, 512}
	topK := 10
	targetRecall := 0.95
	bestEf := -1

	t.Log("  ┌──────────┬──────────────┬──────────────┐")
	t.Log("  │ efSearch │  Recall@10   │ Latency/q    │")
	t.Log("  ├──────────┼──────────────┼──────────────┤")

	for _, efVal := range efValues {
		var totalRecall float64
		start := time.Now()
		for i, q := range queries {
			hnswIDs := candidateIDs(graphFull.Search(q, topK, efVal))
			gt := groundTruth[i]
			if topK < len(gt) {
				gt = gt[:topK]
			}
			totalRecall += recallAtK(hnswIDs, gt, topK)
		}
		dur := time.Since(start)
		avgRecall := totalRecall / float64(nQueries)
		latencyUs := float64(dur.Microseconds()) / float64(nQueries)

		marker := "  "
		if avgRecall >= targetRecall && bestEf == -1 {
			bestEf = efVal
			marker = "* "
		}
		t.Logf("%s│  %5d   │   %6.2f%%    │ %8.1fus    │",
			marker, efVal, avgRecall*100, latencyUs)
	}
	t.Log("  └──────────┴──────────────┴──────────────┘")

	if bestEf > 0 {
		t.Logf("  Best efSearch for >=%.0f%% Recall@%d: %d",
			targetRecall*100, topK, bestEf)
	}

	// ── Stage 4: Compact vs Non-Compact ────────────────────────────────
	t.Log("\n── Stage 4: Compact (Recompute) vs Non-Compact ──")

	// Serialize to measure sizes.
	ctx := context.Background()
	factory := &hnsw.Factory{}

	configFull := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{
			M:                 32,
			EfConstruction:    200,
			EfSearch:          128,
			PruneEmbeddings:   false,
			PruneKeepFraction: 1.0,
		},
	}
	builderFull := factory.NewBuilder(configFull)
	fullData, err := builderFull.Build(ctx, vectors)
	if err != nil {
		t.Fatal(err)
	}

	configCompact := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{
			M:                 32,
			EfConstruction:    200,
			EfSearch:          128,
			PruneEmbeddings:   true,
			PruneKeepFraction: 0.0,
		},
	}
	builderCompact := factory.NewBuilder(configCompact)
	compactData, err := builderCompact.Build(ctx, vectors)
	if err != nil {
		t.Fatal(err)
	}

	fullSizeMB := float64(len(fullData)) / (1024 * 1024)
	compactSizeMB := float64(len(compactData)) / (1024 * 1024)
	saving := (1 - compactSizeMB/fullSizeMB) * 100

	t.Logf("  Full index:    %.2f MB", fullSizeMB)
	t.Logf("  Compact index: %.2f MB", compactSizeMB)
	t.Logf("  Storage saving: %.1f%%", saving)

	// Recall comparison: full vs compact+recompute.
	var recallFull, recallCompact float64
	var latencyFull, latencyCompact time.Duration

	for i, q := range queries {
		gt := groundTruth[i]
		if topK < len(gt) {
			gt = gt[:topK]
		}

		s1 := time.Now()
		fullIDs := candidateIDs(graphFull.Search(q, topK, ef))
		latencyFull += time.Since(s1)

		s2 := time.Now()
		compactIDs := candidateIDs(
			graphCompact.SearchWithRecompute(q, topK, ef, recomputeFn))
		latencyCompact += time.Since(s2)

		recallFull += recallAtK(fullIDs, gt, topK)
		recallCompact += recallAtK(compactIDs, gt, topK)
	}

	avgFull := recallFull / float64(nQueries)
	avgCompact := recallCompact / float64(nQueries)
	latFullUs := float64(latencyFull.Microseconds()) / float64(nQueries)
	latCompactUs := float64(latencyCompact.Microseconds()) / float64(nQueries)

	t.Log("  ┌───────────────┬──────────────┬──────────────┬──────────────┐")
	t.Log("  │     Mode      │  Recall@10   │ Latency/q    │   Size       │")
	t.Log("  ├───────────────┼──────────────┼──────────────┼──────────────┤")
	t.Logf("  │ Full          │   %6.2f%%    │ %8.1fus   │ %6.2f MB    │",
		avgFull*100, latFullUs, fullSizeMB)
	t.Logf("  │ Compact+Recom │   %6.2f%%    │ %8.1fus   │ %6.2f MB    │",
		avgCompact*100, latCompactUs, compactSizeMB)
	t.Log("  └───────────────┴──────────────┴──────────────┴──────────────┘")

	if latFullUs > 0 {
		t.Logf("  Speed ratio: Compact is %.1fx slower", latCompactUs/latFullUs)
	}
	if compactSizeMB > 0 {
		t.Logf("  Storage ratio: Compact is %.1fx smaller", fullSizeMB/compactSizeMB)
	}

	// ── Stage 5: M parameter exploration ───────────────────────────────
	t.Log("\n── Stage 5: M Parameter Trade-offs ──")
	t.Log("  ┌──────┬──────────────┬──────────────┬──────────────┐")
	t.Log("  │  M   │  Recall@10   │ Build time   │ Search/q     │")
	t.Log("  ├──────┼──────────────┼──────────────┼──────────────┤")

	mValues := []int{8, 16, 32, 48, 64}
	for _, m := range mValues {
		g := hnsw.NewGraph(m, 200, dims)
		bs := time.Now()
		for i, v := range vectors {
			g.Insert(int64(i), v)
		}
		buildDur := time.Since(bs)

		var totalRecall float64
		ss := time.Now()
		for i, q := range queries {
			hnswIDs := candidateIDs(g.Search(q, topK, ef))
			gt := groundTruth[i]
			if topK < len(gt) {
				gt = gt[:topK]
			}
			totalRecall += recallAtK(hnswIDs, gt, topK)
		}
		searchDur := time.Since(ss)
		avgRecall := totalRecall / float64(nQueries)

		t.Logf("  │ %3d  │   %6.2f%%    │ %10v   │ %8.1fus    │",
			m, avgRecall*100, buildDur.Round(time.Millisecond),
			float64(searchDur.Microseconds())/float64(nQueries))
	}
	t.Log("  └──────┴──────────────┴──────────────┴──────────────┘")

	t.Log("")
	t.Log("Report complete.")
}
