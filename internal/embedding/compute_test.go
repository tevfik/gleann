package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func TestNewComputerDefaults(t *testing.T) {
	c := NewComputer(Options{})
	if c.provider != ProviderOllama {
		t.Errorf("expected ollama provider, got %s", c.provider)
	}
	if c.model != "bge-m3" {
		t.Errorf("expected bge-m3 model, got %s", c.model)
	}
	// When Options{} is passed, Provider is empty when BatchSize is evaluated,
	// so the non-Ollama default (100) is used. Provider is set to Ollama after.
	if c.batchSize != 100 {
		t.Errorf("expected batch size 100, got %d", c.batchSize)
	}
}

func TestNewComputerCustom(t *testing.T) {
	c := NewComputer(Options{
		Provider:  ProviderOpenAI,
		Model:     "text-embedding-3-small",
		BaseURL:   "https://custom.api.com",
		APIKey:    "test-key",
		BatchSize: 16,
	})
	if c.provider != ProviderOpenAI {
		t.Errorf("expected openai provider, got %s", c.provider)
	}
	if c.model != "text-embedding-3-small" {
		t.Errorf("expected text-embedding-3-small, got %s", c.model)
	}
	if c.batchSize != 16 {
		t.Errorf("expected batch size 16, got %d", c.batchSize)
	}
}

func TestComputeEmpty(t *testing.T) {
	c := NewComputer(Options{})
	result, err := c.Compute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for empty input")
	}
}

func TestComputeOllama(t *testing.T) {
	// Mock Ollama server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req ollamaEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Return fake embeddings.
		var texts []string
		switch v := req.Input.(type) {
		case []interface{}:
			for _, s := range v {
				texts = append(texts, s.(string))
			}
		case string:
			texts = []string{v}
		}

		embeddings := make([][]float32, len(texts))
		for i := range texts {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}

		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: embeddings,
		})
	}))
	defer srv.Close()

	c := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  srv.URL,
		Model:    "test-model",
	})

	results, err := c.Compute(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(results))
	}
	if len(results[0]) != 4 {
		t.Errorf("expected 4 dimensions, got %d", len(results[0]))
	}
	if c.Dimensions() != 4 {
		t.Errorf("expected dimensions=4, got %d", c.Dimensions())
	}
}

func TestComputeOpenAI(t *testing.T) {
	// Mock OpenAI server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-api-key" {
			t.Errorf("unexpected auth header: %s", auth)
		}

		var req openAIEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)

		resp := openAIEmbedResponse{}
		for i := range req.Input {
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				Embedding: []float32{0.5, 0.6, 0.7},
				Index:     i,
			})
		}
		resp.Usage.PromptTokens = 10
		resp.Usage.TotalTokens = 10

		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewComputer(Options{
		Provider: ProviderOpenAI,
		BaseURL:  srv.URL,
		Model:    "text-embedding-3-small",
		APIKey:   "test-api-key",
	})

	results, err := c.Compute(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("expected 3 dimensions, got %d", len(results[0]))
	}
}

func TestComputeSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		})
	}))
	defer srv.Close()

	c := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  srv.URL,
	})

	emb, err := c.ComputeSingle(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(emb) != 3 {
		t.Errorf("expected 3 dimensions, got %d", len(emb))
	}
}

func TestComputeBatching(t *testing.T) {
	var mu sync.Mutex
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		var req ollamaEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)

		var count int
		switch v := req.Input.(type) {
		case []interface{}:
			count = len(v)
		default:
			count = 1
		}

		embeddings := make([][]float32, count)
		for i := range embeddings {
			embeddings[i] = []float32{0.1, 0.2}
		}
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
	defer srv.Close()

	c := NewComputer(Options{
		Provider:  ProviderOllama,
		BaseURL:   srv.URL,
		BatchSize: 2,
	})

	// 5 texts with batch size 2 should make 3 calls.
	texts := []string{"a", "b", "c", "d", "e"}
	results, err := c.Compute(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 embeddings, got %d", len(results))
	}
	mu.Lock()
	got := callCount
	mu.Unlock()
	if got != 3 {
		t.Errorf("expected 3 API calls, got %d", got)
	}
}

func TestComputeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  srv.URL,
	})

	_, err := c.Compute(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error on server error")
	}
}

func TestComputeUnsupportedProvider(t *testing.T) {
	c := NewComputer(Options{
		Provider: "invalid",
		BaseURL:  "http://localhost:1234",
	})

	_, err := c.Compute(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestModelName(t *testing.T) {
	c := NewComputer(Options{Model: "custom-model"})
	if c.ModelName() != "custom-model" {
		t.Errorf("expected custom-model, got %s", c.ModelName())
	}
}

func TestResolveOllamaHost(t *testing.T) {
	// Default.
	host := resolveOllamaHost()
	if host != "http://localhost:11434" {
		t.Errorf("expected default host, got %s", host)
	}

	// From env.
	os.Setenv("OLLAMA_HOST", "http://custom:1234")
	defer os.Unsetenv("OLLAMA_HOST")
	host = resolveOllamaHost()
	if host != "http://custom:1234" {
		t.Errorf("expected custom host, got %s", host)
	}
}

func TestResolveOpenAIBaseURL(t *testing.T) {
	// Default.
	url := resolveOpenAIBaseURL()
	if url != "https://api.openai.com" {
		t.Errorf("expected default URL, got %s", url)
	}

	// From env.
	os.Setenv("OPENAI_BASE_URL", "https://custom.openai.com")
	defer os.Unsetenv("OPENAI_BASE_URL")
	url = resolveOpenAIBaseURL()
	if url != "https://custom.openai.com" {
		t.Errorf("expected custom URL, got %s", url)
	}
}
