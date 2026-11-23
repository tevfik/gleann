//go:build !(cgo && faiss)

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

func TestDefaultBackend_HNSW(t *testing.T) {
	if gleann.IsFaissSupported() {
		t.Skip("skipping HNSW default check: binary built with FAISS support (default is faiss)")
	}

	// 1. DefaultConfig().Backend must be "hnsw"
	cfg := gleann.DefaultConfig()
	if cfg.Backend != "hnsw" {
		t.Errorf("DefaultConfig().Backend = %q, want %q", cfg.Backend, "hnsw")
	}

	// 2. NewBuilder with zero Config (only IndexDir set) builds an hnsw vector index
	dir := t.TempDir()
	emb := &stubEmbedder{dim: 32}
	builder, err := gleann.NewBuilder(gleann.Config{IndexDir: dir}, emb)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	indexName := "zero-config-test"
	items := make([]gleann.Item, 20)
	for i := range items {
		items[i] = gleann.Item{Text: fmt.Sprintf("default backend test passage number %d", i)}
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
	if meta.Backend != "hnsw" {
		t.Errorf("built index meta.Backend = %q, want %q", meta.Backend, "hnsw")
	}
	if meta.LexicalOnly || !meta.VectorReady {
		t.Errorf("expected VectorReady=true LexicalOnly=false, got VectorReady=%v LexicalOnly=%v", meta.VectorReady, meta.LexicalOnly)
	}

	searcher := gleann.NewSearcher(gleann.Config{IndexDir: dir}, emb)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("searcher.Load: %v", err)
	}
	defer searcher.Close()

	if searcher.Meta().Backend != "hnsw" {
		t.Errorf("searcher.Meta().Backend = %q, want %q", searcher.Meta().Backend, "hnsw")
	}

	if searcher.Backend() == nil {
		t.Fatal("searcher vector backend is nil")
	}
	backendType := fmt.Sprintf("%T", searcher.Backend())
	if !strings.Contains(strings.ToLower(backendType), "searcheradapter") {
		t.Errorf("expected HNSW vector backend adapter, got %s", backendType)
	}
	if strings.Contains(backendType, "diskann") {
		t.Errorf("expected HNSW vector backend, got DiskANN: %s", backendType)
	}

	results, err := searcher.Search(ctx, "default backend")
	if err != nil {
		t.Fatalf("searcher.Search: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("expected search results from default hnsw index, got 0")
	}
}
