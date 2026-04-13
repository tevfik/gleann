package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"time"

	"github.com/tevfik/gleann/modules/hnsw"
)

type BenchResult struct {
	BuildTimeS           float64 `json:"build_time_s"`
	BuildMemMB           float64 `json:"build_mem_mb"`
	SearchTotalS         float64 `json:"search_total_s"`
	SearchPerQueryUS     float64 `json:"search_per_query_us"`
	QPS                  float64 `json:"qps"`
	RecallAt10           float64 `json:"recall_at_10"`
	BruteForceTotalS     float64 `json:"brute_force_total_s"`
	BruteForcePerQueryUS float64 `json:"brute_force_per_query_us"`
	SpeedupVsBrute       float64 `json:"speedup_vs_brute"`
}

func randomVector(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var norm float32
	for i := range v {
		v[i] = float32(rng.NormFloat64())
		norm += v[i] * v[i]
	}
	norm = float32(math.Sqrt(float64(norm)))
	for i := range v {
		v[i] /= norm
	}
	return v
}

func l2Dist(a, b []float32) float32 {
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

func bruteForceKNN(query []float32, vectors [][]float32, k int) []int64 {
	type scored struct {
		id   int64
		dist float32
	}
	scores := make([]scored, len(vectors))
	for i, v := range vectors {
		scores[i] = scored{int64(i), l2Dist(query, v)}
	}
	// Selection sort top-k (same complexity as np.argpartition for small k).
	for i := 0; i < k && i < len(scores); i++ {
		minIdx := i
		for j := i + 1; j < len(scores); j++ {
			if scores[j].dist < scores[minIdx].dist {
				minIdx = j
			}
		}
		scores[i], scores[minIdx] = scores[minIdx], scores[i]
	}
	result := make([]int64, k)
	for i := 0; i < k; i++ {
		result[i] = scores[i].id
	}
	return result
}

func recallAtK(predicted []hnsw.Candidate, trueNN []int64, k int) float64 {
	trueSet := make(map[int64]bool, k)
	for i := 0; i < k && i < len(trueNN); i++ {
		trueSet[trueNN[i]] = true
	}
	hits := 0
	for i := 0; i < k && i < len(predicted); i++ {
		if trueSet[predicted[i].ID] {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

func benchHNSW(n, dim, numQueries, k, efSearch int) BenchResult {
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = randomVector(rng, dim)
	}

	qrng := rand.New(rand.NewSource(99))
	queries := make([][]float32, numQueries)
	for i := range queries {
		queries[i] = randomVector(qrng, dim)
	}

	var res BenchResult

	// Build.
	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	t0 := time.Now()
	g := hnsw.NewGraph(32, 200, dim)
	for i, v := range vectors {
		g.Insert(int64(i), v)
	}
	res.BuildTimeS = time.Since(t0).Seconds()

	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	res.BuildMemMB = float64(memAfter.Alloc-memBefore.Alloc) / (1024 * 1024)

	// Warmup.
	for i := 0; i < 3; i++ {
		for _, q := range queries {
			g.Search(q, k, efSearch)
		}
	}

	// Search.
	t0 = time.Now()
	allResults := make([][]hnsw.Candidate, numQueries)
	for i, q := range queries {
		allResults[i] = g.Search(q, k, efSearch)
	}
	searchTime := time.Since(t0).Seconds()
	res.SearchTotalS = searchTime
	res.SearchPerQueryUS = searchTime / float64(numQueries) * 1e6
	res.QPS = float64(numQueries) / searchTime

	// Recall.
	totalRecall := 0.0
	for i, q := range queries {
		trueNN := bruteForceKNN(q, vectors, k)
		totalRecall += recallAtK(allResults[i], trueNN, k)
	}
	res.RecallAt10 = totalRecall / float64(numQueries)

	// Brute force baseline.
	t0 = time.Now()
	for _, q := range queries {
		bruteForceKNN(q, vectors, k)
	}
	bruteTime := time.Since(t0).Seconds()
	res.BruteForceTotalS = bruteTime
	res.BruteForcePerQueryUS = bruteTime / float64(numQueries) * 1e6
	res.SpeedupVsBrute = bruteTime / searchTime

	return res
}

func main() {
	fmt.Println(fmt.Sprintf("%s", "================================================================="))
	fmt.Println("  gleann-go Pure Go HNSW Benchmark")
	fmt.Printf("  Go %s, GOMAXPROCS=%d\n", runtime.Version(), runtime.GOMAXPROCS(0))
	fmt.Printf("  PID: %d\n", os.Getpid())
	fmt.Println("=================================================================")

	// Startup time — Go has near-zero import overhead.
	fmt.Printf("\nStartup overhead: ~0ms (compiled binary, no runtime imports)\n")

	configs := []struct{ n, dim int }{
		{1000, 128},
		{5000, 128},
		{1000, 384},
		{5000, 384},
		{1000, 768},
		{5000, 768},
	}

	allResults := make(map[string]BenchResult)

	for _, cfg := range configs {
		label := fmt.Sprintf("N=%d, dim=%d", cfg.n, cfg.dim)
		fmt.Printf("\n%s\n  %s\n%s\n",
			"──────────────────────────────────────────────────",
			label,
			"──────────────────────────────────────────────────")

		res := benchHNSW(cfg.n, cfg.dim, 100, 10, 128)
		allResults[label] = res

		fmt.Printf("  Build:         %8.1f ms\n", res.BuildTimeS*1000)
		fmt.Printf("  Search (100q): %7.0f µs/q   (%.0f QPS)\n", res.SearchPerQueryUS, res.QPS)
		fmt.Printf("  Brute force:   %7.0f µs/q\n", res.BruteForcePerQueryUS)
		fmt.Printf("  Speedup:       %7.1fx\n", res.SpeedupVsBrute)
		fmt.Printf("  Recall@10:     %7.1f%%\n", res.RecallAt10*100)
		fmt.Printf("  Build memory:  %7.1f MB\n", res.BuildMemMB)
	}

	// Save JSON.
	data, _ := json.MarshalIndent(allResults, "", "  ")
	os.WriteFile("/tmp/go_bench_results.json", data, 0644)
	fmt.Printf("\nResults saved to /tmp/go_bench_results.json\n")
}
