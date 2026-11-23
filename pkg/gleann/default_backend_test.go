package gleann_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

type stubEmbedder struct {
	dim int
}

func (e *stubEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		vec := make([]float32, e.dim)
		for j := range vec {
			vec[j] = float32(len(t)+j+1) * 0.01
		}
		out[i] = vec
	}
	return out, nil
}

func (e *stubEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *stubEmbedder) Dimensions() int   { return e.dim }
func (e *stubEmbedder) ModelName() string { return "stub-embedder" }

func TestExistingDiskANNIndexKeepsBackend(t *testing.T) {
	dir := t.TempDir()
	emb := &stubEmbedder{dim: 32}

	// Build an index explicitly choosing "diskann"
	builder, err := gleann.NewBuilder(gleann.Config{
		IndexDir: dir,
		Backend:  "diskann",
	}, emb)
	if err != nil {
		t.Fatalf("NewBuilder with diskann: %v", err)
	}

	indexName := "diskann-index"
	items := make([]gleann.Item, 20)
	for i := range items {
		items[i] = gleann.Item{Text: fmt.Sprintf("passage indexed with diskann backend number %d", i)}
	}
	ids, err := builder.BuildPassages(indexName, items)
	if err != nil {
		t.Fatalf("BuildPassages: %v", err)
	}

	ctx := context.Background()
	if err := builder.BuildVectors(ctx, indexName, items, ids); err != nil {
		t.Fatalf("BuildVectors: %v", err)
	}

	indexPath := filepath.Join(dir, indexName, indexName+".index")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected .index file on disk at %s: %v", indexPath, err)
	}

	meta, err := gleann.GetIndexMeta(dir, indexName)
	if err != nil {
		t.Fatalf("GetIndexMeta: %v", err)
	}
	if meta.Backend != "diskann" {
		t.Fatalf("built index meta.Backend = %q, want %q", meta.Backend, "diskann")
	}
	if meta.LexicalOnly || !meta.VectorReady {
		t.Errorf("expected VectorReady=true LexicalOnly=false, got VectorReady=%v LexicalOnly=%v", meta.VectorReady, meta.LexicalOnly)
	}

	// Searcher with default config (Backend unset) must load and retain diskann backend
	searcher := gleann.NewSearcher(gleann.Config{IndexDir: dir}, emb)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("searcher.Load: %v", err)
	}
	defer searcher.Close()

	if searcher.Meta().Backend != "diskann" {
		t.Errorf("searcher.Meta().Backend = %q, want %q", searcher.Meta().Backend, "diskann")
	}

	if searcher.Backend() == nil {
		t.Fatal("searcher vector backend is nil")
	}
	backendType := fmt.Sprintf("%T", searcher.Backend())
	if !strings.Contains(backendType, "diskann") {
		t.Errorf("expected DiskANN vector backend, got %s", backendType)
	}

	results, err := searcher.Search(ctx, "diskann")
	if err != nil {
		t.Fatalf("searcher.Search: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("expected search results from diskann index, got 0")
	}
}

func TestReviewMmapLegacyDiskANNKey(t *testing.T) {
	dir := t.TempDir()
	emb := &stubEmbedder{dim: 32}

	// Build an index explicitly choosing "diskann"
	builder, err := gleann.NewBuilder(gleann.Config{
		IndexDir: dir,
		Backend:  "diskann",
	}, emb)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	indexName := "legacy-mmap-test"
	items := make([]gleann.Item, 20)
	for i := range items {
		items[i] = gleann.Item{Text: fmt.Sprintf("test passage number %d for diskann mmap", i)}
	}
	ids, err := builder.BuildPassages(indexName, items)
	if err != nil {
		t.Fatalf("BuildPassages: %v", err)
	}
	ctx := context.Background()
	if err := builder.BuildVectors(ctx, indexName, items, ids); err != nil {
		t.Fatalf("BuildVectors: %v", err)
	}

	// Open index with empty Backend and DiskANNConfig.UseMmap = false
	cfg := gleann.Config{
		IndexDir:      dir,
		DiskANNConfig: gleann.DiskANNConfig{R: 64, UseMmap: false},
	}
	searcher := gleann.NewSearcher(cfg, emb)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("searcher.Load: %v", err)
	}
	defer searcher.Close()

	backendType := fmt.Sprintf("%T", searcher.Backend())
	if strings.Contains(backendType, "diskannMmapSearcherAdapter") {
		t.Errorf("effective backend=%q MmapEnabled=%v (diskann key=%v, hnsw key=%v)",
			"hnsw", true, cfg.DiskANNConfig.UseMmap, true)
		t.Fatalf("DiskANN index opened with diskann_config.use_mmap=false and empty backend uses the MMAP searcher (%s); before the default flip it used the RAM searcher", backendType)
	}
}
