package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
)

func TestCatalogOptionsOfferWhatIsMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// One embedding model is on disk with its full size, another is listed locally.
	e := modelcatalog.OfKind(modelcatalog.Embedding)[0]
	_ = os.MkdirAll(modelcatalog.Dir(), 0o755)
	f, _ := os.Create(e.Path())
	_ = f.Truncate(e.Bytes)
	f.Close()
	other := modelcatalog.OfKind(modelcatalog.Embedding)[1]

	opts := catalogOptions(modelcatalog.Embedding, false, []ModelInfo{{Name: other.File, Tag: other.File}})
	for _, o := range opts {
		if strings.HasSuffix(o.Tag, e.File) || strings.HasSuffix(o.Tag, other.File) {
			t.Errorf("offered a model that is already there: %+v", o)
		}
		if !strings.HasPrefix(o.Name, "(Download) ") || o.Size == "" {
			t.Errorf("option: %+v", o)
		}
	}
	if len(opts) != len(modelcatalog.OfKind(modelcatalog.Embedding))-2 {
		t.Errorf("%d options", len(opts))
	}
	for _, o := range catalogOptions(modelcatalog.LLM, true, nil) {
		if !strings.HasPrefix(o.Tag, modelcatalog.Dir()) {
			t.Errorf("eif options must be paths: %q", o.Tag)
		}
	}
}

func TestOnlyChosenGGUFFilesAreDownloaded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, ok := catalogEntryFor("bge-m3"); ok {
		t.Error("an Ollama model name was taken for a catalog file")
	}
	if e, ok := catalogEntryFor("/x/bge-m3-Q4_K_M.gguf"); !ok || e.ID != "bge-m3" {
		t.Errorf("path of a catalog file: %+v %v", e, ok)
	}
	var out bytes.Buffer
	r := OnboardResult{EmbeddingProvider: "ollama", EmbeddingModel: "bge-m3-Q4_K_M.gguf", LLMProvider: "ollama", LLMModel: "qwen3:8b"}
	if downloadChosenModels(context.Background(), r, &out) || out.Len() != 0 {
		t.Errorf("server providers must not download files: %q", out.String())
	}
}
