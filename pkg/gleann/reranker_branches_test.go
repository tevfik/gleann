package gleann

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rerankProviders lists the HTTP rerankers; label is the prefix of their errors.
var rerankProviders = []struct {
	name     string
	provider RerankerProvider
	label    string
}{
	{"jina", RerankerJina, "jina"},
	{"llamacpp", RerankerLlamacpp, "jina"},
	{"cohere", RerankerCohere, "cohere"},
	{"voyage", RerankerVoyage, "voyage"},
}

func fxResults() []SearchResult {
	return []SearchResult{{ID: 1, Text: "a"}, {ID: 2, Text: "b"}}
}

func TestRerankHTTPFailures(t *testing.T) {
	// A closed listener gives a connection error; a bad BaseURL fails request
	// construction; a non-JSON body fails decoding; a truncated body on the
	// error path is reported as unreadable.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer garbage.Close()

	// Announces a long body, sends a little and drops the connection.
	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("partial"))
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	defer truncated.Close()

	for _, p := range rerankProviders {
		t.Run(p.name, func(t *testing.T) {
			cases := []struct {
				name, baseURL, want string
			}{
				{"connection refused", deadURL, p.label + " request"},
				{"invalid url", "http://bad\x7fhost", "invalid control character"},
				{"bad json", garbage.URL, "decode " + p.label + " response"},
				{"unreadable error body", truncated.URL, "(body unreadable)"},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					r := NewReranker(RerankerConfig{Provider: p.provider, Model: "m", BaseURL: c.baseURL, APIKey: "k"})
					_, err := r.Rerank(context.Background(), "q", fxResults(), 2)
					if err == nil || !strings.Contains(err.Error(), c.want) {
						t.Fatalf("err = %v, want it to contain %q", err, c.want)
					}
				})
			}
		})
	}
}

func TestRerankTruncatedPropagatesError(t *testing.T) {
	// With MaxDocChars set the truncated pass must surface the provider error.
	r := NewReranker(RerankerConfig{Provider: RerankerOllama, MaxDocChars: 3})
	_, err := r.Rerank(context.Background(), "q", fxResults(), 2)
	if err == nil || !strings.Contains(err.Error(), "ollama has no rerank API") {
		t.Fatalf("err = %v", err)
	}
}

func TestRerankTruncatedDuplicateIDsScoresFullTexts(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jinaRerankRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = req.Documents
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}]}`))
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{Provider: RerankerJina, BaseURL: srv.URL, MaxDocChars: 2})
	in := []SearchResult{{ID: 7, Text: "first document"}, {ID: 7, Text: "second document"}}
	out, err := r.Rerank(context.Background(), "q", in, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "first document" || seen[1] != "second document" {
		t.Errorf("colliding IDs must send untruncated texts, got %q", seen)
	}
	if out[0].Text != "second document" {
		t.Errorf("order = %+v", out)
	}
}

func TestRerankerRegistrationAndPool(t *testing.T) {
	oldRank, oldCheck := eifRank, eifRerankModelInstalled
	t.Cleanup(func() { eifRank, eifRerankModelInstalled = oldRank, oldCheck })

	RegisterEIFRerankModelCheck(func(m string) bool { return m == "x" })
	if eifRerankModelInstalled == nil || !eifRerankModelInstalled("x") || eifRerankModelInstalled("y") {
		t.Error("model check was not installed")
	}
	if got := NewReranker(RerankerConfig{Provider: RerankerJina, Pool: 25}).PreferredPool(); got != 25 {
		t.Errorf("PreferredPool = %d", got)
	}
}

func TestNewRerankerEnvDefaults(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://ollama.test:1")
	t.Setenv(OllamaAPIKeyEnv, "ok")
	t.Setenv("JINA_API_KEY", "jk")
	t.Setenv("COHERE_API_KEY", "")
	t.Setenv("CO_API_KEY", "cok")
	t.Setenv("VOYAGE_API_KEY", "vk")

	cases := []struct {
		p       RerankerProvider
		baseURL string
		key     string
	}{
		{RerankerOllama, "http://ollama.test:1", "ok"},
		{RerankerLlamacpp, DefaultLlamaCPPHost, ""},
		{RerankerJina, "https://api.jina.ai", "jk"},
		{RerankerCohere, "https://api.cohere.com", "cok"},
		{RerankerVoyage, "https://api.voyageai.com", "vk"},
	}
	for _, c := range cases {
		r := NewReranker(RerankerConfig{Provider: c.p})
		if r.config.BaseURL != c.baseURL || r.config.APIKey != c.key {
			t.Errorf("%s: base=%q key=%q, want %q %q", c.p, r.config.BaseURL, r.config.APIKey, c.baseURL, c.key)
		}
	}

	t.Setenv("OLLAMA_HOST", "")
	if got := NewReranker(RerankerConfig{Provider: RerankerOllama}).config.BaseURL; got != DefaultOllamaHost {
		t.Errorf("ollama default host = %q", got)
	}
}

func TestMostlyCode(t *testing.T) {
	meta := func(key, v string) SearchResult { return SearchResult{Metadata: map[string]any{key: v}} }
	cases := []struct {
		name string
		in   []SearchResult
		want bool
	}{
		{"empty", nil, false},
		{"source key", []SearchResult{meta("source", "a.go")}, true},
		{"file key fallback", []SearchResult{meta("file", "B.PY")}, true},
		{"no metadata", []SearchResult{{}}, false},
		{"exactly half", []SearchResult{meta("source", "a.go"), meta("source", "n.md")}, true},
		{"below half", []SearchResult{meta("source", "a.go"), meta("source", "n.md"), meta("source", "m.md")}, false},
	}
	for _, c := range cases {
		if got := mostlyCode(c.in); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
