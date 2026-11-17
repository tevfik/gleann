package tui

import (
	"testing"
)

func TestLoadConfig_DefaultsWithoutSavedConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := loadConfig()
	if cfg.IndexDir != DefaultIndexDir() {
		t.Errorf("IndexDir = %q, want the default %q", cfg.IndexDir, DefaultIndexDir())
	}
}

func TestLoadConfig_SavedValuesOverrideDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	idx := t.TempDir()
	if err := SaveConfig(OnboardResult{
		EmbeddingProvider: "openai", EmbeddingModel: "text-embedding-3-small",
		OllamaHost: "http://gpu:11434", OpenAIKey: "sk-test", OllamaAPIKey: "olk", IndexDir: idx, Completed: true,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig()
	if cfg.EmbeddingProvider != "openai" || cfg.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("embedding settings not applied: %q %q", cfg.EmbeddingProvider, cfg.EmbeddingModel)
	}
	if cfg.OllamaHost != "http://gpu:11434" || cfg.OpenAIAPIKey != "sk-test" || cfg.OllamaAPIKey != "olk" {
		t.Errorf("host/keys not applied: %+v", cfg)
	}
	if cfg.IndexDir != idx {
		t.Errorf("IndexDir = %q, want %q", cfg.IndexDir, idx)
	}
}
