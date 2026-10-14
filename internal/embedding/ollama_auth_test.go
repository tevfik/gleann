package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newEmbedAuthServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	last := "<no request>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.Header.Get("Authorization")
		mu.Unlock()
		var req ollamaEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := 1
		if arr, ok := req.Input.([]interface{}); ok {
			n = len(arr)
		}
		embs := make([][]float32, n)
		for i := range embs {
			embs[i] = []float32{0.1, 0.2}
		}
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embs})
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

func TestComputeOllamaBearerFromConfigKey(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	srv, last := newEmbedAuthServer(t)
	cfg := gleann.Config{OllamaAPIKey: "ollama-k", OpenAIAPIKey: "sk-openai"}

	c := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  srv.URL,
		Model:    "m",
		APIKey:   cfg.APIKeyForProvider("ollama"),
	})
	if _, err := c.Compute(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got := last(); got != "Bearer ollama-k" {
		t.Errorf("Authorization = %q, want Bearer ollama-k (never the OpenAI key)", got)
	}
}

func TestComputeOllamaBearerFromEnv(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "env-ollama")
	t.Setenv("OPENAI_API_KEY", "sk-env-openai")
	srv, last := newEmbedAuthServer(t)

	c := NewComputer(Options{Provider: ProviderOllama, BaseURL: srv.URL, Model: "m"})
	if _, err := c.Compute(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if got := last(); got != "Bearer env-ollama" {
		t.Errorf("Authorization = %q, want Bearer env-ollama", got)
	}

	// The OpenAI provider keeps using OPENAI_API_KEY, not OLLAMA_API_KEY.
	oc := NewComputer(Options{Provider: ProviderOpenAI, BaseURL: "https://example.invalid", Model: "m"})
	if oc.apiKey != "sk-env-openai" {
		t.Errorf("openai apiKey = %q, want OPENAI_API_KEY value", oc.apiKey)
	}
}

func TestComputeOllamaNoKeyNoHeader(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	srv, last := newEmbedAuthServer(t)
	c := NewComputer(Options{Provider: ProviderOllama, BaseURL: srv.URL, Model: "m"})
	if _, err := c.Compute(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if got := last(); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
}
