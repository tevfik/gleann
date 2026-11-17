package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func proxyModelsBody(t *testing.T, s *Server, provider, host string) (int, string) {
	t.Helper()
	q := url.Values{"provider": {provider}, "host": {host}}
	rec := httptest.NewRecorder()
	s.handleProxyModels(rec, httptest.NewRequest(http.MethodGet, "/api/proxy/models?"+q.Encode(), nil))
	return rec.Code, rec.Body.String()
}

func TestProxyModels_ConfiguredLocalProviderReachable(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"m1"}]}`))
	}))
	defer srv.Close()
	s := &Server{config: gleann.Config{OllamaHost: srv.URL}}
	code, body := proxyModelsBody(t, s, "ollama", srv.URL)
	if code != http.StatusOK || !strings.Contains(body, "m1") {
		t.Fatalf("configured loopback provider must stay reachable: %d %s", code, body)
	}
}

func TestProxyModels_ClientSuppliedPrivateHostRefused(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"models":[{"name":"secret"}]}`))
	}))
	defer srv.Close()
	s := &Server{config: gleann.Config{OllamaHost: "http://127.0.0.1:1"}}
	code, body := proxyModelsBody(t, s, "ollama", srv.URL)
	if code == http.StatusOK || hit || !strings.Contains(body, "restricted") {
		t.Fatalf("client-supplied loopback host must be refused: %d %s hit=%v", code, body, hit)
	}
	// The override env var restores the old behaviour.
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")
	if code, _ := proxyModelsBody(t, s, "ollama", srv.URL); code != http.StatusOK {
		t.Fatalf("allow-private override ignored: %d", code)
	}
}

func TestProxyModels_RedirectToPrivateRefused(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"models":[{"name":"secret"}]}`))
	}))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/tags", http.StatusFound)
	}))
	defer redir.Close()

	// Configured provider that redirects to another host: refused.
	s := &Server{config: gleann.Config{OllamaHost: redir.URL}}
	if code, _ := proxyModelsBody(t, s, "ollama", redir.URL); code == http.StatusOK || hit {
		t.Fatalf("redirect off the configured host must be refused: %d hit=%v", code, hit)
	}
	// Unconfigured host: refused at the first dial already.
	s = &Server{config: gleann.Config{OllamaHost: "http://127.0.0.1:1"}}
	if code, _ := proxyModelsBody(t, s, "ollama", redir.URL); code == http.StatusOK || hit {
		t.Fatalf("guarded client followed a redirect to a private host: %d hit=%v", code, hit)
	}
}
