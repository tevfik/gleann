package main

import (
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestCmdConfigSet_OllamaAPIKeyAliasesAndMasking(t *testing.T) {
	for _, key := range []string{"ollama_api_key", "ollama-api-key", "ollama_key"} {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)
		t.Setenv("USERPROFILE", tmp)

		out := captureStdout(t, func() { cmdConfigSet([]string{key, "ollama-secret-123"}) })
		if strings.Contains(out, "ollama-secret-123") {
			t.Errorf("%s: set output leaks the key: %q", key, out)
		}
		if !strings.Contains(out, "oll…") {
			t.Errorf("%s: set output should show masked key, got %q", key, out)
		}

		saved := tui.LoadSavedConfig()
		if saved == nil || saved.OllamaAPIKey != "ollama-secret-123" {
			t.Fatalf("%s: saved OllamaAPIKey = %+v", key, saved)
		}
		if saved.OpenAIKey != "" {
			t.Errorf("%s: must not touch openai_api_key", key)
		}

		show := captureStdout(t, func() { cmdConfigShow() })
		if strings.Contains(show, "ollama-secret-123") {
			t.Errorf("%s: config show leaks the key: %s", key, show)
		}
		if !strings.Contains(show, `"ollama_api_key": "oll…"`) {
			t.Errorf("%s: config show should include masked ollama_api_key, got %s", key, show)
		}
	}
}

func TestCmdConfigShow_MasksAllSecrets(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := tui.SaveConfig(tui.OnboardResult{
		OllamaAPIKey: "ollama-long-secret",
		OpenAIKey:    "sk-openai-long-secret",
		AnthropicKey: "sk-ant-long-secret",
		Completed:    true,
	}); err != nil {
		t.Fatal(err)
	}
	show := captureStdout(t, func() { cmdConfigShow() })
	for _, secret := range []string{"ollama-long-secret", "sk-openai-long-secret", "sk-ant-long-secret"} {
		if strings.Contains(show, secret) {
			t.Errorf("config show leaks %q", secret)
		}
	}
	// The stored file itself keeps the real values.
	if saved := tui.LoadSavedConfig(); saved.OllamaAPIKey != "ollama-long-secret" {
		t.Errorf("stored key changed: %q", saved.OllamaAPIKey)
	}
}

func TestApplyConfigSet_ClearAndUnknown(t *testing.T) {
	cfg := &tui.OnboardResult{OllamaAPIKey: "x"}
	disp, err := applyConfigSet(cfg, "ollama_api_key", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OllamaAPIKey != "" || disp != `""` {
		t.Errorf("clear: key=%q display=%q", cfg.OllamaAPIKey, disp)
	}
	if _, err := applyConfigSet(cfg, "no_such_key", "v"); err == nil {
		t.Error("unknown key should error")
	}
}

func TestGetConfig_OllamaAPIKeyFlag(t *testing.T) {
	cfg := getConfig([]string{"--host", "gpu:11434", "--ollama-api-key", "flag-key"})
	if cfg.OllamaAPIKey != "flag-key" {
		t.Errorf("OllamaAPIKey = %q, want flag-key", cfg.OllamaAPIKey)
	}
	if cfg.OllamaHost != "http://gpu:11434" {
		t.Errorf("OllamaHost = %q", cfg.OllamaHost)
	}
}

func TestApplySavedConfig_OllamaAPIKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := tui.SaveConfig(tui.OnboardResult{OllamaAPIKey: "saved-key", Completed: true}); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	applySavedConfig(&cfg, nil)
	if cfg.OllamaAPIKey != "saved-key" {
		t.Errorf("OllamaAPIKey = %q, want saved-key", cfg.OllamaAPIKey)
	}
	// A CLI flag wins over the saved value.
	cfg = getConfig([]string{"--ollama-api-key", "flag-key"})
	applySavedConfig(&cfg, nil)
	if cfg.OllamaAPIKey != "flag-key" {
		t.Errorf("OllamaAPIKey = %q, want flag-key", cfg.OllamaAPIKey)
	}
	if got := resolveOllamaAPIKey(gleann.Config{}); got != "saved-key" {
		t.Errorf("resolveOllamaAPIKey = %q, want saved-key", got)
	}
}

func TestBuildChatConfig_ProviderSpecificKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv(gleann.OllamaAPIKeyEnv, "")

	cfg := gleann.DefaultConfig()
	cfg.LLMProvider = "ollama"
	cfg.OllamaAPIKey = "ollama-k"
	cfg.OpenAIAPIKey = "sk-openai"

	if got := buildChatConfig(nil, cfg).APIKey; got != "ollama-k" {
		t.Errorf("ollama APIKey = %q, want ollama-k (never the OpenAI key)", got)
	}
	if got := buildChatConfig([]string{"--llm-provider", "openai"}, cfg).APIKey; got != "sk-openai" {
		t.Errorf("openai APIKey = %q, want sk-openai", got)
	}
	// --api-key overrides for either provider.
	if got := buildChatConfig([]string{"--api-key", "override"}, cfg).APIKey; got != "override" {
		t.Errorf("--api-key APIKey = %q, want override", got)
	}
	cfg.OllamaAPIKey = ""
	if got := buildChatConfig(nil, cfg).APIKey; got != "" {
		t.Errorf("ollama without key: APIKey = %q, want empty", got)
	}
}

func TestNewEmbedder_OllamaKey(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	cfg := gleann.DefaultConfig()
	cfg.EmbeddingProvider = "ollama"
	cfg.OllamaAPIKey = "emb-k"
	cfg.OpenAIAPIKey = "sk-openai"
	// newEmbedder must not panic and routes the Ollama key; the header itself
	// is covered by internal/embedding tests.
	if e := newEmbedder(cfg); e == nil {
		t.Fatal("newEmbedder returned nil")
	}
	if got := cfg.APIKeyForProvider(cfg.EmbeddingProvider); got != "emb-k" {
		t.Errorf("embedding key = %q, want emb-k", got)
	}
}

func TestExtractPositionalArgs_SkipsKeyFlagValues(t *testing.T) {
	pos, _ := extractPositionalArgs([]string{"idx", "--ollama-api-key", "k", "--api-key", "k2", "question"})
	if len(pos) != 2 || pos[0] != "idx" || pos[1] != "question" {
		t.Errorf("positional = %v, want [idx question]", pos)
	}
}
