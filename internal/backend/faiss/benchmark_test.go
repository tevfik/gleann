//go:build cgo && faiss

package faiss

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	hnsw "github.com/tevfik/gleann-hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

// TestFAISSvsPureGo compares FAISS CGo backend against pure Go HNSW.
// Run with: go test -v -tags faiss -run TestFAISSvsPureGo -timeout 300s ./internal/backend/faiss/
func TestFAISSvsPureGo(t *testing.T) {
	configs := []struct {
		name string
		n    int
		dim  int
	}{
		{"1K×64d", 1000, 64},
		{"1K×128d", 1000, 128},
		{"5K×128d", 5000, 128},
		{"5K×384d", 5000, 384},
	}

	nQueries := 50
	topK := 10

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			embeddings := randomVectors(rng, cfg.n, cfg.dim)

			// Generate queries.
			queries := randomVectors(rng, nQueries, cfg.dim)

			ctx := context.Background()

			// ─── FAISS ───
			faissConfig := gleann.DefaultConfig()
			faissConfig.Backend = "faiss"
			faissConfig.HNSWConfig.M = 32
			faissConfig.HNSWConfig.EfSearch = 128

			faissBuilder := &Builder{config: faissConfig}
			faissStart := time.Now()
			faissData, err := faissBuilder.Build(ctx, embeddings)
			if err != nil {
				t.Fatalf("FAISS build: %v", err)
			}
			faissBuildTime := time.Since(faissStart)

			faissSearcher := &Searcher{config: faissConfig}
			meta := gleann.IndexMeta{Dimensions: cfg.dim, Backend: "faiss"}
			if err := faissSearcher.Load(ctx, faissData, meta); err != nil {
				t.Fatalf("FAISS load: %v", err)
			}

			faissStart = time.Now()
			faissResults := make([][]int64, nQueries)
			for i, q := range queries {
				ids, _, err := faissSearcher.Search(ctx, q, topK)
				if err != nil {
					t.Fatalf("FAISS search %d: %v", i, err)
				}
				faissResults[i] = ids
			}
			faissSearchTime := time.Since(faissStart)
			faissSearcher.Close()

			// ─── Pure Go HNSW ───
			goConfig := gleann.DefaultConfig()
			goConfig.Backend = "hnsw"
			goConfig.HNSWConfig.M = 32
			goConfig.HNSWConfig.EfConstruction = 200
			goConfig.HNSWConfig.PruneEmbeddings = false

			goFactory := &hnsw.Factory{}
			goBuilder := goFactory.NewBuilder(goConfig)
			goStart := time.Now()
			goData, err := goBuilder.Build(ctx, embeddings)
			if err != nil {
				t.Fatalf("Go build: %v", err)
			}
			goBuildTime := time.Since(goStart)

			goSearcher := goFactory.NewSearcher(goConfig)
			goMeta := gleann.IndexMeta{Dimensions: cfg.dim, Backend: "hnsw"}
			if err := goSearcher.Load(ctx, goData, goMeta); err != nil {
				t.Fatalf("Go load: %v", err)
			}

			goStart = time.Now()
			goResults := make([][]int64, nQueries)
			for i, q := range queries {
				ids, _, err := goSearcher.Search(ctx, q, topK)
				if err != nil {
					t.Fatalf("Go search %d: %v", i, err)
				}
				goResults[i] = ids
			}
			goSearchTime := time.Since(goStart)
			goSearcher.Close()

			// ─── Recall vs Brute Force ───
			faissRecall := computeRecall(embeddings, queries, faissResults, topK)
			goRecall := computeRecall(embeddings, queries, goResults, topK)

			// ─── Report ───
			buildSpeedup := float64(goBuildTime) / float64(faissBuildTime)
			searchSpeedup := float64(goSearchTime) / float64(faissSearchTime)

			t.Logf("")
			t.Logf("╭─────────────────────────────────────────────────╮")
			t.Logf("│ %-47s │", cfg.name)
			t.Logf("├──────────────┬────────────────┬────────────────┤")
			t.Logf("│              │ %-14s │ %-14s │", "FAISS (CGo)", "Pure Go")
			t.Logf("├──────────────┼────────────────┼────────────────┤")
			t.Logf("│ Build        │ %14s │ %14s │", faissBuildTime.Round(time.Millisecond), goBuildTime.Round(time.Millisecond))
			t.Logf("│ Search (%dq) │ %14s │ %14s │", nQueries, faissSearchTime.Round(time.Microsecond), goSearchTime.Round(time.Microsecond))
			t.Logf("│ Search/query │ %14s │ %14s │", (faissSearchTime / time.Duration(nQueries)).Round(time.Microsecond), (goSearchTime / time.Duration(nQueries)).Round(time.Microsecond))
			t.Logf("│ QPS          │ %14.0f │ %14.0f │",
				float64(nQueries)/faissSearchTime.Seconds(),
				float64(nQueries)/goSearchTime.Seconds())
			t.Logf("│ Recall@%d    │ %13.1f%% │ %13.1f%% │", topK, faissRecall*100, goRecall*100)
			t.Logf("│ Index size   │ %11d KB │ %11d KB │", len(faissData)/1024, len(goData)/1024)
			t.Logf("├──────────────┼────────────────┴────────────────┤")
			t.Logf("│ Build ⚡      │ FAISS %.1fx faster %15s│", buildSpeedup, "")
			t.Logf("│ Search ⚡     │ FAISS %.1fx faster %15s│", searchSpeedup, "")
			t.Logf("╰──────────────┴─────────────────────────────────╯")
		})
	}
}

