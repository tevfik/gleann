package multimodal

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestMultimodalOllamaBearer(t *testing.T) {
	var mu sync.Mutex
	last := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.Header.Get("Authorization")
		mu.Unlock()
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:latest"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"details":{"families":["gemma","clip"]}}`))
		}
	}))
	defer srv.Close()
	get := func() string { mu.Lock(); defer mu.Unlock(); return last }

	t.Setenv("OLLAMA_API_KEY", "")
	if caps := DetectCapabilities(srv.URL, "gemma4", "k1"); !caps.Vision {
		t.Error("expected vision capability")
	}
	if got := get(); got != "Bearer k1" {
		t.Errorf("show Authorization = %q", got)
	}
	AutoDetectModel(srv.URL, "k2")
	if got := get(); got != "Bearer k2" {
		t.Errorf("tags Authorization = %q", got)
	}

	// Env fallback, and Processor picks up the env key by default.
	t.Setenv("OLLAMA_API_KEY", "env-k")
	DetectCapabilities(srv.URL, "gemma4")
	if got := get(); got != "Bearer env-k" {
		t.Errorf("env Authorization = %q", got)
	}
	if p := NewProcessor(srv.URL, "gemma4"); p.APIKey != "env-k" {
		t.Errorf("Processor.APIKey = %q, want env-k", p.APIKey)
	}
}
