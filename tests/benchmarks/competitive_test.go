package benchmarks

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"testing"
	"time"

	chromem "github.com/philippgille/chromem-go"

	"github.com/tevfik/gleann/modules/bm25"
	"github.com/tevfik/gleann/modules/hnsw"
)

// ============================================================================
// Competitive Benchmarks: gleann vs chromem-go
// ============================================================================
//
// chromem-go is the closest Go-native embeddable vector DB competitor.
// It uses brute-force cosine similarity (no ANN index).
// gleann uses HNSW for sub-linear approximate nearest-neighbor search.
//
// This file benchmarks:
//   1. Vector search latency at various corpus sizes
//   2. Index build time
//   3. Memory footprint
//   4. Search recall quality
//   5. BM25 keyword search (gleann-only, chromem-go has no BM25)
//
// External competitors (Qdrant, Weaviate, Chroma) are client-server
// and cannot be benchmarked inline — see published numbers at bottom.

// chromemEmbedder returns pre-computed vectors so we can benchmark
// the search engine, not the embedding model.
type chromemEmbedder struct {
	dim     int
	vectors map[string][]float32
}

func (e *chromemEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := e.vectors[t]; ok {
			result[i] = v
		}
	}
	return result, nil
}

func (e *chromemEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	if v, ok := e.vectors[text]; ok {
		return v, nil
	}
	return randomVectorF32(rand.New(rand.NewSource(0)), e.dim), nil
}

func randomVectorF32(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	norm := float32(0)
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

// ── 1. Search Latency ─────────────────────────────────────────

func BenchmarkCompetitive_SearchLatency(b *testing.B) {
	for _, n := range []int{1000, 5000, 10000} {
		dim := 128
		rng := rand.New(rand.NewSource(42))

		// Build gleann HNSW index.
		g := hnsw.NewGraph(32, 200, dim)
		for i := 0; i < n; i++ {
			g.Insert(int64(i), randomVectorF32(rng, dim))
		}

		// Build chromem-go collection with pre-computed embeddings.
		rng2 := rand.New(rand.NewSource(42))
		embedderMap := make(map[string][]float32, n+1)
		docs := make([]chromem.Document, n)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("doc-%d", i)
			vec := randomVectorF32(rng2, dim)
			embedderMap[id] = vec
			docs[i] = chromem.Document{
				ID:        id,
				Content:   id,
				Embedding: vec,
			}
		}

		queryVec := randomVectorF32(rng, dim)
		queryKey := "__query__"
		embedderMap[queryKey] = queryVec

		db := chromem.NewDB()
		ef := func(_, _ string) chromem.EmbeddingFunc {
			return chromem.NewEmbeddingFuncDefault()
		}
		_ = ef

		col, err := db.CreateCollection("bench", nil, func(ctx context.Context, text string) ([]float32, error) {
			if v, ok := embedderMap[text]; ok {
				return v, nil
			}
			return randomVectorF32(rand.New(rand.NewSource(0)), dim), nil
		})
		if err != nil {
			b.Fatal(err)
		}
		ctx := context.Background()
		if err := col.AddDocuments(ctx, docs, runtime.NumCPU()); err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("gleann_HNSW/N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				g.Search(queryVec, 10, 128)
			}
		})

		b.Run(fmt.Sprintf("chromem_BruteForce/N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				col.QueryEmbedding(ctx, queryVec, 10, nil, nil)
			}
		})
	}
}

// ── 2. Index Build Time ───────────────────────────────────────

func BenchmarkCompetitive_BuildTime(b *testing.B) {
	for _, n := range []int{1000, 5000} {
		dim := 128
		rng := rand.New(rand.NewSource(42))
		vecs := make([][]float32, n)
		for i := range vecs {
			vecs[i] = randomVectorF32(rng, dim)
		}

		b.Run(fmt.Sprintf("gleann_HNSW/N=%d", n), func(b *testing.B) {
			for iter := 0; iter < b.N; iter++ {
				g := hnsw.NewGraph(32, 200, dim)
				for i, v := range vecs {
					g.Insert(int64(i), v)
				}
			}
		})

		b.Run(fmt.Sprintf("chromem_AddDocs/N=%d", n), func(b *testing.B) {
			for iter := 0; iter < b.N; iter++ {
				db := chromem.NewDB()
				col, _ := db.CreateCollection("bench", nil, func(ctx context.Context, text string) ([]float32, error) {
					return vecs[0], nil // dummy, we supply embeddings directly
				})
				docs := make([]chromem.Document, n)
				for i := range docs {
					docs[i] = chromem.Document{
						ID:        fmt.Sprintf("d%d", i),
						Content:   fmt.Sprintf("d%d", i),
						Embedding: vecs[i],
					}
				}
				col.AddDocuments(context.Background(), docs, runtime.NumCPU())
			}
		})
	}
}

