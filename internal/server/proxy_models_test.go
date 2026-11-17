package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// providerFake answers GET /api/tags (Ollama) and GET /models (OpenAI) and
// records the Authorization header it saw.
type providerFake struct {
	mu     sync.Mutex
	auth   string
	tags   string
	models string
}

func (p *providerFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.auth = r.Header.Get("Authorization")
	p.mu.Unlock()
	switch r.URL.Path {
	case "/api/tags":
		_, _ = w.Write([]byte(p.tags))
	case "/models", "/v1/models":
		_, _ = w.Write([]byte(p.models))
	default:
		http.NotFound(w, r)
	}
}

func proxyModelsReq(s *Server, q url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleProxyModels(w, httptest.NewRequest(http.MethodGet, "/api/proxy/models?"+q.Encode(), nil))
	return w
}

func TestHandleProxyModels_Ollama(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")
	fake := &providerFake{tags: `{"models":[{"name":"llama3"},{"name":"qwen"},{"other":1}]}`}
	ts := httptest.NewServer(fake)
	defer ts.Close()
	s := newProxyTestServer(t)

	w := proxyModelsReq(s, url.Values{"provider": {"ollama"}, "host": {ts.URL + "/"}, "apikey": {"k1"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"llama3","qwen"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if fake.auth != "Bearer k1" {
		t.Errorf("authorization = %q", fake.auth)
	}

	// Not JSON: a clear 500 rather than an empty list.
	fake.tags = "<html>nope</html>"
	if w := proxyModelsReq(s, url.Values{"provider": {"ollama"}, "host": {ts.URL}}); w.Code != 500 ||
		!strings.Contains(w.Body.String(), "failed to decode ollama response") {
		t.Errorf("non-json: %d %s", w.Code, w.Body.String())
	}

	// Unreachable host.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	if w := proxyModelsReq(s, url.Values{"provider": {"ollama"}, "host": {deadURL}}); w.Code != 500 {
		t.Errorf("unreachable: %d %s", w.Code, w.Body.String())
	}
}

func TestHandleProxyModels_OpenAI(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")
	fake := &providerFake{models: `{"data":[{"id":"gpt-a"},{"id":"gpt-b"},{"x":1}]}`}
	ts := httptest.NewServer(fake)
	defer ts.Close()
	s := newProxyTestServer(t)
	s.config.OpenAIBaseURL = ts.URL
	s.config.OpenAIAPIKey = "stored-key"

	// The UI sends the redacted placeholder: the stored key is used for the configured endpoint.
	w := proxyModelsReq(s, url.Values{"provider": {"openai"}, "host": {ts.URL}, "apikey": {gleann.RedactedSecret}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"gpt-a","gpt-b"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if fake.auth != "Bearer stored-key" {
		t.Errorf("stored key not used for the configured endpoint: %q", fake.auth)
	}

	// An explicit key is forwarded as is; no key means no Authorization header.
	proxyModelsReq(s, url.Values{"provider": {"openai"}, "host": {ts.URL}, "apikey": {"explicit"}})
	if fake.auth != "Bearer explicit" {
		t.Errorf("explicit key: %q", fake.auth)
	}

	// The stored key is never sent to another host.
	other := &providerFake{models: `{"data":[]}`}
	ots := httptest.NewServer(other)
	defer ots.Close()
	proxyModelsReq(s, url.Values{"provider": {"openai"}, "host": {ots.URL}, "apikey": {gleann.RedactedSecret}})
	if other.auth != "" {
		t.Errorf("stored key leaked to a foreign host: %q", other.auth)
	}

	fake.models = "garbage"
	if w := proxyModelsReq(s, url.Values{"provider": {"openai"}, "host": {ts.URL}}); w.Code != 500 ||
		!strings.Contains(w.Body.String(), "failed to decode openai response") {
		t.Errorf("non-json: %d %s", w.Code, w.Body.String())
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	if w := proxyModelsReq(s, url.Values{"provider": {"openai"}, "host": {deadURL}}); w.Code != 500 {
		t.Errorf("unreachable: %d", w.Code)
	}
}

func TestHandleProxyModels_UnknownProviderAndBadHost(t *testing.T) {
	s := newProxyTestServer(t)
	if w := proxyModelsReq(s, url.Values{"provider": {"other"}}); w.Code != 200 || !strings.Contains(w.Body.String(), `"models":[]`) {
		t.Errorf("unknown provider: %d %s", w.Code, w.Body.String())
	}
	if w := proxyModelsReq(s, url.Values{"provider": {"ollama"}, "host": {"http://h/path?x=1"}}); w.Code != 400 {
		t.Errorf("host with path/query: %d %s", w.Code, w.Body.String())
	}
}

func TestProxyStoredKey_OnlyForConfiguredHosts(t *testing.T) {
	s := newProxyTestServer(t)
	s.config.OllamaHost = ""
	s.config.OpenAIBaseURL = ""
	s.config.OpenAIAPIKey = "oa-key"
	s.config.OllamaAPIKey = "ol-key"

	tests := []struct {
		provider, host, want string
	}{
		{"openai", "", "oa-key"},
		{"openai", "https://api.openai.com/v1", "oa-key"}, // default endpoint, /v1 ignored
		{"openai", "https://evil.example", ""},
		{"ollama", "", "ol-key"},
		{"ollama", gleann.DefaultOllamaHost, "ol-key"},
		{"ollama", "http://evil.example:11434", ""},
		{"nope", "", ""},
	}
	for _, tc := range tests {
		if got := s.proxyStoredKey(tc.provider, tc.host); got != tc.want {
			t.Errorf("proxyStoredKey(%q,%q) = %q, want %q", tc.provider, tc.host, got, tc.want)
		}
	}
}

func TestSameBaseURLAndConfiguredHost(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"HTTP://Host:1/", "http://host:1", true},
		{"http://host:1/v1", "http://host:1/v1/", true},
		{"http://host:1/a", "http://host:1/b", false},
		{"host:1", "host:1/", true},
		{"http://a", "http://b", false},
	} {
		if got := sameBaseURL(tc.a, tc.b); got != tc.want {
			t.Errorf("sameBaseURL(%q,%q) = %v", tc.a, tc.b, got)
		}
	}

	s := newProxyTestServer(t)
	s.config.OllamaHost = "http://lan-box:11434"
	s.config.OpenAIBaseURL = "http://lan-box:8080/v1"
	for host, want := range map[string]bool{
		"":                             false,
		"http://lan-box:11434":         true,
		"http://lan-box:8080":          true,
		"http://elsewhere:11434":       false,
		"http://localhost:11434":       true,
		gleann.DefaultOllamaHost + "/": true,
	} {
		if got := s.isConfiguredProviderHost(host); got != want {
			t.Errorf("isConfiguredProviderHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestProxyConfiguredClient_RedirectPolicy(t *testing.T) {
	// Redirects that stay on the configured host are followed; others are refused.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer target.Close()
	same := httptest.NewServer(nil)
	defer same.Close()
	same.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/landed", http.StatusFound)
			return
		}
		if r.URL.Path == "/away" {
			http.Redirect(w, r, target.URL, http.StatusFound)
			return
		}
		if r.URL.Path == "/loop" {
			http.Redirect(w, r, "/loop", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("landed"))
	})

	resp, err := proxyConfiguredClient.Get(same.URL + "/hop")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/landed" {
		t.Errorf("same-host redirect not followed: %v", resp.Request.URL)
	}
	if _, err := proxyConfiguredClient.Get(same.URL + "/away"); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Errorf("cross-host redirect: %v", err)
	}
	if _, err := proxyConfiguredClient.Get(same.URL + "/loop"); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Errorf("redirect loop: %v", err)
	}
}
