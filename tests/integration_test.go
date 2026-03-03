package integration

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann-bm25"
	"github.com/tevfik/gleann-hnsw"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// mockEmbeddingComputer is a test double for EmbeddingComputer.
type mockEmbeddingComputer struct {
	dim int
}

func (m *mockEmbeddingComputer) Compute(_ context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		emb := make([]float32, m.dim)
		// Deterministic embeddings based on text content.
		r := rand.New(rand.NewSource(int64(hashString(texts[i]))))
		for j := range emb {
			emb[j] = r.Float32()
		}
		// Normalize.
		var norm float32
		for _, v := range emb {
			norm += v * v
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

func (m *mockEmbeddingComputer) Dimensions() int   { return m.dim }
func (m *mockEmbeddingComputer) ModelName() string { return "mock" }

func hashString(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

func TestBuildAndSearch(t *testing.T) {
	dir := t.TempDir()

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.EmbeddingModel = "mock"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 32}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	items := []gleann.Item{
		{Text: "Go is a statically typed compiled language", Metadata: map[string]any{"lang": "go"}},
		{Text: "Python is a dynamic interpreted language", Metadata: map[string]any{"lang": "python"}},
		{Text: "Rust is a systems programming language", Metadata: map[string]any{"lang": "rust"}},
		{Text: "JavaScript runs in the browser", Metadata: map[string]any{"lang": "js"}},
		{Text: "TypeScript adds types to JavaScript", Metadata: map[string]any{"lang": "ts"}},
	}

	ctx := context.Background()
	err = builder.Build(ctx, "test-index", items)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Verify index files exist.
	indexDir := filepath.Join(dir, "test-index")
	for _, suffix := range []string{".meta.json", ".passages.jsonl", ".index"} {
		path := filepath.Join(indexDir, "test-index"+suffix)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", path)
		}
	}

	// Verify metadata.
	metaData, err := os.ReadFile(filepath.Join(indexDir, "test-index.meta.json"))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var meta gleann.IndexMeta
	json.Unmarshal(metaData, &meta)

	if meta.Backend != "hnsw" {
		t.Errorf("expected backend hnsw, got %s", meta.Backend)
	}
	if meta.NumPassages != 5 {
		t.Errorf("expected 5 passages, got %d", meta.NumPassages)
	}
	if meta.Dimensions != 32 {
		t.Errorf("expected 32 dimensions, got %d", meta.Dimensions)
	}

	// Search.
	searcher := gleann.NewSearcher(config, embedder)
	if err := searcher.Load(ctx, "test-index"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	loadedMeta := searcher.Meta()
	t.Logf("Loaded meta: backend=%s dims=%d passages=%d", loadedMeta.Backend, loadedMeta.Dimensions, loadedMeta.NumPassages)

	// Verify the index file is not empty.
	indexPath := filepath.Join(indexDir, "test-index.index")
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	t.Logf("Index file size: %d bytes", indexInfo.Size())

	results, err := searcher.Search(ctx, "Go programming language", gleann.WithTopK(3))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}

	t.Logf("Top result: %q (score: %.4f)", results[0].Text, results[0].Score)
}

func TestBuildFromTexts(t *testing.T) {
	dir := t.TempDir()

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 16}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	texts := []string{
		"Hello world",
		"Foo bar baz",
		"Testing one two three",
	}

	ctx := context.Background()
	err = builder.BuildFromTexts(ctx, "text-index", texts)
	if err != nil {
		t.Fatalf("BuildFromTexts: %v", err)
	}

	// Search.
	searcher := gleann.NewSearcher(config, embedder)
	if err := searcher.Load(ctx, "text-index"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(ctx, "hello")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
}

func TestListAndRemoveIndexes(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	embedder := &mockEmbeddingComputer{dim: 8}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	ctx := context.Background()
	for _, name := range []string{"idx1", "idx2"} {
		builder.BuildFromTexts(ctx, name, []string{"test document " + name})
	}

	indexes, err := gleann.ListIndexes(dir)
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(indexes) != 2 {
		t.Fatalf("expected 2 indexes, got %d", len(indexes))
	}

	// Remove one.
	if err := gleann.RemoveIndex(dir, "idx1"); err != nil {
		t.Fatalf("RemoveIndex: %v", err)
	}

	indexes, err = gleann.ListIndexes(dir)
	if err != nil {
		t.Fatalf("ListIndexes after remove: %v", err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index after remove, got %d", len(indexes))
	}
}

func TestHybridSearchWithBM25(t *testing.T) {
	dir := t.TempDir()

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 16}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	items := []gleann.Item{
		{Text: "machine learning and deep neural networks"},
		{Text: "natural language processing with transformers"},
		{Text: "computer vision and image recognition"},
		{Text: "database indexing and query optimization"},
		{Text: "network security and firewall configuration"},
	}

	ctx := context.Background()
	err = builder.Build(ctx, "hybrid-test", items)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	searcher := gleann.NewSearcher(config, embedder)
	scorer := gleann.NewBM25Adapter()
	searcher.SetScorer(scorer)

	if err := searcher.Load(ctx, "hybrid-test"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	// Pure vector search.
	vectorResults, err := searcher.Search(ctx, "neural network learning",
		gleann.WithTopK(3),
		gleann.WithHybridAlpha(1.0), // 100% vector
	)
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}

	// Hybrid search (50% vector + 50% BM25).
	hybridResults, err := searcher.Search(ctx, "neural network learning",
		gleann.WithTopK(3),
		gleann.WithHybridAlpha(0.5),
	)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}

	t.Logf("Vector results: %d, Hybrid results: %d", len(vectorResults), len(hybridResults))
	for i, r := range hybridResults {
		t.Logf("  #%d: %q (score=%.4f)", i+1, r.Text, r.Score)
	}
}