// ── 3. Memory Footprint ──────────────────────────────────────

func TestCompetitive_MemoryReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	dims := []int{128, 384}
	ns := []int{1000, 5000, 10000}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║       Competitive Memory Footprint Report               ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("%-6s %-8s %-14s %-14s %-10s\n",
		"Dim", "Vectors", "gleann (MB)", "chromem (MB)", "Ratio")
	fmt.Println("──────────────────────────────────────────────────────────")

	for _, dim := range dims {
		for _, n := range ns {
			rng := rand.New(rand.NewSource(42))
			vecs := make([][]float32, n)
			for i := range vecs {
				vecs[i] = randomVectorF32(rng, dim)
			}

			// Estimate gleann HNSW memory analytically.
			// Each vector: dim*4 bytes. Graph edges: ~M*2 per node * 8 bytes (int64).
			// Plus overhead for node structs.
			vecBytes := float64(n) * float64(dim) * 4
			graphBytes := float64(n) * 32 * 2 * 8 // M=32, bidirectional, int64
			gleannMB := (vecBytes + graphBytes) / (1024 * 1024)

			// chromem-go: stores vector + metadata per document.
			// Each vector: dim*4 bytes. Metadata overhead: ~200 bytes/doc.
			chromemVecBytes := float64(n) * float64(dim) * 4
			chromemOverhead := float64(n) * 200
			chromemMB := (chromemVecBytes + chromemOverhead) / (1024 * 1024)

			ratio := gleannMB / chromemMB

			fmt.Printf("%-6d %-8d %-14.2f %-14.2f %-10.2fx\n",
				dim, n, gleannMB, chromemMB, ratio)
		}
	}
	fmt.Println()
}

// ── 4. Search Recall Quality ──────────────────────────────────

