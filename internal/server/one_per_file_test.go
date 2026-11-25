package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// newDupSourceServer builds a server whose index has one source with several
// matching chunks and several single-chunk sources, so one_per_file has an
// observable effect.
func newDupSourceServer(t *testing.T) http.Handler {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ollama := newFakeOllama(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = "test-model"
	cfg.OllamaHost = ollama.URL
	emb := embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama,
		Model:    cfg.EmbeddingModel,
		BaseURL:  cfg.OllamaHost,
	})
	s := &Server{config: cfg, embedder: emb, searchers: make(map[string]*gleann.LeannSearcher)}
	t.Cleanup(s.invalidateAllSearchers)
	h := s.buildMux()

	items := []gleann.Item{}
	for i := 0; i < 5; i++ {
		items = append(items, gleann.Item{
			Text:     "alpha zeta keyword matching content variant " + string(rune('a'+i)),
			Metadata: map[string]any{"source": "shared/dup.go"},
		})
	}
	for i := 0; i < 7; i++ {
		items = append(items, gleann.Item{
			Text:     "alpha zeta keyword matching content other " + string(rune('a'+i)),
			Metadata: map[string]any{"source": "uniq/other" + string(rune('0'+i)) + ".go"},
		})
	}
	body, _ := json.Marshal(map[string]any{"items": items})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/dupidx/build", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("build: %d %s", w.Code, w.Body.String())
	}
	return h
}

func doSearchJSON(t *testing.T, h http.Handler, body string) map[string]int {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/dupidx/search", bytes.NewReader([]byte(body))))
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Results []struct {
			Metadata map[string]any `json:"metadata"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode search body: %v (%s)", err, w.Body.String())
	}
	bySource := map[string]int{}
	for _, r := range resp.Results {
		src, _ := r.Metadata["source"].(string)
		bySource[src]++
	}
	return bySource
}

// TestOnePerFile_REST verifies that the REST search honours one_per_file: a
// file with several matching chunks is collapsed to one when true, while the
// default returns every chunk (and so repeats the source).
func TestOnePerFile_REST(t *testing.T) {
	h := newDupSourceServer(t)

	all := doSearchJSON(t, h, `{"query":"alpha zeta keyword matching","top_k":20}`)
	if all["shared/dup.go"] < 2 {
		t.Fatalf("default search should return several chunks of shared/dup.go, got source counts: %v", all)
	}
	total := 0
	for _, n := range all {
		total += n
	}

	one := doSearchJSON(t, h, `{"query":"alpha zeta keyword matching","top_k":20,"one_per_file":true}`)
	if one["shared/dup.go"] != 1 {
		t.Fatalf("one_per_file should return exactly one chunk of shared/dup.go, got: %v", one)
	}
	for src, n := range one {
		if n > 1 {
			t.Fatalf("one_per_file returned %d results for source %q: %v", n, src, one)
		}
	}
	// The collapsed result set must be strictly smaller than the default one.
	oneTotal := 0
	for _, n := range one {
		oneTotal += n
	}
	if oneTotal >= total {
		t.Fatalf("one_per_file result count %d should be below default %d", oneTotal, total)
	}
}
