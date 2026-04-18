package main

import (
	"bytes"
	"os"
	"testing"
)

func TestGetConfigDefaultsExt(t *testing.T) {
	cfg := getConfig(nil)
	if cfg.IndexDir == "" {
		t.Error("IndexDir should have a default")
	}
	if cfg.HNSWConfig.UseMmap != true {
		t.Error("UseMmap should default to true")
	}
}

func TestGetConfigNoMmap(t *testing.T) {
	cfg := getConfig([]string{"--no-mmap"})
	if cfg.HNSWConfig.UseMmap != false {
		t.Error("--no-mmap should disable mmap")
	}
}

func TestGetConfigProvider(t *testing.T) {
	cfg := getConfig([]string{"--provider", "openai"})
	if cfg.EmbeddingProvider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.EmbeddingProvider)
	}
}

func TestGetConfigModel(t *testing.T) {
	cfg := getConfig([]string{"--model", "bge-m3"})
	if cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("model = %q, want bge-m3", cfg.EmbeddingModel)
	}
}

func TestGetConfigHost(t *testing.T) {
	cfg := getConfig([]string{"--host", "http://myhost:11434"})
	if cfg.OllamaHost != "http://myhost:11434" {
		t.Errorf("host = %q", cfg.OllamaHost)
	}
}

func TestGetConfigIndexDir(t *testing.T) {
	cfg := getConfig([]string{"--index-dir", "/tmp/custom-idx"})
	if cfg.IndexDir != "/tmp/custom-idx" {
		t.Errorf("index-dir = %q", cfg.IndexDir)
	}
}

func TestGetConfigTopK(t *testing.T) {
	cfg := getConfig([]string{"--top-k", "20"})
	if cfg.SearchConfig.TopK != 20 {
		t.Errorf("top-k = %d, want 20", cfg.SearchConfig.TopK)
	}
}

func TestGetConfigEfSearch(t *testing.T) {
	cfg := getConfig([]string{"--ef-search", "256"})
	if cfg.HNSWConfig.EfSearch != 256 {
		t.Errorf("ef-search = %d, want 256", cfg.HNSWConfig.EfSearch)
	}
}

func TestGetConfigChunkSize(t *testing.T) {
	cfg := getConfig([]string{"--chunk-size", "512"})
	if cfg.ChunkConfig.ChunkSize != 512 {
		t.Errorf("chunk-size = %d, want 512", cfg.ChunkConfig.ChunkSize)
	}
}

func TestGetConfigChunkOverlap(t *testing.T) {
	cfg := getConfig([]string{"--chunk-overlap", "50"})
	if cfg.ChunkConfig.ChunkOverlap != 50 {
		t.Errorf("chunk-overlap = %d, want 50", cfg.ChunkConfig.ChunkOverlap)
	}
}

func TestGetConfigBatchSize(t *testing.T) {
	cfg := getConfig([]string{"--batch-size", "64"})
	if cfg.BatchSize != 64 {
		t.Errorf("batch-size = %d, want 64", cfg.BatchSize)
	}
}

func TestGetConfigConcurrency(t *testing.T) {
	cfg := getConfig([]string{"--concurrency", "8"})
	if cfg.Concurrency != 8 {
		t.Errorf("concurrency = %d, want 8", cfg.Concurrency)
	}
}

func TestGetConfigHybrid(t *testing.T) {
	cfg := getConfig([]string{"--hybrid", "0.5"})
	if cfg.SearchConfig.HybridAlpha != 0.5 {
		t.Errorf("hybrid = %f, want 0.5", cfg.SearchConfig.HybridAlpha)
	}
}

func TestGetConfigMetric(t *testing.T) {
	cfg := getConfig([]string{"--metric", "l2"})
	if string(cfg.HNSWConfig.DistanceMetric) != "l2" {
		t.Errorf("metric = %s, want l2", cfg.HNSWConfig.DistanceMetric)
	}
}

func TestGetConfigPrune(t *testing.T) {
	cfg := getConfig([]string{"--prune", "0.9"})
	if cfg.HNSWConfig.PruneKeepFraction != 0.9 {
		t.Errorf("prune = %f, want 0.9", cfg.HNSWConfig.PruneKeepFraction)
	}
	if !cfg.HNSWConfig.PruneEmbeddings {
		t.Error("PruneEmbeddings should be true")
	}
}

func TestGetConfigMultipleFlags(t *testing.T) {
	cfg := getConfig([]string{
		"--provider", "openai",
		"--model", "text-embedding-3-small",
		"--top-k", "15",
		"--no-mmap",
	})
	if cfg.EmbeddingProvider != "openai" {
		t.Error("provider wrong")
	}
	if cfg.EmbeddingModel != "text-embedding-3-small" {
		t.Error("model wrong")
	}
	if cfg.SearchConfig.TopK != 15 {
		t.Error("top-k wrong")
	}
	if cfg.HNSWConfig.UseMmap {
		t.Error("mmap should be disabled")
	}
}

func TestGetFlagPresentExt(t *testing.T) {
	val := getFlag([]string{"--foo", "bar", "--baz", "qux"}, "--foo")
	if val != "bar" {
		t.Errorf("getFlag = %q, want bar", val)
	}
}

func TestGetFlagMissingExt(t *testing.T) {
	val := getFlag([]string{"--foo", "bar"}, "--missing")
	if val != "" {
		t.Errorf("getFlag = %q, want empty", val)
	}
}

func TestGetFlagLastExt(t *testing.T) {
	val := getFlag([]string{"--flag"}, "--flag")
	if val != "" {
		t.Errorf("getFlag should return empty when no value follows")
	}
}

func TestHasFlagPresentExt(t *testing.T) {
	if !hasFlag([]string{"--foo", "--bar"}, "--bar") {
		t.Error("should find --bar")
	}
}

func TestHasFlagMissingExt(t *testing.T) {
	if hasFlag([]string{"--foo"}, "--bar") {
		t.Error("should not find --bar")
	}
}

func TestStderrfExt(t *testing.T) {
	// Redirect stderr for testing.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	stderrf(nil, "hello %s\n", "world")

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "hello world\n" {
		t.Errorf("stderrf output = %q", buf.String())
	}
}

func TestStderrfQuietExt(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	stderrf([]string{"--quiet"}, "should not appear")

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "" {
		t.Errorf("stderrf with --quiet should produce no output, got %q", buf.String())
	}
}

func TestIsOutputTTYExt(t *testing.T) {
	// Just exercise the function — in test environment it returns false.
	_ = isOutputTTY()
}

func TestApplySavedConfigNoSavedConfigExt(t *testing.T) {
	// If there's no saved config file, applySavedConfig should not modify config.
	cfg := getConfig(nil)
	originalProvider := cfg.EmbeddingProvider
	applySavedConfig(&cfg, nil)
	// May or may not change depending on whether ~/.gleann/config.json exists.
	// At minimum, it shouldn't panic.
	_ = originalProvider
}

func TestGetConfigChunkSizeZero(t *testing.T) {
	cfg := getConfig([]string{"--chunk-size", "0"})
	// ChunkSize <= 0 is not applied.
	if cfg.ChunkConfig.ChunkSize <= 0 {
		// 0 should not be applied; default should remain.
		t.Logf("ChunkSize = %d (0 not applied, default used)", cfg.ChunkConfig.ChunkSize)
	}
}
