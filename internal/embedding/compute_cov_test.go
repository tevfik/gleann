package embedding

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

// ── SetPromptTemplate ───────────────────────────────────────────

func TestSetPromptTemplate(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderOllama,
		Model:    "nomic-embed-text",
		BaseURL:  "http://localhost:11434",
	})
	c.SetPromptTemplate("search_query: %s")
	if c.promptTemplate != "search_query: %s" {
		t.Errorf("expected template set, got %q", c.promptTemplate)
	}
}

// ── isConnectionRefused ─────────────────────────────────────────

func TestIsConnectionRefused_StringMatch(t *testing.T) {
	err := fmt.Errorf("dial tcp: connection refused")
	if !isConnectionRefused(err) {
		t.Error("expected true for string containing 'connection refused'")
	}
}

func TestIsConnectionRefused_NoMatch(t *testing.T) {
	err := fmt.Errorf("timeout error")
	if isConnectionRefused(err) {
		t.Error("expected false for non-connection-refused error")
	}
}

func TestIsConnectionRefused_NetOpError(t *testing.T) {
	err := &net.OpError{
		Err: &os.SyscallError{
			Syscall: "connect",
			Err:     errors.New("connection refused"),
		},
	}
	if !isConnectionRefused(err) {
		t.Error("expected true for net.OpError with connection refused")
	}
}

func TestIsConnectionRefused_NetOpErrorOther(t *testing.T) {
	err := &net.OpError{
		Err: &os.SyscallError{
			Syscall: "connect",
			Err:     errors.New("network unreachable"),
		},
	}
	if isConnectionRefused(err) {
		t.Error("expected false for network unreachable")
	}
}

// ── Computer provider dispatch ──────────────────────────────────

func TestCompute_UnsupportedProviderCov(t *testing.T) {
	c := NewComputer(Options{
		Provider: Provider("unsupported"),
		Model:    "test",
	})
	_, err := c.Compute(context.Background(), []string{"hello"})
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestCompute_EmptyCov(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderOllama,
		Model:    "nomic-embed-text",
		BaseURL:  "http://localhost:11434",
	})
	result, err := c.Compute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil input")
	}
}

func TestCompute_EmptyStringsCov(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderOllama,
		Model:    "nomic-embed-text",
		BaseURL:  "http://localhost:11434",
	})
	result, err := c.Compute(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Error("expected empty result for empty input")
	}
}

func TestComputeSingle_UnsupportedProvider(t *testing.T) {
	c := NewComputer(Options{
		Provider: Provider("bad"),
		Model:    "test",
	})
	_, err := c.ComputeSingle(context.Background(), "hello")
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

// ── Batcher RecomputerAdapter ───────────────────────────────────

// Batcher RecomputerAdapter test removed — requires Server which needs real embedding backend

// mockCovComputer is a minimal mock for testing.
type mockCovComputer struct {
	dims       int
	embeddings [][]float32
}

func (m *mockCovComputer) Compute(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		if i < len(m.embeddings) {
			result[i] = m.embeddings[i]
		} else {
			result[i] = make([]float32, m.dims)
		}
	}
	return result, nil
}

func (m *mockCovComputer) ComputeSingle(_ context.Context, text string) ([]float32, error) {
	if len(m.embeddings) > 0 {
		return m.embeddings[0], nil
	}
	return make([]float32, m.dims), nil
}

func (m *mockCovComputer) Dimensions() int   { return m.dims }
func (m *mockCovComputer) ModelName() string { return "mock-cov-model" }

// ── TruncateToTokenLimit additional ─────────────────────────────

func TestTruncateToTokenLimit_LongText(t *testing.T) {
	// Long text that exceeds typical model limit.
	longText := strings.Repeat("hello world ", 10000)
	truncated := TruncateToTokenLimit(longText, 2048)
	if len(truncated) >= len(longText) {
		t.Error("expected truncation for very long text")
	}
}

