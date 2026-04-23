package autosetup

import (
	"strings"
	"testing"
)

func TestFormatDetectedConfig_Full(t *testing.T) {
	dc := DetectedConfig{
		OllamaRunning:  true,
		OllamaHost:     "http://localhost:11434",
		EmbeddingModel:  "bge-m3",
		LLMModel:       "llama3:8b",
		RerankModel:    "bge-reranker-v2-m3",
		IndexDir:       "~/.gleann/indexes",
		MCPEnabled:     true,
		ServerEnabled:  true,
	}
	out := FormatDetectedConfig(dc)
	if !strings.Contains(out, "✓ running") {
		t.Error("expected running status")
	}
	if !strings.Contains(out, "bge-m3") {
		t.Error("expected embedding model")
	}
	if !strings.Contains(out, "llama3:8b") {
		t.Error("expected LLM model")
	}
	if !strings.Contains(out, "bge-reranker-v2-m3") {
		t.Error("expected reranker model")
	}
	if !strings.Contains(out, "enabled") {
		t.Error("expected enabled MCP/REST")
	}
}

func TestFormatDetectedConfig_NotRunning(t *testing.T) {
	dc := DetectedConfig{
		OllamaRunning:  false,
		OllamaHost:     "http://localhost:11434",
		EmbeddingModel:  "nomic-embed-text",
		LLMModel:       "gemma2:2b",
		IndexDir:       "~/.gleann/indexes",
	}
	out := FormatDetectedConfig(dc)
	if !strings.Contains(out, "✗ not found") {
		t.Error("expected 'not found' status")
	}
	if !strings.Contains(out, "(none)") {
		t.Error("expected (none) for reranker")
	}
	if !strings.Contains(out, "disabled") {
		t.Error("expected disabled for MCP/REST")
	}
}

func TestFormatDetectedConfig_Structure(t *testing.T) {
	dc := DetectedConfig{
		OllamaHost:    "http://custom:9999",
		EmbeddingModel: "test-embed",
		LLMModel:      "test-llm",
		IndexDir:      "/custom/path",
	}
	out := FormatDetectedConfig(dc)
	// Should have box borders
	if !strings.Contains(out, "┌") || !strings.Contains(out, "┘") {
		t.Error("expected box borders")
	}
	if !strings.Contains(out, "Accept & continue") {
		t.Error("expected prompt instructions")
	}
}
