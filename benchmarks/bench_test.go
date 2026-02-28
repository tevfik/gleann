package benchmarks

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/backend/hnsw"
	"github.com/tevfik/gleann/internal/bm25"
	"github.com/tevfik/gleann/internal/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ---- HNSW Benchmarks ----

func BenchmarkHNSWInsert(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dim := 128
			rng := rand.New(rand.NewSource(42))
			vecs := make([][]float32, n)
			for i := range vecs {
				vecs[i] = randomVector(rng, dim)
			}

			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				g := hnsw.NewGraph(32, 200, dim)
				for i, v := range vecs {
					g.Insert(int64(i), v)
				}
			}
		})
	}
}

func BenchmarkHNSWSearch(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dim := 128
			rng := rand.New(rand.NewSource(42))
			g := hnsw.NewGraph(32, 200, dim)
			for i := 0; i < n; i++ {
				g.Insert(int64(i), randomVector(rng, dim))
			}
			query := randomVector(rng, dim)

			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				g.Search(query, 10, 128)
			}
		})
	}
}

func BenchmarkHNSWSearchRecall(b *testing.B) {
	dim := 128
	n := 5000
	rng := rand.New(rand.NewSource(42))

	g := hnsw.NewGraph(32, 200, dim)
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vectors[i] = randomVector(rng, dim)
		g.Insert(int64(i), vectors[i])
	}

	// Precompute queries.
	numQueries := 100
	queries := make([][]float32, numQueries)
	for i := range queries {
		queries[i] = randomVector(rng, dim)
	}

	// Compute true nearest neighbors via brute force.
	trueNNs := make([][]int64, numQueries)
	for q := range queries {
		trueNNs[q] = bruteForceKNN(queries[q], vectors, 10)
	}

	for _, ef := range []int{32, 64, 128, 256} {
		b.Run(fmt.Sprintf("ef=%d", ef), func(b *testing.B) {
			totalRecall := 0.0
			for iter := 0; iter < b.N; iter++ {
				for q := range queries {
					results := g.Search(queries[q], 10, ef)
					recall := computeRecall(results, trueNNs[q])
					totalRecall += recall
				}
			}
			avgRecall := totalRecall / float64(b.N*numQueries)
			b.ReportMetric(avgRecall, "recall@10")
		})
	}
}

// ---- CSR Storage Benchmarks ----

func BenchmarkCSRConvert(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dim := 128
			rng := rand.New(rand.NewSource(42))
			g := hnsw.NewGraph(32, 200, dim)
			for i := 0; i < n; i++ {
				g.Insert(int64(i), randomVector(rng, dim))
			}

			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				hnsw.ConvertToCSR(g)
			}
		})
	}
}

func BenchmarkCSRSerialize(b *testing.B) {
	dim := 128
	n := 5000
	rng := rand.New(rand.NewSource(42))
	g := hnsw.NewGraph(32, 200, dim)
	for i := 0; i < n; i++ {
		g.Insert(int64(i), randomVector(rng, dim))
	}
	csr := hnsw.ConvertToCSR(g)
	hnsw.PruneEmbeddings(g, csr, 1.0)

	b.Run("WriteTo", func(b *testing.B) {
		for iter := 0; iter < b.N; iter++ {
			var buf bytes.Buffer
			csr.WriteTo(&buf)
		}
	})

	var buf bytes.Buffer
	csr.WriteTo(&buf)
	data := buf.Bytes()

	b.Run("ReadCSR", func(b *testing.B) {
		for iter := 0; iter < b.N; iter++ {
			hnsw.ReadCSR(bytes.NewReader(data))
		}
	})
}

// ---- Storage Reduction Report ----

