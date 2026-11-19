package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
)

func ptrF32(v float32) *float32 { return &v }

// countingEmbedder points the server's embedder at a fake that counts
// embedding requests (any request is an embedding request).
func countingEmbedder(t *testing.T, s *Server) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	inner := newFakeOllama(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		r2 := r.Clone(r.Context())
		r2.RequestURI = ""
		r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(inner.URL, "http://")
		resp, err := http.DefaultTransport.RoundTrip(r2)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		b, _ := io.ReadAll(resp.Body)
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	s.cfgMu.Lock()
	s.embedder = embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama,
		Model:    s.config.EmbeddingModel,
		BaseURL:  srv.URL,
	})
	s.config.OllamaHost = srv.URL
	s.cfgMu.Unlock()
	s.invalidateAllSearchers()
	return &n
}

// "hybrid_alpha": 0 asks for a lexical-only search. It used to be dropped
// (omitempty float32 and a > 0 check), so the configured weight applied and
// the query was embedded.
func TestSearchHybridAlphaZeroIsLexicalOnly(t *testing.T) {
	s, h, _ := newIndexedServer(t)
	embeds := countingEmbedder(t, s)

	for _, path := range []string{"/api/indexes/idx/search", "/api/search"} {
		embeds.Store(0)
		body := map[string]any{"query": "alpha zero " + path, "top_k": 3, "hybrid_alpha": 0}
		if path == "/api/search" {
			body["indexes"] = []string{"idx"}
		}
		w := postObj(h, path, body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", path, w.Code, w.Body.String())
		}
		if n := embeds.Load(); n != 0 {
			t.Errorf("%s with hybrid_alpha 0 embedded the query (%d requests)", path, n)
		}

		// Without the field the configured weight applies and the query is
		// embedded.
		delete(body, "hybrid_alpha")
		body["query"] = "alpha default " + path
		if w := postObj(h, path, body); w.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", path, w.Code, w.Body.String())
		}
		if embeds.Load() == 0 {
			t.Errorf("%s without hybrid_alpha did not embed the query", path)
		}
	}
}

func TestSearchHybridAlphaOutOfRangeIs400(t *testing.T) {
	_, h, _ := newIndexedServer(t)
	for _, path := range []string{"/api/indexes/idx/search", "/api/search"} {
		for _, a := range []float64{-0.1, 1.5} {
			w := postObj(h, path, map[string]any{"query": "alpha", "hybrid_alpha": a})
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "hybrid_alpha must be between 0 and 1") {
				t.Errorf("%s alpha %v: status %d %s", path, a, w.Code, w.Body.String())
			}
		}
	}
	if err := validHybridAlpha(nil); err != nil {
		t.Errorf("absent alpha: %v", err)
	}
	if err := validHybridAlpha(ptrF32(0)); err != nil {
		t.Errorf("alpha 0: %v", err)
	}
}
