package gleann

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tevfik/gleann/modules/hnsw"
)

var registerHNSWOnce sync.Once

type testHNSWFactory struct{}

func (f *testHNSWFactory) Name() string { return "hnsw" }
func (f *testHNSWFactory) NewBuilder(cfg Config) BackendBuilder {
	factory := &hnsw.Factory{}
	return factory.NewBuilder(hnsw.Config{IndexDir: cfg.IndexDir, Backend: "hnsw"})
}
func (f *testHNSWFactory) NewSearcher(cfg Config) BackendSearcher {
	factory := &hnsw.Factory{}
	inner := factory.NewSearcher(hnsw.Config{IndexDir: cfg.IndexDir, Backend: "hnsw"})
	return &testSearcherAdapter{inner: inner}
}

type testSearcherAdapter struct {
	inner hnsw.BackendSearcher
}

func (s *testSearcherAdapter) Load(ctx context.Context, data []byte, meta IndexMeta) error {
	return s.inner.Load(ctx, data, hnsw.IndexMeta{
		Name:       meta.Name,
		Backend:    meta.Backend,
		Dimensions: meta.Dimensions,
	})
}
func (s *testSearcherAdapter) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	return s.inner.Search(ctx, query, topK)
}
func (s *testSearcherAdapter) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return s.inner.SearchWithRecompute(ctx, query, topK, func(ctx context.Context, ids []int64) ([][]float32, error) {
		return recompute(ctx, ids)
	})
}
func (s *testSearcherAdapter) Close() error {
	return s.inner.Close()
}

func initTestHNSW() {
	registerHNSWOnce.Do(func() {
		RegisterBackend(&testHNSWFactory{})
	})
}

func TestBuildLexicalOnly_SearchBM25Fallback(t *testing.T) {
	dir := t.TempDir()
	builder, embedder := newTestBuilder(t, dir, 8)

	items := []Item{
		{Text: "quick brown fox jumps over the lazy dog", Metadata: map[string]any{"source": "fox.txt"}},
		{Text: "quicksort algorithm implementation in Go", Metadata: map[string]any{"source": "sort.go"}},
		{Text: "neapolitan pizza dough fermentation recipe", Metadata: map[string]any{"source": "recipe.md"}},
	}

	indexName := "lexical_test"
	if err := builder.BuildLexicalOnly(indexName, items); err != nil {
		t.Fatalf("BuildLexicalOnly failed: %v", err)
	}

	// Verify filesystem state
	basePath := filepath.Join(dir, indexName, indexName)
	if _, err := os.Stat(basePath + ".passages.db"); err != nil {
		t.Fatalf("expected passages.db to exist: %v", err)
	}
	if _, err := os.Stat(basePath + ".index"); !os.IsNotExist(err) {
		t.Fatalf("expected .index to NOT exist for lexical-only build, but got err: %v", err)
	}

	meta, err := GetIndexMeta(dir, indexName)
	if err != nil {
		t.Fatalf("ReadIndexMeta failed: %v", err)
	}
	if !meta.LexicalOnly {
		t.Errorf("expected meta.LexicalOnly to be true, got false")
	}
	if meta.VectorReady {
		t.Errorf("expected meta.VectorReady to be false, got true")
	}
	if meta.NumPassages != 3 {
		t.Errorf("expected 3 passages in meta, got %d", meta.NumPassages)
	}

	// Create searcher and verify it loads without .index
	cfg := Config{
		IndexDir: dir,
		Backend:  "hnsw",
	}
	searcher := NewSearcher(cfg, embedder)
	ctx := context.Background()
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("searcher.Load failed on lexical-only index: %v", err)
	}

	if searcher.VectorAvailable() {
		t.Errorf("expected searcher.VectorAvailable() to be false, got true")
	}

	// Search for "pizza" via Search (should fall back to BM25)
	results, err := searcher.Search(ctx, "pizza", WithTopK(5))
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected results for query 'pizza', got 0")
	}
	if results[0].Metadata["source"] != "recipe.md" {
		t.Errorf("expected top result to be recipe.md, got %v", results[0].Metadata["source"])
	}

	// Search for "quicksort" via SearchBM25 directly
	bmResults, err := searcher.SearchBM25(ctx, "quicksort", 5)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}
	if len(bmResults) == 0 {
		t.Fatalf("expected results for query 'quicksort', got 0")
	}
	if bmResults[0].Metadata["source"] != "sort.go" {
		t.Errorf("expected top result to be sort.go, got %v", bmResults[0].Metadata["source"])
	}
}

func TestStagedBuild_TwoPhase_PassagesThenVectors(t *testing.T) {
	initTestHNSW()
	dir := t.TempDir()
	builder, embedder := newTestBuilder(t, dir, 8)
	ctx := context.Background()

	items := []Item{
		{Text: "binary search tree traversal algorithm", Metadata: map[string]any{"source": "bst.go"}},
		{Text: "chocolate chip cookie baking instructions", Metadata: map[string]any{"source": "cookie.txt"}},
	}
	indexName := "staged_test"

	// Phase 1: Build Passages (Instant Lexical)
	ids, err := builder.BuildPassages(indexName, items)
	if err != nil {
		t.Fatalf("BuildPassages failed: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}

	// Phase 1 verification: searcher can immediately search via BM25
	cfg := Config{IndexDir: dir, Backend: "hnsw"}
	searcher := NewSearcher(cfg, embedder)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("Load Phase 1 failed: %v", err)
	}
	if searcher.VectorAvailable() {
		t.Errorf("Phase 1: expected VectorAvailable == false")
	}
	res1, err := searcher.Search(ctx, "cookie", WithTopK(2))
	if err != nil {
		t.Fatalf("Phase 1 Search failed: %v", err)
	}
	if len(res1) == 0 || res1[0].Metadata["source"] != "cookie.txt" {
		t.Fatalf("Phase 1: expected cookie.txt, got %v", res1)
	}

	// Phase 2: Build Vectors
	if err := builder.BuildVectors(ctx, indexName, items, ids); err != nil {
		t.Fatalf("BuildVectors failed: %v", err)
	}

	// Verify meta updated
	meta, err := GetIndexMeta(dir, indexName)
	if err != nil {
		t.Fatalf("ReadIndexMeta failed: %v", err)
	}
	if !meta.VectorReady {
		t.Errorf("Phase 2: expected meta.VectorReady == true")
	}
	if meta.LexicalOnly {
		t.Errorf("Phase 2: expected meta.LexicalOnly == false")
	}

	// Phase 2 verification: reload searcher, now VectorAvailable is true
	searcher2 := NewSearcher(cfg, embedder)
	if err := searcher2.Load(ctx, indexName); err != nil {
		t.Fatalf("Load Phase 2 failed: %v", err)
	}
	if !searcher2.VectorAvailable() {
		t.Errorf("Phase 2: expected VectorAvailable == true")
	}

	res2, err := searcher2.Search(ctx, "binary search", WithTopK(2))
	if err != nil {
		t.Fatalf("Phase 2 Search failed: %v", err)
	}
	if len(res2) == 0 || res2[0].Metadata["source"] != "bst.go" {
		t.Fatalf("Phase 2: expected bst.go, got %v", res2)
	}
}
