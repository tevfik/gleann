package embedding

import (
	"testing"
)

// ── GetModelTokenLimit (deeper branches) ──────────────────────

func TestGetModelTokenLimitCov2_Ollama(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"bge-m3", 2048},
		{"bge-large-en-v1.5", 512},
		{"nomic-embed-text", 2048},
		{"all-minilm", 256},
		{"snowflake-arctic-embed", 512},
		{"text-embedding-004", 2048},
		{"unknown-model-xyz99", 384},
	}
	for _, tt := range tests {
		got := GetModelTokenLimit(tt.model)
		if got != tt.want {
			t.Errorf("GetModelTokenLimit(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

// ── TruncateToTokenLimit (deeper branches) ────────────────────

func TestTruncateToTokenLimitCov2_Short(t *testing.T) {
	text := "short text"
	result := TruncateToTokenLimit(text, 1000)
	if result != text {
		t.Fatal("short text should not be truncated")
	}
}

func TestTruncateToTokenLimitCov2_ZeroMax(t *testing.T) {
	text := "some text"
	result := TruncateToTokenLimit(text, 0)
	if result != text {
		t.Fatal("zero max should return original")
	}
}

func TestTruncateToTokenLimitCov2_Truncated(t *testing.T) {
	text := ""
	for i := 0; i < 200; i++ {
		text += "word "
	}
	result := TruncateToTokenLimit(text, 300)
	if len(result) >= len(text) {
		t.Fatal("should be truncated")
	}
}

func TestTruncateToTokenLimitCov2_SmallMax(t *testing.T) {
	text := ""
	for i := 0; i < 200; i++ {
		text += "x "
	}
	result := TruncateToTokenLimit(text, 100)
	if len(result) > 320 {
		t.Fatal("should respect minimum token limit")
	}
}
