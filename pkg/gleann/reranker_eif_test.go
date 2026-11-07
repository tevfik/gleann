package gleann

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRerankEIF_OrdersByEngineScores(t *testing.T) {
	old := eifRank
	defer func() { eifRank = old }()
	var gotModel, gotQuery string
	RegisterEIFRanker(func(_ context.Context, model, query string, docs []string) ([]float32, error) {
		gotModel, gotQuery = model, query
		out := make([]float32, len(docs))
		for i, d := range docs {
			if strings.Contains(d, "NGG") {
				out[i] = 9
			} else {
				out[i] = float32(-i)
			}
		}
		return out, nil
	})
	r := NewReranker(RerankerConfig{Provider: RerankerEIF, Model: "m.gguf"})
	in := []SearchResult{{ID: 1, Text: "bananas"}, {ID: 2, Text: "apples"}, {ID: 3, Text: "binds NGG PAM"}}
	got, err := r.Rerank(context.Background(), "pam?", in, 2)
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "m.gguf" || gotQuery != "pam?" {
		t.Errorf("engine called with model=%q query=%q", gotModel, gotQuery)
	}
	if len(got) != 2 || got[0].ID != 3 || got[0].Score != 9 || got[1].ID != 1 {
		t.Errorf("unexpected order: %+v", got)
	}
	if in[0].Score != 0 {
		t.Errorf("input slice was modified")
	}
}

func TestRerankEIF_Errors(t *testing.T) {
	old := eifRank
	defer func() { eifRank = old }()
	r := NewReranker(RerankerConfig{Provider: RerankerEIF})
	in := []SearchResult{{ID: 1, Text: "a"}}

	eifRank = nil
	if _, err := r.Rerank(context.Background(), "q", in, 0); err == nil || !strings.Contains(err.Error(), "-tags eif") {
		t.Errorf("want a build-tag hint without an engine, got %v", err)
	}
	RegisterEIFRanker(func(context.Context, string, string, []string) ([]float32, error) { return nil, errors.New("boom") })
	if _, err := r.Rerank(context.Background(), "q", in, 0); err == nil {
		t.Error("engine error must be returned")
	}
	RegisterEIFRanker(func(context.Context, string, string, []string) ([]float32, error) { return []float32{1, 2}, nil })
	if _, err := r.Rerank(context.Background(), "q", in, 0); err == nil {
		t.Error("score count mismatch must be an error")
	}
}
