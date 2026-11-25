package embedding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
)

// A catalogue id or file name of an installed embedding model maps to its file, so embedding_model
// "embeddinggemma-2" works with the eif provider (the file is embeddinggemma-2-Q8_0.gguf).
func TestCatalogEmbeddingPath(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	e, ok := modelcatalog.Find("embeddinggemma-2")
	if !ok {
		t.Fatal("embeddinggemma-2 not in the catalogue")
	}
	if _, ok := catalogEmbeddingPath("embeddinggemma-2"); ok {
		t.Fatal("not installed yet, must not resolve")
	}
	if err := os.MkdirAll(filepath.Dir(e.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Path(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"embeddinggemma-2", e.File} {
		if p, ok := catalogEmbeddingPath(name); !ok || p != e.Path() {
			t.Errorf("%s: %q %v", name, p, ok)
		}
	}
	if _, ok := catalogEmbeddingPath("bge-reranker-v2-m3"); ok {
		t.Error("a reranker is not an embedding model")
	}
}