func TestStorageReductionReport(t *testing.T) {
	dims := []int{64, 128, 384, 768}
	ns := []int{100, 1000, 5000}

	fmt.Println("\n=== gleann-go Storage Reduction Report ===")
	fmt.Println()
	fmt.Printf("%-8s %-8s %-12s %-12s %-12s %-8s\n",
		"Dim", "N", "Full (KB)", "CSR (KB)", "Pruned (KB)", "Savings")
	fmt.Println("-----------------------------------------------------------")

	for _, dim := range dims {
		for _, n := range ns {
			rng := rand.New(rand.NewSource(42))
			g := hnsw.NewGraph(32, 200, dim)
			for i := 0; i < n; i++ {
				g.Insert(int64(i), randomVector(rng, dim))
			}

			// Full-size graph (all embeddings stored).
			csrFull := hnsw.ConvertToCSR(g)
			hnsw.PruneEmbeddings(g, csrFull, 1.0)
			statsFull := csrFull.Stats()

			// Pruned CSR (LEANN's optimization).
			csrPruned := hnsw.ConvertToCSR(g)
			hnsw.PruneEmbeddings(g, csrPruned, 0.0) // Keep only entry point.
			statsPruned := csrPruned.Stats()

			fullKB := float64(statsFull.OriginalSizeBytes) / 1024
			csrKB := float64(statsFull.TotalSizeBytes) / 1024
			prunedKB := float64(statsPruned.TotalSizeBytes) / 1024
			savings := (1.0 - prunedKB/fullKB) * 100

			fmt.Printf("%-8d %-8d %-12.1f %-12.1f %-12.1f %-8.1f%%\n",
				dim, n, fullKB, csrKB, prunedKB, savings)
		}
	}
	fmt.Println()
}

// ---- BM25 Benchmarks ----

func BenchmarkBM25Score(b *testing.B) {
	scorer := bm25.NewScorer()
	rng := rand.New(rand.NewSource(42))
	words := []string{"go", "python", "rust", "java", "code", "function", "class", "module",
		"test", "build", "run", "compile", "debug", "error", "search", "index"}

	for i := 0; i < 1000; i++ {
		text := ""
		for j := 0; j < 20; j++ {
			text += words[rng.Intn(len(words))] + " "
		}
		scorer.AddDocument(int64(i), text)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scorer.Score("go compile function")
	}
}

func BenchmarkBM25TopK(b *testing.B) {
	scorer := bm25.NewScorer()
	rng := rand.New(rand.NewSource(42))
	words := []string{"algorithm", "database", "network", "vector", "graph", "search",
		"index", "query", "embedding", "model", "neural", "deep", "learning"}

	for i := 0; i < 5000; i++ {
		text := ""
		for j := 0; j < 30; j++ {
			text += words[rng.Intn(len(words))] + " "
		}
		scorer.AddDocument(int64(i), text)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scorer.TopK("vector graph search", 10)
	}
}

// ---- Chunking Benchmarks ----

func BenchmarkSentenceSplitter(b *testing.B) {
	text := generateLongText(10000)
	s := chunking.NewSentenceSplitter(512, 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Chunk(text)
	}
}

func BenchmarkCodeChunker(b *testing.B) {
	code := generateLongCode(5000)
	c := chunking.NewCodeChunker(512, 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Chunk(code)
	}
}

// ---- End-to-End Pipeline Benchmark ----

func BenchmarkEndToEndPipeline(b *testing.B) {
	for _, n := range []int{100, 500, 1000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dim := 64
			embedder := &mockEmbeddingComputer{dim: dim}

			items := make([]gleann.Item, n)
			rng := rand.New(rand.NewSource(42))
			words := []string{"algorithm", "database", "network", "vector", "graph",
				"search", "index", "query", "machine", "deep", "learning", "neural"}
			for i := range items {
				text := ""
				for j := 0; j < 20; j++ {
					text += words[rng.Intn(len(words))] + " "
				}
				items[i] = gleann.Item{Text: text}
			}

			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				dir := b.TempDir()
				config := gleann.DefaultConfig()
				config.IndexDir = dir
				config.Backend = "hnsw"

				builder, _ := gleann.NewBuilder(config, embedder)
				ctx := context.Background()
				builder.Build(ctx, "bench", items)

				searcher := gleann.NewSearcher(config, embedder)
				searcher.Load(ctx, "bench")
				searcher.Search(ctx, "vector search query", gleann.WithTopK(10))
				searcher.Close()
			}
		})
	}
}

