package gleann

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeExpander struct {
	out   string
	err   error
	calls int
	lang  string
}

func (f *fakeExpander) Expand(_ context.Context, _, language string) (string, error) {
	f.calls++
	f.lang = language
	return f.out, f.err
}

func TestRetrievalQuery(t *testing.T) {
	s := &LeannSearcher{}
	s.langOnce.Do(func() { s.language = "Go" })
	ctx := context.Background()
	e := &fakeExpander{out: "func Rerank() {}"}

	if got := s.retrievalQuery(ctx, "how are results reranked", SearchConfig{}); got != "how are results reranked" {
		t.Fatalf("expansion off: %q", got)
	}
	got := s.retrievalQuery(ctx, "how are results reranked", SearchConfig{Expand: true, Expander: e})
	if got != "how are results reranked\nfunc Rerank() {}" || e.lang != "Go" {
		t.Fatalf("expanded = %q (lang %q)", got, e.lang)
	}
	// Identifier queries are never expanded.
	e.calls = 0
	if got := s.retrievalQuery(ctx, "LeannSearcher", SearchConfig{Expand: true, Expander: e}); got != "LeannSearcher" || e.calls != 0 {
		t.Fatalf("identifier query expanded: %q", got)
	}
	// Errors and empty output fall back to the query.
	for _, bad := range []*fakeExpander{{err: errors.New("down")}, {out: "  "}} {
		if got := s.retrievalQuery(ctx, "how are results reranked", SearchConfig{Expand: true, Expander: bad}); got != "how are results reranked" {
			t.Fatalf("fallback = %q", got)
		}
	}
	// The searcher-level expander is used when none is passed.
	s.SetQueryExpander(e)
	if got := s.retrievalQuery(ctx, "how are results reranked", SearchConfig{Expand: true}); !strings.Contains(got, "func Rerank") {
		t.Fatalf("searcher expander not used: %q", got)
	}
	// No expander at all: query unchanged.
	if got := (&LeannSearcher{}).retrievalQuery(ctx, "how are results reranked", SearchConfig{Expand: true}); got != "how are results reranked" {
		t.Fatalf("no expander: %q", got)
	}
}

func TestHyDEPromptAndFences(t *testing.T) {
	if p := HyDEPrompt("parse flags", "Go"); !strings.Contains(p, "short Go code snippet") || !strings.HasSuffix(p, "implement: parse flags") {
		t.Fatalf("prompt = %q", p)
	}
	if p := HyDEPrompt("parse flags", ""); !strings.Contains(p, "short code snippet") {
		t.Fatalf("prompt without language = %q", p)
	}
	if got := stripCodeFences("```go\nfunc A() {}\n```"); got != "func A() {}" {
		t.Fatalf("stripCodeFences = %q", got)
	}
}

func TestLLMQueryExpanderCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "```go\nfunc ParseFlags() {}\n```"},
			"done":    true,
		})
	}))
	defer srv.Close()

	cc := DefaultChatConfig()
	cc.Provider, cc.BaseURL, cc.Model = LLMOllama, srv.URL, "m"
	e := NewLLMQueryExpander(NullSearcher{}, cc)
	for i := 0; i < 3; i++ {
		out, err := e.Expand(context.Background(), "parse flags", "Go")
		if err != nil {
			t.Fatal(err)
		}
		if out != "func ParseFlags() {}" {
			t.Fatalf("out = %q", out)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("LLM called %d times, want 1 (cache)", n)
	}
	if _, err := e.Expand(context.Background(), "parse flags", "Python"); err != nil || calls.Load() != 2 {
		t.Fatalf("language must be part of the cache key (calls %d, err %v)", calls.Load(), err)
	}
}
