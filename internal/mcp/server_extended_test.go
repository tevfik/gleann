package mcp

import (
	"testing"
)

func TestNewServerExtended(t *testing.T) {
	cfg := Config{
		IndexDir:          t.TempDir(),
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test-model",
		OllamaHost:        "http://localhost:11434",
		Version:           "1.0.0",
	}
	s := NewServer(cfg)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	defer s.Close()

	if s.config.IndexDir != cfg.IndexDir {
		t.Error("IndexDir mismatch")
	}
	if s.config.EmbeddingModel != "test-model" {
		t.Error("EmbeddingModel mismatch")
	}
}

func TestNewServerDefaultVersion(t *testing.T) {
	cfg := Config{
		IndexDir: t.TempDir(),
	}
	s := NewServer(cfg)
	defer s.Close()

	// Version should default to "dev" in the gleann config.
	if s.config.IndexDir != cfg.IndexDir {
		t.Error("IndexDir should be set")
	}
}

func TestServerClose(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	// Should not panic.
	s.Close()
}

func TestServerGetSearcherErrorExt(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	_, err := s.getSearcher("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent index")
	}
}

func TestServerTouchLRU(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	s.searcherLRU = []string{"a", "b", "c"}
	s.touchLRU("a")
	if s.searcherLRU[len(s.searcherLRU)-1] != "a" {
		t.Error("'a' should be at end after touch")
	}
	if s.searcherLRU[0] != "b" {
		t.Error("'b' should be first after touch")
	}
}

func TestServerEvictOldest(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	s.searcherLRU = []string{"old", "new"}
	s.searchers["old"] = nil
	s.searchers["new"] = nil

	s.evictOldest()
	if len(s.searcherLRU) != 1 {
		t.Errorf("LRU len = %d", len(s.searcherLRU))
	}
	if s.searcherLRU[0] != "new" {
		t.Error("'new' should remain")
	}
	if _, ok := s.searchers["old"]; ok {
		t.Error("'old' should be evicted")
	}
}

func TestServerEvictOldestEmpty(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	// Should not panic on empty.
	s.evictOldest()
}

func TestMaxCachedSearchersExt(t *testing.T) {
	if maxCachedSearchers <= 0 {
		t.Error("maxCachedSearchers should be positive")
	}
}
