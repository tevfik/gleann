package gleann

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// withEIFRanker toggles the in-process engine for one test.
func withEIFRanker(t *testing.T, on bool) {
	t.Helper()
	oldRank, oldCheck := eifRank, eifRerankModelInstalled
	t.Cleanup(func() { eifRank, eifRerankModelInstalled = oldRank, oldCheck })
	eifRank = nil
	eifRerankModelInstalled = nil
	if on {
		eifRank = func(context.Context, string, string, []string) ([]float32, error) { return nil, nil }
	}
}

func TestAutoRerankerProvider(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("GLEANN_CONFIG_DIR", cfgDir)
	models := filepath.Join(cfgDir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "local-reranker-q4.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		eif       bool
		installed func(string) bool
		embedding string
		model     string
		want      RerankerProvider
	}{
		{"pure build keeps the embedding provider", false, nil, "jina", "", RerankerJina},
		{"pure build, unknown embedding provider", false, nil, "openai", "", RerankerOllama},
		{"pure build cannot run the in-process engine", false, nil, "eif", "", RerankerOllama},
		{"engine and no model", true, nil, "ollama", "", RerankerEIF},
		{"engine with a .gguf path", true, nil, "ollama", "Some/Model.GGUF", RerankerEIF},
		{"engine with a catalog id", true, func(m string) bool { return m == "ms-marco-minilm-l6" }, "ollama", "ms-marco-minilm-l6", RerankerEIF},
		{"engine with a downloaded model name", true, nil, "ollama", "local-reranker", RerankerEIF},
		{"engine, remote model name, ollama embeddings", true, nil, "ollama", "qllama/bge-reranker", RerankerOllama},
		{"engine, path-like model is never a local name", true, nil, "voyage", "a/b", RerankerVoyage},
		{"engine, remote model, eif embeddings", true, nil, "eif", "remote-model", RerankerEIF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withEIFRanker(t, c.eif)
			eifRerankModelInstalled = c.installed
			if got := AutoRerankerProvider(c.embedding, c.model); got != c.want {
				t.Errorf("AutoRerankerProvider(%q, %q) = %s, want %s", c.embedding, c.model, got, c.want)
			}
		})
	}
}

func TestResolveRerankerConfigLlamacppURL(t *testing.T) {
	withEIFRanker(t, false)
	cfg := DefaultConfig()
	cfg.SearchConfig.RerankerConfig = RerankerConfig{Provider: RerankerLlamacpp}
	cfg.OpenAIBaseURL = "http://llama.test:8080"

	// Without llama.cpp embeddings there is no URL to inherit.
	cfg.EmbeddingProvider = "ollama"
	if rc := ResolveRerankerConfig(cfg, ""); rc.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty", rc.BaseURL)
	}
	cfg.EmbeddingProvider = string(RerankerLlamacpp)
	if rc := ResolveRerankerConfig(cfg, ""); rc.BaseURL != "http://llama.test:8080" {
		t.Errorf("BaseURL = %q, want the embedding server's", rc.BaseURL)
	}
	// An explicit rerank URL is never overwritten.
	cfg.SearchConfig.RerankerConfig.BaseURL = "http://rerank.test"
	if rc := ResolveRerankerConfig(cfg, ""); rc.BaseURL != "http://rerank.test" {
		t.Errorf("BaseURL = %q", rc.BaseURL)
	}
}
