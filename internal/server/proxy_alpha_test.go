package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// buildFormat2DocsIndex writes files into a temp docs dir and builds them, in
// process, as a format-2 index called name on s.
func buildFormat2DocsIndex(t *testing.T, s *Server, name string, files map[string]string) {
	t.Helper()
	docs := t.TempDir()
	for f, text := range files {
		if err := os.WriteFile(filepath.Join(docs, f), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.runBuild(context.Background(), s.buildOptions(name, docs)); err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	if got := gleann.StoreFormatOf(filepath.Join(s.cfg().IndexDir, name, name)); got != 2 {
		t.Fatalf("index %s is format %d, want 2", name, got)
	}
}

// passthroughSources runs a pass-through chat request and returns the decoded X-Gleann-Sources entries.
func passthroughSources(t *testing.T, s *Server, model, question string, hdr map[string]string) []proxySource {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"`+question+`"}]}`))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	raw, err := url.QueryUnescape(w.Header().Get("X-Gleann-Sources"))
	if err != nil {
		t.Fatal(err)
	}
	var out []proxySource
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("X-Gleann-Sources %q: %v", raw, err)
	}
	return out
}

func TestHybridAlphaOption(t *testing.T) {
	cases := []struct {
		header string
		ok     bool
		want   float32
	}{
		{"", false, 0},
		{"0", true, 0},
		{"0.0", true, 0},
		{"0.25", true, 0.25},
		{" 1 ", true, 1},
		{"-0.1", false, 0},
		{"1.5", false, 0},
		{"NaN", false, 0},
		{"Inf", false, 0},
		{"banana", false, 0},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if c.header != "" {
			r.Header.Set("X-Gleann-Hybrid-Alpha", c.header)
		}
		o, ok := hybridAlphaOption(r)
		if ok != c.ok {
			t.Errorf("%q: ok = %v, want %v", c.header, ok, c.ok)
			continue
		}
		if ok {
			var cfg gleann.SearchConfig
			cfg.HybridAlpha = 0.7
			o(&cfg)
			if cfg.HybridAlpha != c.want {
				t.Errorf("%q: alpha %v, want %v", c.header, cfg.HybridAlpha, c.want)
			}
		}
	}
}

// Alpha 0 ranks several indexes by BM25 alone: results that carry no vector distance still merge (the
// cross-index ranking uses their lexical scores), and the best lexical score wins whichever index it is in.
func TestMergeProxyHitsAlphaZeroUsesBM25(t *testing.T) {
	f := func(v float32) *float32 { return &v }
	per := map[string][]gleann.SearchResult{
		"a": {{ID: 1, Score: 1.0, BM25Score: f(2.0)}, {ID: 2, Score: 0.5, BM25Score: f(1.0)}},
		"b": {{ID: 3, Score: 1.0, BM25Score: f(8.0)}, {ID: 4, Score: 0.1, BM25Score: f(0.5)}},
	}
	got := mergeProxyHits(per, []string{"a", "b"}, 3, 0)
	wantIDs := []int64{3, 1, 2}
	for i, id := range wantIDs {
		if got[i].result.ID != id {
			t.Fatalf("hit %d is %d, want %d (all: %+v)", i, got[i].result.ID, id, got)
		}
	}
	// Without raw lexical scores the own scores decide, as before.
	plain := map[string][]gleann.SearchResult{"a": {{ID: 1, Score: 0.2}}, "b": {{ID: 2, Score: 0.9}}}
	if g := mergeProxyHits(plain, []string{"a", "b"}, 2, 0); g[0].result.ID != 2 {
		t.Errorf("fallback order wrong: %+v", g)
	}
}

// With format-2 indexes, alpha 0 retrieves only the passages that match the query words, where the hybrid
// default also fills the context with vector neighbours; it works in pass-through mode with several indexes.
func TestPassthroughHybridAlphaZeroIsLexicalOnly(t *testing.T) {
	up := &fakeLLM{status: http.StatusOK, body: "data: {}\n\n"}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	s, _, _ := newBuildServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	s.config.OpenAIBaseURL = ts.URL + "/v1"
	s.config.LLMModel = "llm"

	buildFormat2DocsIndex(t, s, "animals", map[string]string{
		"zebra.md":   "# Zebra\n\nThe zebra has black and white stripes.\n",
		"giraffe.md": "# Giraffe\n\nThe giraffe has a very long neck.\n",
	})
	buildFormat2DocsIndex(t, s, "plants", map[string]string{
		"fern.md": "# Fern\n\nA fern reproduces with spores.\n",
		"oak.md":  "# Oak\n\nThe oak drops acorns in autumn.\n",
	})

	lex := passthroughSources(t, s, "gleann/animals,plants", "zebra", map[string]string{"X-Gleann-Hybrid-Alpha": "0"})
	if len(lex) != 1 || lex[0].Index != "animals" || !strings.Contains(lex[0].Source, "zebra.md") {
		t.Fatalf("alpha 0 sources = %+v, want only the zebra passage of animals", lex)
	}
	hyb := passthroughSources(t, s, "gleann/animals,plants", "zebra", nil)
	if len(hyb) < 2 {
		t.Fatalf("default hybrid sources = %+v, want vector neighbours as well", hyb)
	}
	// An invalid value is ignored: same as no header.
	bad := passthroughSources(t, s, "gleann/animals,plants", "zebra", map[string]string{"X-Gleann-Hybrid-Alpha": "7"})
	if len(bad) != len(hyb) {
		t.Fatalf("invalid alpha changed the result: %d sources, want %d", len(bad), len(hyb))
	}
}

// The default proxy mode takes the header too.
func TestDefaultProxyHybridAlphaZero(t *testing.T) {
	s, _, _ := newBuildServer(t)
	buildFormat2DocsIndex(t, s, "animals", map[string]string{
		"zebra.md":   "# Zebra\n\nThe zebra has black and white stripes.\n",
		"giraffe.md": "# Giraffe\n\nThe giraffe has a very long neck.\n",
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Gleann-Hybrid-Alpha", "0")
	o, ok := hybridAlphaOption(r)
	if !ok {
		t.Fatal("header not parsed")
	}
	block, _, err := s.retrieveProxyContext(context.Background(), "zebra", []string{"animals"}, []gleann.SearchOption{o})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block, "zebra") || strings.Contains(block, "giraffe") {
		t.Fatalf("context = %q, want only the zebra passage", block)
	}
}
