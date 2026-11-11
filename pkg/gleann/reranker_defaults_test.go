package gleann

import (
	"context"
	"testing"
)

func TestResolveRerankerConfig_EIFDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SearchConfig.RerankerConfig.Provider = RerankerEIF
	rc := ResolveRerankerConfig(cfg, "")
	if rc.Pool != DefaultEIFRerankPool || rc.MaxDocChars != DefaultEIFRerankChars || !rc.autoCodeModel || rc.Model != DefaultEIFRerankModel {
		t.Fatalf("eif defaults = %+v", rc)
	}
	// A chosen model is used as is; explicit pool and length win.
	cfg.SearchConfig.RerankerConfig.Pool, cfg.SearchConfig.RerankerConfig.MaxDocChars = 7, 900
	rc = ResolveRerankerConfig(cfg, "my.gguf")
	if rc.autoCodeModel || rc.Model != "my.gguf" || rc.Pool != 7 || rc.MaxDocChars != 900 {
		t.Fatalf("explicit settings = %+v", rc)
	}
	// Server rerankers keep scoring every candidate in full.
	cfg = DefaultConfig()
	cfg.SearchConfig.RerankerConfig.Provider = RerankerJina
	rc = ResolveRerankerConfig(cfg, "")
	if rc.Pool != 0 || rc.MaxDocChars != 0 || rc.autoCodeModel {
		t.Fatalf("server defaults = %+v", rc)
	}
}

// poolReranker reverses its input and prefers a pool size.
type poolReranker struct {
	reverseReranker
	pool int
}

func (p *poolReranker) PreferredPool() int { return p.pool }

func TestRerankStage_PreferredPool(t *testing.T) {
	rr := &poolReranker{pool: 4}
	rerankStage(context.Background(), rr, "how does it work", stageInput(10), 3, SearchConfig{})
	if len(rr.got) != 4 {
		t.Fatalf("pool = %d, want the reranker's 4", len(rr.got))
	}
	rerankStage(context.Background(), rr, "how does it work", stageInput(10), 3, SearchConfig{RerankPool: 6})
	if len(rr.got) != 6 {
		t.Fatalf("pool = %d, want the request's 6", len(rr.got))
	}
}

func TestRerankEIF_PicksCodeModel(t *testing.T) {
	oldRank, oldCheck := eifRank, eifRerankModelInstalled
	t.Cleanup(func() { eifRank, eifRerankModelInstalled = oldRank, oldCheck })
	var used string
	eifRank = func(_ context.Context, model, _ string, docs []string) ([]float32, error) {
		used = model
		return make([]float32, len(docs)), nil
	}
	installed := true
	eifRerankModelInstalled = func(m string) bool { return installed && m == DefaultEIFCodeRerankModel }

	code := []SearchResult{{ID: 1, Metadata: map[string]any{"source": "a.go"}}, {ID: 2, Metadata: map[string]any{"source": "b.py"}}, {ID: 3, Metadata: map[string]any{"source": "c.md"}}}
	docs := []SearchResult{{ID: 1, Metadata: map[string]any{"source": "a.pdf"}}, {ID: 2, Metadata: map[string]any{"source": "b.md"}}, {ID: 3, Metadata: map[string]any{"source": "c.go"}}}

	cfg := DefaultConfig()
	cfg.SearchConfig.RerankerConfig.Provider = RerankerEIF
	auto := NewReranker(ResolveRerankerConfig(cfg, ""))
	cases := []struct {
		name      string
		r         *CrossEncoderReranker
		in        []SearchResult
		installed bool
		want      string
	}{
		{"code candidates", auto, code, true, DefaultEIFCodeRerankModel},
		{"document candidates", auto, docs, true, DefaultEIFRerankModel},
		{"code model not downloaded", auto, code, false, DefaultEIFRerankModel},
		{"user's model", NewReranker(ResolveRerankerConfig(cfg, "mine.gguf")), code, true, "mine.gguf"},
	}
	for _, c := range cases {
		installed = c.installed
		if _, err := c.r.rerankEIF(context.Background(), "q", c.in, 0); err != nil {
			t.Fatal(err)
		}
		if used != c.want {
			t.Errorf("%s: model %q, want %q", c.name, used, c.want)
		}
	}
}