func TestTruncateToTokenLimit_ShortText(t *testing.T) {
	text := "short text"
	truncated := TruncateToTokenLimit(text, 2048)
	if truncated != text {
		t.Error("short text should not be truncated")
	}
}

func TestTruncateToTokenLimit_EmptyTextCov(t *testing.T) {
	truncated := TruncateToTokenLimit("", 2048)
	if truncated != "" {
		t.Error("empty text should remain empty")
	}
}

func TestGetModelTokenLimit_Known(t *testing.T) {
	limit := GetModelTokenLimit("nomic-embed-text")
	if limit <= 0 {
		t.Error("expected positive limit for known model")
	}
}

func TestGetModelTokenLimit_Unknown(t *testing.T) {
	limit := GetModelTokenLimit("unknown-model-xyz")
	if limit <= 0 {
		t.Error("expected positive default limit for unknown model")
	}
}

// ── NewComputer defaults ────────────────────────────────────────

func TestNewComputer_GeminiProvider(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderGemini,
		Model:    "text-embedding-004",
		APIKey:   "test-key",
	})
	if c.ModelName() != "text-embedding-004" {
		t.Errorf("expected model name, got %s", c.ModelName())
	}
}

func TestNewComputer_OpenAIProvider(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderOpenAI,
		Model:    "text-embedding-3-small",
		APIKey:   "test-key",
	})
	if c.provider != ProviderOpenAI {
		t.Errorf("expected openai provider, got %s", c.provider)
	}
}

func TestNewComputer_LlamaCPPProvider(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderLlamaCPP,
		Model:    "test",
		BaseURL:  "http://localhost:8080",
	})
	if c.provider != ProviderLlamaCPP {
		t.Errorf("expected llamacpp provider, got %s", c.provider)
	}
}

// ── Gemini/OpenAI provider branch coverage ────────────────────

func TestNewComputer_GeminiDefaults(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-gemini-key")
	t.Setenv("GOOGLE_API_KEY", "")
	c := NewComputer(Options{
		Provider: ProviderGemini,
		Model:    "text-embedding-004",
	})
	if c.provider != ProviderGemini {
		t.Errorf("expected Gemini provider, got: %s", c.provider)
	}
	if c.baseURL != "https://generativelanguage.googleapis.com" {
		t.Errorf("expected Gemini URL, got: %s", c.baseURL)
	}
	if c.apiKey != "test-gemini-key" {
		t.Errorf("expected test-gemini-key, got: %s", c.apiKey)
	}
	if c.batchSize != 100 {
		t.Errorf("expected batchSize=100 for external API, got: %d", c.batchSize)
	}
	if c.concurrency != 20 {
		t.Errorf("expected concurrency=20, got: %d", c.concurrency)
	}
}

func TestNewComputer_GeminiGoogleKeyFallback(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "google-fallback")
	c := NewComputer(Options{
		Provider: ProviderGemini,
		Model:    "text-embedding-004",
	})
	if c.apiKey != "google-fallback" {
		t.Errorf("expected GOOGLE_API_KEY fallback, got: %s", c.apiKey)
	}
}

func TestNewComputer_LlamaCPPCrossContamination(t *testing.T) {
	c := NewComputer(Options{
		Provider: ProviderLlamaCPP,
		Model:    "bge-m3",
		BaseURL:  "http://localhost:11434",
	})
	if c.baseURL == "http://localhost:11434" {
		t.Error("expected LlamaCPP to override Ollama host")
	}
}

func TestNewComputer_OpenAIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-key")
	c := NewComputer(Options{
		Provider: ProviderOpenAI,
		Model:    "text-embedding-3-small",
	})
	if c.apiKey != "sk-test-key" {
		t.Errorf("expected OpenAI key from env, got: %s", c.apiKey)
	}
	if c.batchSize != 100 {
		t.Errorf("expected batchSize=100, got: %d", c.batchSize)
	}
}