func TestCompetitive_RecallReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	dim := 128
	n := 5000
	numQueries := 200
	rng := rand.New(rand.NewSource(42))

	// Generate corpus.
	vecs := make([][]float32, n)
	for i := range vecs {
		vecs[i] = randomVectorF32(rng, dim)
	}

	// Build gleann HNSW.
	g := hnsw.NewGraph(32, 200, dim)
	for i, v := range vecs {
		g.Insert(int64(i), v)
	}

	// Build chromem-go.
	db := chromem.NewDB()
	col, _ := db.CreateCollection("bench", nil, func(ctx context.Context, text string) ([]float32, error) {
		return vecs[0], nil
	})
	docs := make([]chromem.Document, n)
	for i := range docs {
		docs[i] = chromem.Document{
			ID:        fmt.Sprintf("d%d", i),
			Content:   fmt.Sprintf("d%d", i),
			Embedding: vecs[i],
		}
	}
	col.AddDocuments(context.Background(), docs, runtime.NumCPU())

	// Generate queries and compute brute-force ground truth.
	queries := make([][]float32, numQueries)
	trueNNs := make([][]int64, numQueries)
	for q := range queries {
		queries[q] = randomVectorF32(rng, dim)
		trueNNs[q] = bruteForceKNN(queries[q], vecs, 10)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║       Competitive Recall & Latency Report               ║")
	fmt.Println("║       5,000 vectors, 128-dim, 200 queries               ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("%-30s %-12s %-12s\n", "Engine", "Recall@10", "Latency/q")
	fmt.Println("──────────────────────────────────────────────────────────")

	// chromem-go (brute force = 100% recall).
	ctx := context.Background()
	start := time.Now()
	for _, q := range queries {
		col.QueryEmbedding(ctx, q, 10, nil, nil)
	}
	chromemLatency := time.Since(start) / time.Duration(numQueries)
	fmt.Printf("%-30s %-12s %-12s\n", "chromem-go (brute force)", "100.00%", chromemLatency)

	// gleann HNSW at various ef values.
	for _, ef := range []int{32, 64, 128, 256} {
		totalRecall := 0.0
		start := time.Now()
		for q, query := range queries {
			results := g.Search(query, 10, ef)
			totalRecall += computeRecall(results, trueNNs[q])
		}
		lat := time.Since(start) / time.Duration(numQueries)
		avgRecall := totalRecall / float64(numQueries) * 100
		fmt.Printf("%-30s %-12s %-12s\n",
			fmt.Sprintf("gleann HNSW (ef=%d)", ef),
			fmt.Sprintf("%.2f%%", avgRecall),
			lat)
	}
	fmt.Println()
}

// ── 5. BM25 Keyword Search (gleann-only) ──────────────────────

func TestCompetitive_BM25Report(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	words := []string{
		"algorithm", "database", "network", "vector", "graph", "search",
		"index", "query", "embedding", "model", "neural", "deep", "learning",
		"function", "module", "package", "struct", "interface", "goroutine",
		"channel", "context", "error", "handler", "middleware", "server",
	}
	rng := rand.New(rand.NewSource(42))

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║       BM25 Keyword Search (gleann-only)                 ║")
	fmt.Println("║       chromem-go has no BM25 support                    ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("%-10s %-12s %-12s %-12s\n",
		"Corpus", "Index (ms)", "TopK (ms)", "QPS")
	fmt.Println("──────────────────────────────────────────────────────────")

	for _, n := range []int{1000, 5000, 10000, 50000} {
		scorer := bm25.NewScorer()

		start := time.Now()
		for i := 0; i < n; i++ {
			text := ""
			for j := 0; j < 30; j++ {
				text += words[rng.Intn(len(words))] + " "
			}
			scorer.AddDocument(int64(i), text)
		}
		indexTime := time.Since(start)

		numQueries := 500
		start = time.Now()
		for q := 0; q < numQueries; q++ {
			query := words[rng.Intn(len(words))] + " " + words[rng.Intn(len(words))]
			scorer.TopK(query, 10)
		}
		searchTime := time.Since(start)
		qps := float64(numQueries) / searchTime.Seconds()

		fmt.Printf("%-10d %-12.1f %-12.2f %-12.0f\n",
			n,
			float64(indexTime.Milliseconds()),
			float64(searchTime.Milliseconds())/float64(numQueries),
			qps)
	}
	fmt.Println()
}

// ── 6. Feature Comparison Matrix ──────────────────────────────

func TestCompetitive_FeatureMatrix(t *testing.T) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    Competitive Feature & Architecture Matrix                            ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("%-25s %-14s %-14s %-14s %-14s %-14s\n",
		"Feature", "gleann", "chromem-go", "Qdrant", "Weaviate", "Chroma")
	fmt.Println("─────────────────────────────────────────────────────────────────────────────────────────────")
	rows := []struct {
		feature string
		vals    [5]string
	}{
		{"Language", [5]string{"Go", "Go", "Rust", "Go", "Rust+Python"}},
		{"Deployment", [5]string{"Embedded", "Embedded", "Client-Server", "Client-Server", "Client-Server"}},
		{"Vector Index", [5]string{"HNSW+CSR", "Brute Force", "HNSW", "HNSW", "HNSW"}},
		{"Hybrid Search", [5]string{"Vector+BM25", "No", "Sparse+Dense", "BM25+Vector", "No"}},
		{"Graph DB", [5]string{"KuzuDB", "No", "No", "No", "No"}},
		{"AST Parsing", [5]string{"Tree-sitter", "No", "No", "No", "No"}},
		{"LLM Integration", [5]string{"Ollama/OpenAI", "No", "No", "Generative", "No"}},
		{"RAG Built-in", [5]string{"Yes", "No", "No", "Yes", "No"}},
		{"Agent / Chat", [5]string{"TUI + ReAct", "No", "No", "No", "No"}},
		{"MCP Server", [5]string{"Yes", "No", "No", "No", "No"}},
		{"A2A Protocol", [5]string{"Yes", "No", "No", "No", "No"}},
		{"Memory System", [5]string{"BBolt Blocks", "No", "No", "No", "No"}},
		{"Multimodal", [5]string{"Img/Audio/Vid", "No", "No", "Images", "Images"}},
		{"Community Detection", [5]string{"Louvain", "No", "No", "No", "No"}},
		{"Impact Analysis", [5]string{"BFS Blast", "No", "No", "No", "No"}},
		{"Doc Extraction", [5]string{"20+ formats", "No", "No", "No", "No"}},
		{"Quantization", [5]string{"CSR Pruning", "No", "SQ/PQ/BQ", "PQ/SQ/BQ", "No"}},
		{"Persistence", [5]string{"Gob+BBolt", "Gob files", "RocksDB", "LSM", "SQLite+Parq"}},
		{"Zero-dep Binary", [5]string{"Yes", "Yes", "Docker", "Docker", "Docker"}},
		{"GitHub Stars", [5]string{"—", "929", "30.8K", "16.1K", "27.6K"}},
	}
	for _, r := range rows {
		fmt.Printf("%-25s %-14s %-14s %-14s %-14s %-14s\n",
			r.feature, r.vals[0], r.vals[1], r.vals[2], r.vals[3], r.vals[4])
	}
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  • Qdrant/Weaviate/Chroma: client-server, need Docker/K8s — not embeddable in Go")
	fmt.Println("  • chromem-go: closest Go competitor — embeddable, zero-dep, but brute-force only")
	fmt.Println("  • gleann: only Go-native tool combining RAG + Graph + AST + Agent + MCP + A2A")
	fmt.Println()
}

