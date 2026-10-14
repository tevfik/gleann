package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newTagsAuthServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	last := "<no request>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"models":[{"name":"bge-m3:latest"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

func proxyModels(t *testing.T, s *Server, provider, host, apikey string) int {
	t.Helper()
	q := url.Values{}
	q.Set("provider", provider)
	q.Set("host", host)
	q.Set("apikey", apikey)
	req := httptest.NewRequest(http.MethodGet, "/api/proxy/models?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	s.handleProxyModels(rec, req)
	return rec.Code
}

func TestProxyModels_OllamaPassesBearer(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	srv, last := newTagsAuthServer(t)
	s := &Server{config: gleann.Config{
		OllamaHost:   srv.URL,
		OllamaAPIKey: "stored-ollama",
		OpenAIAPIKey: "sk-openai",
	}}

	// Explicit key from the UI.
	if code := proxyModels(t, s, "ollama", srv.URL, "ui-key"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := last(); got != "Bearer ui-key" {
		t.Errorf("Authorization = %q, want Bearer ui-key", got)
	}

	// Redacted sentinel (round-tripped from GET /api/config) → stored key.
	if code := proxyModels(t, s, "ollama", srv.URL, "********"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := last(); got != "Bearer stored-ollama" {
		t.Errorf("Authorization = %q, want Bearer stored-ollama", got)
	}

	// Empty key → stored Ollama key, never the OpenAI key.
	if code := proxyModels(t, s, "ollama", srv.URL+"/", ""); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := last(); got != "Bearer stored-ollama" {
		t.Errorf("Authorization = %q, want Bearer stored-ollama", got)
	}
}

func TestProxyModels_StoredKeyNotSentToOtherHost(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	srv, last := newTagsAuthServer(t)
	s := &Server{config: gleann.Config{
		OllamaHost:   "http://configured-host:11434",
		OllamaAPIKey: "stored-ollama",
	}}
	if code := proxyModels(t, s, "ollama", srv.URL, "********"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := last(); got != "" {
		t.Errorf("stored key leaked to unconfigured host: Authorization = %q", got)
	}
}

func TestProxyModels_OllamaNoKey(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	srv, last := newTagsAuthServer(t)
	s := &Server{config: gleann.Config{OllamaHost: srv.URL, OpenAIAPIKey: "sk-openai"}}
	if code := proxyModels(t, s, "ollama", srv.URL, ""); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := last(); got != "" {
		t.Errorf("Authorization = %q, want none (OpenAI key must not be used)", got)
	}
}

func TestProxyLLMConfig_OllamaKey(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	s := &Server{config: gleann.Config{
		LLMProvider:  "ollama",
		OllamaHost:   "http://localhost:11434",
		OllamaAPIKey: "ok",
		OpenAIAPIKey: "sk-openai",
	}}
	if got := s.proxyLLMConfig().APIKey; got != "ok" {
		t.Errorf("APIKey = %q, want ok", got)
	}
	s.config.LLMProvider = ""
	s.config.OpenAIAPIKey = ""
	if got := s.proxyLLMConfig().APIKey; got != "ok" {
		t.Errorf("default-provider APIKey = %q, want ok", got)
	}
}