// ---- Memory Usage Report ----

func TestMemoryUsageReport(t *testing.T) {
	dims := []int{128, 384, 768}
	ns := []int{1000, 5000}

	fmt.Println("\n=== gleann-go Memory Usage Report ===")
	fmt.Println()
	fmt.Printf("%-8s %-8s %-12s %-12s %-12s\n",
		"Dim", "N", "Graph (MB)", "CSR (MB)", "Savings")
	fmt.Println("-------------------------------------------")

	for _, dim := range dims {
		for _, n := range ns {
			var beforeMem, afterMem runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&beforeMem)

			rng := rand.New(rand.NewSource(42))
			g := hnsw.NewGraph(32, 200, dim)
			for i := 0; i < n; i++ {
				g.Insert(int64(i), randomVector(rng, dim))
			}

			runtime.GC()
			runtime.ReadMemStats(&afterMem)
			graphMem := float64(afterMem.Alloc-beforeMem.Alloc) / (1024 * 1024)

			// Convert to CSR with pruning.
			csr := hnsw.ConvertToCSR(g)
			hnsw.PruneEmbeddings(g, csr, 0.0)
			var buf bytes.Buffer
			csr.WriteTo(&buf)
			csrMem := float64(buf.Len()) / (1024 * 1024)

			savings := (1.0 - csrMem/graphMem) * 100
			if savings < 0 {
				savings = 0
			}

			fmt.Printf("%-8d %-8d %-12.2f %-12.2f %-12.1f%%\n",
				dim, n, graphMem, csrMem, savings)
		}
	}
	fmt.Println()
}

// ---- Latency Report ----

func TestSearchLatencyReport(t *testing.T) {
	dims := 128
	ns := []int{1000, 5000}
	efs := []int{32, 64, 128, 256}

	fmt.Println("\n=== gleann-go Search Latency Report ===")
	fmt.Println()
	fmt.Printf("%-8s %-8s %-12s %-12s\n",
		"N", "ef", "Latency", "QPS")
	fmt.Println("--------------------------------------")

	for _, n := range ns {
		rng := rand.New(rand.NewSource(42))
		g := hnsw.NewGraph(32, 200, dims)
		for i := 0; i < n; i++ {
			g.Insert(int64(i), randomVector(rng, dims))
		}

		numQueries := 1000
		queries := make([][]float32, numQueries)
		for i := range queries {
			queries[i] = randomVector(rng, dims)
		}

		for _, ef := range efs {
			start := time.Now()
			for _, q := range queries {
				g.Search(q, 10, ef)
			}
			elapsed := time.Since(start)
			avgLatency := elapsed / time.Duration(numQueries)
			qps := float64(numQueries) / elapsed.Seconds()

			fmt.Printf("%-8d %-8d %-12s %-12.0f\n",
				n, ef, avgLatency.String(), qps)
		}
	}
	fmt.Println()
}

// ---- Comparison with Brute Force ----

func TestBruteForceComparison(t *testing.T) {
	dim := 128
	n := 5000
	numQueries := 100
	rng := rand.New(rand.NewSource(42))

	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = randomVector(rng, dim)
	}

	// Build HNSW.
	g := hnsw.NewGraph(32, 200, dim)
	for i, v := range vectors {
		g.Insert(int64(i), v)
	}

	queries := make([][]float32, numQueries)
	for i := range queries {
		queries[i] = randomVector(rng, dim)
	}

	fmt.Println("\n=== HNSW vs Brute Force Comparison ===")
	fmt.Printf("Dataset: %d vectors, %d dimensions, %d queries\n\n", n, dim, numQueries)

	// Brute force.
	bruteStart := time.Now()
	for _, q := range queries {
		bruteForceKNN(q, vectors, 10)
	}
	bruteElapsed := time.Since(bruteStart)

	// HNSW.
	hnswStart := time.Now()
	totalRecall := 0.0
	for i, q := range queries {
		results := g.Search(q, 10, 128)
		trueNN := bruteForceKNN(q, vectors, 10)
		recall := computeRecall(results, trueNN)
		totalRecall += recall
		_ = i
	}
	hnswElapsed := time.Since(hnswStart)
	avgRecall := totalRecall / float64(numQueries)

	fmt.Printf("Brute Force: %s total, %s/query\n",
		bruteElapsed, bruteElapsed/time.Duration(numQueries))
	fmt.Printf("HNSW (ef=128): %s total, %s/query\n",
		hnswElapsed, hnswElapsed/time.Duration(numQueries))
	fmt.Printf("Speedup: %.1fx\n", float64(bruteElapsed)/float64(hnswElapsed))
	fmt.Printf("Recall@10: %.2f%%\n\n", avgRecall*100)
}

