package gleann

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A /v1/rerank server such as llama-server --reranking wants plain-string
// documents and no Authorization header when no key is configured.
func TestRerankJinaCompatibleServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "" {
			t.Errorf("unexpected Authorization header %q", h)
		}
		var req struct {
			Documents []string `json:"documents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("documents must be plain strings: %v", err)
		}
		// Score documents by length so the order flips.
		type item struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		}
		var out struct {
			Results []item `json:"results"`
		}
		for i, d := range req.Documents {
			out.Results = append(out.Results, item{i, float64(len(d))})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	rr := NewReranker(RerankerConfig{Provider: RerankerJina, Model: "m", BaseURL: srv.URL})
	got, err := rr.Rerank(context.Background(), "q", []SearchResult{{Text: "a"}, {Text: "aaa"}, {Text: "aa"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "aaa" || got[1].Text != "aa" {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestWithRerankPool(t *testing.T) {
	var c SearchConfig
	WithRerankPool(20)(&c)
	if c.RerankPool != 20 {
		t.Fatalf("RerankPool = %d, want 20", c.RerankPool)
	}
}
