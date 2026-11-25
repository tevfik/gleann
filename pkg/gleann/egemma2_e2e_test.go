//go:build eif

package gleann_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

const egemma2SHA = "6f1bd4ac6c5df7444f9cca7ca36cafe6cfa34cd6f49fefb1e0b4be8143aed8bc"

// TestEmbeddingGemma2Retrieval indexes and searches a small Turkish and English set with the real Q8_0 file
// through the eif engine, prefixes included. It needs GLEANN_TEST_EGEMMA2_GGUF (the model file, sha256-checked)
// and GLEANN_TEST_EGEMMA2_RETR (the evaluation's retr.json: queries[i] belongs to passages[i]); it skips without
// them and never downloads.
func TestEmbeddingGemma2Retrieval(t *testing.T) {
	model, retr := os.Getenv("GLEANN_TEST_EGEMMA2_GGUF"), os.Getenv("GLEANN_TEST_EGEMMA2_RETR")
	if model == "" || retr == "" {
		t.Skip("GLEANN_TEST_EGEMMA2_GGUF / GLEANN_TEST_EGEMMA2_RETR not set")
	}
	f, err := os.Open(model)
	if err != nil {
		t.Skipf("model file absent: %v", err)
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != egemma2SHA {
		t.Fatalf("sha256 %s, want %s", got, egemma2SHA)
	}
	var data struct{ Queries, Passages []string }
	raw, err := os.ReadFile(retr)
	if err != nil {
		t.Skipf("retr.json absent: %v", err)
	}
	if err := json.Unmarshal([]byte(string(raw)), &data); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	cfg := gleann.Config{IndexDir: t.TempDir(), Backend: "hnsw", EmbeddingModel: model, EmbeddingProvider: "eif",
		SearchConfig: gleann.SearchConfig{HybridAlpha: 1}}
	emb := embedding.NewComputer(embedding.Options{Provider: embedding.ProviderEIF, Model: model})
	b, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]gleann.Item, len(data.Passages))
	for i, p := range data.Passages {
		items[i] = gleann.Item{Text: p, Metadata: map[string]any{"source": fmt.Sprintf("p%02d", i)}}
	}
	if err := b.Build(ctx, "egemma2", items); err != nil {
		t.Fatal(err)
	}
	meta, err := gleann.GetIndexMeta(cfg.IndexDir, "egemma2")
	if err != nil || meta.PassagePrefix != "title: none | text: " || meta.QueryPrefix != "task: search result | query: " {
		t.Fatalf("meta %+v %v", meta, err)
	}
	s := gleann.NewSearcher(cfg, emb)
	if err := s.Load(ctx, "egemma2"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mrr float64
	top1 := 0
	for qi, q := range data.Queries {
		res, err := s.Search(ctx, q, gleann.WithTopK(10))
		if err != nil {
			t.Fatal(err)
		}
		for rank, r := range res {
			if r.Text == data.Passages[qi] {
				mrr += 1 / float64(rank+1)
				if rank == 0 {
					top1++
				}
				break
			}
		}
	}
	mrr /= float64(len(data.Queries))
	t.Logf("MRR@10 %.3f  R@1 %.3f over %d queries", mrr, float64(top1)/float64(len(data.Queries)), len(data.Queries))
	if mrr < 0.9 {
		t.Errorf("MRR@10 %.3f, the evaluation measured 0.944", mrr)
	}
}