// ---- Helpers ----

func randomVector(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var norm float32
	for i := range v {
		v[i] = rng.Float32()*2 - 1
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

type scored struct {
	id   int64
	dist float32
}

func sortScored(s []scored) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j].dist < s[i].dist {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func computeRecall(results []hnsw.Candidate, trueNN []int64) float64 {
	trueSet := make(map[int64]bool, len(trueNN))
	for _, id := range trueNN {
		trueSet[id] = true
	}
	hits := 0
	for _, r := range results {
		if trueSet[r.ID] {
			hits++
		}
	}
	return float64(hits) / float64(len(trueNN))
}

func bruteForceKNN(query []float32, vectors [][]float32, k int) []int64 {
	scores := make([]scored, len(vectors))
	for i, v := range vectors {
		scores[i] = scored{int64(i), l2Dist(query, v)}
	}
	sortScored(scores)
	if len(scores) > k {
		scores = scores[:k]
	}
	ids := make([]int64, len(scores))
	for i, s := range scores {
		ids[i] = s.id
	}
	return ids
}

func generateLongText(words int) string {
	rng := rand.New(rand.NewSource(42))
	vocab := []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
		"machine", "learning", "neural", "network", "data", "science", "algorithm",
		"database", "query", "index", "search", "vector", "graph", "node", "edge"}
	text := ""
	for i := 0; i < words; i++ {
		text += vocab[rng.Intn(len(vocab))]
		if rng.Float32() < 0.1 {
			text += ". "
		} else if rng.Float32() < 0.05 {
			text += "\n\n"
		} else {
			text += " "
		}
	}
	return text
}

func generateLongCode(lines int) string {
	rng := rand.New(rand.NewSource(42))
	code := "package main\n\nimport \"fmt\"\n\n"
	for i := 0; i < lines; i++ {
		indent := ""
		if rng.Float32() < 0.3 {
			indent = "\t"
		}
		switch rng.Intn(5) {
		case 0:
			code += fmt.Sprintf("func fn%d() {\n", i)
		case 1:
			code += indent + fmt.Sprintf("x%d := %d\n", i, rng.Intn(100))
		case 2:
			code += indent + fmt.Sprintf("fmt.Println(x%d)\n", rng.Intn(i+1))
		case 3:
			code += "}\n\n"
		case 4:
			code += indent + "// comment\n"
		}
	}
	return code
}

type mockEmbeddingComputer struct {
	dim int
}

func (m *mockEmbeddingComputer) Compute(_ context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		r := rand.New(rand.NewSource(int64(hashStr(texts[i]))))
		emb := make([]float32, m.dim)
		var norm float32
		for j := range emb {
			emb[j] = r.Float32()*2 - 1
			norm += emb[j] * emb[j]
		}
		norm = float32(math.Sqrt(float64(norm)))
		for j := range emb {
			emb[j] /= norm
		}
		embeddings[i] = emb
	}
	return embeddings, nil
}

func (m *mockEmbeddingComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	results, err := m.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

func (m *mockEmbeddingComputer) Dimensions() int  { return m.dim }
func (m *mockEmbeddingComputer) ModelName() string { return "mock" }

func hashStr(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}