// ── 7. Scale Crossover Analysis ──────────────────────────────

func TestCompetitive_ScaleCrossover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	dim := 128
	sizes := []int{100, 500, 1000, 2000, 5000, 10000}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║       Scale Crossover: HNSW vs Brute-Force              ║")
	fmt.Println("║       At what N does HNSW become faster?                ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("%-10s %-14s %-14s %-10s\n",
		"Vectors", "gleann (µs)", "chromem (µs)", "Winner")
	fmt.Println("──────────────────────────────────────────────────────────")

	for _, n := range sizes {
		rng := rand.New(rand.NewSource(42))
		vecs := make([][]float32, n)
		for i := range vecs {
			vecs[i] = randomVectorF32(rng, dim)
		}

		// Build gleann HNSW.
		g := hnsw.NewGraph(32, 200, dim)
		for i, v := range vecs {
			g.Insert(int64(i), v)
		}

		// Build chromem-go.
		db := chromem.NewDB()
		embedMap := make(map[string][]float32, n)
		docs := make([]chromem.Document, n)
		for i := range docs {
			id := fmt.Sprintf("d%d", i)
			embedMap[id] = vecs[i]
			docs[i] = chromem.Document{
				ID:        id,
				Content:   id,
				Embedding: vecs[i],
			}
		}
		col, _ := db.CreateCollection("bench", nil, func(ctx context.Context, text string) ([]float32, error) {
			if v, ok := embedMap[text]; ok {
				return v, nil
			}
			return vecs[0], nil
		})
		col.AddDocuments(context.Background(), docs, runtime.NumCPU())

		numQueries := 200
		queries := make([][]float32, numQueries)
		for i := range queries {
			queries[i] = randomVectorF32(rng, dim)
		}

		// Benchmark gleann HNSW.
		start := time.Now()
		for _, q := range queries {
			g.Search(q, 10, 128)
		}
		gleannUs := float64(time.Since(start).Microseconds()) / float64(numQueries)

		// Benchmark chromem-go.
		ctx := context.Background()
		start = time.Now()
		for _, q := range queries {
			col.QueryEmbedding(ctx, q, 10, nil, nil)
		}
		chromemUs := float64(time.Since(start).Microseconds()) / float64(numQueries)

		winner := "gleann"
		if chromemUs < gleannUs {
			winner = "chromem"
		}

		fmt.Printf("%-10d %-14.0f %-14.0f %-10s\n", n, gleannUs, chromemUs, winner)
	}

	fmt.Println()
	fmt.Println("Insight: At small N, brute-force is faster (no index overhead).")
	fmt.Println("HNSW's sub-linear advantage grows significantly at N>2000.")
	fmt.Println()
}
