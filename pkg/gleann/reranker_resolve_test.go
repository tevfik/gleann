package gleann

import (
	"context"
	"testing"
)

func TestResolveRerankerConfig(t *testing.T) {
	saved := eifRank
	t.Cleanup(func() { eifRank = saved })

	cfg := DefaultConfig()
	cfg.EmbeddingProvider = "ollama"
	cfg.OllamaHost = "http://ollama:11434"

	eifRank = nil // pure build
	rc := ResolveRerankerConfig(cfg, "")
	if rc.Provider != RerankerOllama || rc.Model != DefaultServerRerankModel || rc.BaseURL != "http://ollama:11434" {
		t.Errorf("pure build, ollama embeddings: got %+v", rc)
	}

	eifRank = func(context.Context, string, string, []string) ([]float32, error) { return nil, nil }
	if rc := ResolveRerankerConfig(cfg, ""); rc.Provider != RerankerEIF || rc.Model != DefaultEIFRerankModel {
		t.Errorf("eif build, no model: want the built-in default, got %+v", rc)
	}
	if rc := ResolveRerankerConfig(cfg, "bge-reranker-v2-m3-Q8_0.gguf"); rc.Provider != RerankerEIF {
		t.Errorf("a GGUF reranker runs in process, got %+v", rc)
	}
	if rc := ResolveRerankerConfig(cfg, "qllama/bge-reranker-v2-m3"); rc.Provider != RerankerOllama {
		t.Errorf("an Ollama model name stays on Ollama, got %+v", rc)
	}

	cfg.SearchConfig.RerankerConfig.Provider = RerankerJina
	if rc := ResolveRerankerConfig(cfg, ""); rc.Provider != RerankerJina || rc.Model != DefaultServerRerankModel {
		t.Errorf("an explicit provider wins, got %+v", rc)
	}
	cfg.SearchConfig.RerankerConfig.Provider = RerankerAuto
	if rc := ResolveRerankerConfig(cfg, ""); rc.Provider != RerankerEIF {
		t.Errorf("auto behaves like empty, got %+v", rc)
	}
}
