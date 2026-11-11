//go:build eif

package embedding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
)

// GLEANN_TEST_RERANK_MODEL points at a reranker GGUF (e.g.
// bge-reranker-v2-m3-Q8_0.gguf or mmarco-mMiniLMv2-L12-H384-v1-Q8_0.gguf).
func TestEIFRanker_RelevantFirst(t *testing.T) {
	model := os.Getenv("GLEANN_TEST_RERANK_MODEL")
	if model == "" {
		t.Skip("GLEANN_TEST_RERANK_MODEL not set")
	}
	r, err := GetOrLoadEIFRanker(model)
	if err != nil {
		t.Fatal(err)
	}
	docs := []string{
		"Bananas are yellow and rich in potassium.",
		"SpCas9 recognizes and binds NGG PAM sequences immediately 3' of the target site.",
		"The CAP theorem is about consistency, availability and partition tolerance.",
		"Süper iletken kübitler 15 milikelvinde çalışır.",
	}
	scores, err := r.Rank(context.Background(), "Which PAM sequence does SpCas9 recognize?", docs)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range scores {
		if i != 1 && s >= scores[1] {
			t.Errorf("doc %d scored %.2f >= relevant doc %.2f", i, s, scores[1])
		}
	}
	// Same input twice: identical scores whatever instance served it.
	again, err := r.Rank(context.Background(), "Which PAM sequence does SpCas9 recognize?", docs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range scores {
		if scores[i] != again[i] {
			t.Errorf("doc %d: %.4f then %.4f", i, scores[i], again[i])
		}
	}
}

func TestEIFRanker_RejectsEmbeddingModelAndMissing(t *testing.T) {
	if _, err := resolveRerankModelPath(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing reranker must not resolve (no fallback to an embedding model)")
	}
	emb := os.Getenv("GLEANN_TEST_EMBED_MODEL")
	if emb == "" {
		t.Skip("GLEANN_TEST_EMBED_MODEL not set")
	}
	if _, err := GetOrLoadEIFRanker(emb); err == nil || !strings.Contains(err.Error(), "not a reranker") {
		t.Errorf("embedding model loaded as reranker: %v", err)
	}
}

func TestResolveRerankModelPath_CatalogID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Not downloaded: the error names the download command.
	_, err := resolveRerankModelPath("ms-marco-minilm-l6")
	if err == nil || !strings.Contains(err.Error(), "gleann models download ms-marco-minilm-l6") {
		t.Fatalf("want a download hint, got %v", err)
	}
	// Installed (catalog size): the id resolves to the catalog file.
	e, ok := modelcatalog.Find("ms-marco-minilm-l6")
	if !ok || e.Pooler == nil {
		t.Fatal("catalog entry missing")
	}
	if err := os.MkdirAll(filepath.Dir(e.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Path(), make([]byte, e.Pooler.Bytes), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRerankModelPath("ms-marco-minilm-l6")
	if err != nil || got != e.Path() {
		t.Fatalf("got %q, %v; want %q", got, err, e.Path())
	}
}
