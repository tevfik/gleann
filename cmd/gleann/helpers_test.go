package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}

	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestGetFlag(t *testing.T) {
	args := []string{"build", "--docs", "/tmp", "--model", "bge-m3", "--top-k", "5"}

	if got := getFlag(args, "--docs"); got != "/tmp" {
		t.Errorf("getFlag(--docs) = %q, want /tmp", got)
	}
	if got := getFlag(args, "--model"); got != "bge-m3" {
		t.Errorf("getFlag(--model) = %q, want bge-m3", got)
	}
	if got := getFlag(args, "--missing"); got != "" {
		t.Errorf("getFlag(--missing) = %q, want empty", got)
	}
	// Edge: flag at end with no value
	args2 := []string{"--docs"}
	if got := getFlag(args2, "--docs"); got != "" {
		t.Errorf("getFlag(--docs at end) = %q, want empty", got)
	}
}

func TestHasFlag(t *testing.T) {
	args := []string{"build", "--graph", "--docs", "/tmp"}

	if !hasFlag(args, "--graph") {
		t.Error("hasFlag(--graph) should be true")
	}
	if hasFlag(args, "--missing") {
		t.Error("hasFlag(--missing) should be false")
	}
}

func TestGetConfig_DefaultValues(t *testing.T) {
	args := []string{}
	config := getConfig(args)

	if config.EmbeddingModel == "" {
		t.Error("default EmbeddingModel should not be empty")
	}
	if config.IndexDir == "" {
		t.Error("default IndexDir should not be empty")
	}
}

func TestGetConfig_CustomFlags(t *testing.T) {
	args := []string{"--model", "custom-model", "--provider", "openai", "--top-k", "20", "--host", "http://custom:11434"}

	config := getConfig(args)

	if config.EmbeddingModel != "custom-model" {
		t.Errorf("EmbeddingModel = %q, want custom-model", config.EmbeddingModel)
	}
	if config.EmbeddingProvider != "openai" {
		t.Errorf("EmbeddingProvider = %q, want openai", config.EmbeddingProvider)
	}
	if config.SearchConfig.TopK != 20 {
		t.Errorf("TopK = %d, want 20", config.SearchConfig.TopK)
	}
	if config.OllamaHost != "http://custom:11434" {
		t.Errorf("OllamaHost = %q, want http://custom:11434", config.OllamaHost)
	}
}

func TestApplySavedConfig_CLIOverride(t *testing.T) {
	// When CLI flag is provided, saved config should not override it.
	config := gleann.DefaultConfig()
	config.EmbeddingModel = "cli-model"
	applySavedConfig(&config, []string{"--model", "cli-model"})
	if config.EmbeddingModel != "cli-model" {
		t.Errorf("EmbeddingModel = %q, want cli-model (CLI should take precedence)", config.EmbeddingModel)
	}
}

func TestApplySavedConfig_EnvIndexDirBeatsSavedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte(`{"index_dir":"/saved/dir","completed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_INDEX_DIR", "/env/dir")
	if got := savedIndexDir(); got != filepath.FromSlash("/env/dir") {
		t.Fatalf("IndexDir = %q, GLEANN_INDEX_DIR must win over saved config", got)
	}
	t.Setenv("GLEANN_INDEX_DIR", "")
	if got := savedIndexDir(); got != filepath.FromSlash("/saved/dir") {
		t.Fatalf("IndexDir = %q, saved config should apply without env", got)
	}
}

func savedIndexDir() string {
	cfg := getConfig(nil)
	applySavedConfig(&cfg, nil)
	return cfg.IndexDir
}
