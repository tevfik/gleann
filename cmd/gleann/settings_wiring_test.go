package main

import (
	"testing"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

// A start-up override (the embedded llama-server URL) survives a settings
// save that did not touch it, and yields to one that did.
func TestKeepRuntimeOverrides(t *testing.T) {
	asLoaded := gleann.DefaultConfig()
	asLoaded.EmbeddingModel = "e5.gguf"
	running := asLoaded
	running.OpenAIBaseURL = "http://127.0.0.1:39001/v1" // set by initLlamaCPP
	overrides := gleann.ChangedKeys(asLoaded, running)

	fresh := asLoaded
	fresh.EmbeddingModel = "bge-m3.gguf" // the save changed the model only
	got := keepRuntimeOverrides(fresh, running, asLoaded, overrides)
	if got.OpenAIBaseURL != running.OpenAIBaseURL {
		t.Errorf("runtime URL lost: %q", got.OpenAIBaseURL)
	}
	if got.EmbeddingModel != "bge-m3.gguf" {
		t.Errorf("saved model not applied: %q", got.EmbeddingModel)
	}

	fresh.OpenAIBaseURL = "http://gpu-box:8000/v1" // the save set it on purpose
	if got := keepRuntimeOverrides(fresh, running, asLoaded, overrides); got.OpenAIBaseURL != "http://gpu-box:8000/v1" {
		t.Errorf("an explicit change must win over the runtime value, got %q", got.OpenAIBaseURL)
	}
}

func TestSavedRerankerReachesSearchConfig(t *testing.T) {
	cfg := gleann.DefaultConfig()
	applySavedReranker(&cfg, &tui.OnboardResult{RerankEnabled: true, RerankModel: "m.gguf", RerankProvider: "eif"})
	if !cfg.SearchConfig.UseReranker || cfg.SearchConfig.RerankerConfig.Model != "m.gguf" ||
		cfg.SearchConfig.RerankerConfig.Provider != gleann.RerankerEIF {
		t.Fatalf("saved reranker not applied: %+v", cfg.SearchConfig)
	}
	if !wantRerank(cfg, nil) || wantRerank(cfg, []string{"--no-rerank"}) {
		t.Error("the saved setting turns reranking on; --no-rerank turns it off")
	}
}

func TestSavedProxyModeReachesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := tui.SaveConfig(tui.OnboardResult{ProxyMode: "passthrough", Completed: true}); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	applySavedConfig(&cfg, nil)
	if cfg.ProxyMode != "passthrough" {
		t.Fatalf("ProxyMode = %q, want passthrough from config.json", cfg.ProxyMode)
	}
}