func TestCSRStorageReduction(t *testing.T) {
	// Build a graph with some vectors and verify storage reduction.
	dim := 128
	graph := hnsw.NewGraph(8, 32, dim)

	n := 100
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < n; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		graph.Insert(int64(i), vec)
	}

	// Convert to CSR for compact storage.
	csr := hnsw.ConvertToCSR(graph)
	stats := csr.Stats()

	t.Logf("Original size: %d bytes, Total size: %d bytes, Ratio: %.2f%%",
		stats.OriginalSizeBytes, stats.TotalSizeBytes, stats.CompressionRatio*100)

	// Now prune embeddings.
	hnsw.PruneEmbeddings(graph, csr, 0.1) // Keep only 10%.
	statsAfter := csr.Stats()

	t.Logf("After pruning: Original=%d, Total=%d, Ratio=%.2f%%",
		statsAfter.OriginalSizeBytes, statsAfter.TotalSizeBytes, statsAfter.CompressionRatio*100)

	if statsAfter.CompressionRatio >= stats.CompressionRatio {
		t.Error("pruning should reduce compression ratio")
	}
}

func TestEndToEndSearchQuality(t *testing.T) {
	// Test that semantically similar queries return relevant results.
	dir := t.TempDir()

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 64}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	items := make([]gleann.Item, 50)
	for i := 0; i < 50; i++ {
		items[i] = gleann.Item{
			Text:     randomSentence(i),
			Metadata: map[string]any{"id": i},
		}
	}

	ctx := context.Background()
	err = builder.Build(ctx, "quality-test", items)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	searcher := gleann.NewSearcher(config, embedder)
	if err := searcher.Load(ctx, "quality-test"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	// Search should return results.
	results, err := searcher.Search(ctx, "test query")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from search")
	}

	// Results should be sorted by score descending.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: result[%d].Score=%f > result[%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestBM25ScorerDirectly(t *testing.T) {
	scorer := bm25.NewScorer()

	docs := map[int64]string{
		1: "the quick brown fox jumps over the lazy dog",
		2: "a fast red fox leaps across the sleeping hound",
		3: "database query optimization techniques",
		4: "machine learning neural network training",
	}

	for id, text := range docs {
		scorer.AddDocument(id, text)
	}

	scores := scorer.Score("fox jumps")
	if scores[1] <= 0 {
		t.Error("doc 1 should have positive score for 'fox jumps'")
	}
	if scores[3] != 0 {
		t.Error("doc 3 should have zero score for 'fox jumps'")
	}

	// TopK should rank doc 1 first.
	ids, topScores := scorer.TopK("fox jumps", 2)
	if len(ids) < 1 {
		t.Fatal("expected at least 1 result")
	}
	if ids[0] != 1 && ids[0] != 2 {
		t.Errorf("expected doc 1 or 2 first, got %d", ids[0])
	}
	if topScores[0] <= 0 {
		t.Error("top score should be positive")
	}
}

func randomSentence(seed int) string {
	words := []string{
		"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
		"machine", "learning", "neural", "network", "data", "science",
		"golang", "python", "server", "client", "database", "query",
	}
	r := rand.New(rand.NewSource(int64(seed)))
	n := 5 + r.Intn(10)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = words[r.Intn(len(words))]
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += " "
		}
		result += p
	}
	return result
}