// computeRecall computes recall@k against brute-force ground truth.
func computeRecall(embeddings [][]float32, queries [][]float32, hnswResults [][]int64, k int) float64 {
	totalHits := 0
	totalExpected := 0

	for qi, query := range queries {
		// Brute force top-k.
		type pair struct {
			id   int
			dist float32
		}
		dists := make([]pair, len(embeddings))
		for i, emb := range embeddings {
			var d float32
			for j := range query {
				diff := query[j] - emb[j]
				d += diff * diff
			}
			dists[i] = pair{i, d}
		}
		// Sort by distance.
		for i := 0; i < k && i < len(dists); i++ {
			for j := i + 1; j < len(dists); j++ {
				if dists[j].dist < dists[i].dist {
					dists[i], dists[j] = dists[j], dists[i]
				}
			}
		}

		groundTruth := make(map[int64]bool)
		for i := 0; i < k && i < len(dists); i++ {
			groundTruth[int64(dists[i].id)] = true
		}

		for _, id := range hnswResults[qi] {
			if groundTruth[id] {
				totalHits++
			}
		}
		totalExpected += k
	}

	if totalExpected == 0 {
		return 0
	}
	return float64(totalHits) / float64(totalExpected)
}

// BenchmarkFAISSBuild runs Go benchmarks for FAISS build speed.
func BenchmarkFAISSBuild(b *testing.B) {
	dims := 128
	n := 1000
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, n, dims)

	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 32
	builder := &Builder{config: config}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := builder.Build(ctx, embeddings)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFAISSSearch runs Go benchmarks for FAISS search speed.
func BenchmarkFAISSSearch(b *testing.B) {
	dims := 128
	n := 5000
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, n, dims)

	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 32
	builder := &Builder{config: config}
	ctx := context.Background()

	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		b.Fatal(err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		b.Fatal(err)
	}
	defer searcher.Close()

	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := searcher.Search(ctx, query, 10)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFAISSBatchSearch benchmarks batch search.
func BenchmarkFAISSBatchSearch(b *testing.B) {
	dims := 128
	n := 5000
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, n, dims)

	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 32
	builder := &Builder{config: config}
	ctx := context.Background()

	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		b.Fatal(err)
	}

	index, cleanup, err := loadFromBytes(data)
	if err != nil {
		b.Fatal(err)
	}
	defer cleanup()

	queries := randomVectors(rng, 100, dims)

	_ = ctx // suppress unused
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := BatchSearch(index, queries, 10)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func init() {
	// Register the formatter for test results.
	fmt.Sprintf("") // avoid unused import
}
