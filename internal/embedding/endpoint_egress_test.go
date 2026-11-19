package embedding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Without an endpoint the openai embedder fails before sending anything; it
// used to embed the whole corpus through api.openai.com.
func TestOpenAIEmbeddingWithoutEndpointSendsNothing(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	c := NewComputer(Options{Provider: ProviderOpenAI, Model: "text-embedding-3-small", APIKey: "sk"})
	if c.baseURL != "" {
		t.Fatalf("baseURL = %q, want empty (no implicit api.openai.com)", c.baseURL)
	}
	if _, err := c.Compute(context.Background(), []string{"private document"}); !errors.Is(err, gleann.ErrOpenAIEndpointUnset) {
		t.Fatalf("Compute err = %v, want ErrOpenAIEndpointUnset", err)
	}
}

// An OpenAI-compatible URL on port 11434 is Ollama's own /v1 API. It used to
// be rewritten to api.openai.com, sending the corpus to OpenAI's cloud.
func TestOpenAIEmbeddingKeepsAnOllamaV1Endpoint(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	for _, u := range []string{"http://localhost:11434/v1", "http://localhost:11434"} {
		c := NewComputer(Options{Provider: ProviderOpenAI, Model: "nomic-embed-text", BaseURL: u})
		if c.baseURL != "http://localhost:11434" {
			t.Errorf("BaseURL %q became %q", u, c.baseURL)
		}
	}
}

// The configured server gets the request at /v1/embeddings, also when the
// URL is given in the documented ".../v1" form.
func TestOpenAIEmbeddingUsesTheConfiguredServer(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3],"index":0}]}`))
	}))
	defer srv.Close()
	c := NewComputer(Options{Provider: ProviderOpenAI, Model: "m", BaseURL: srv.URL + "/v1", APIKey: "sk"})
	if _, err := c.Compute(context.Background(), []string{"doc"}); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/embeddings" || auth != "Bearer sk" {
		t.Errorf("request = %s auth %q", path, auth)
	}
}
