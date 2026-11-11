package gleann

import (
	"context"
	"errors"
	"testing"
)

// reverseReranker scores results in reverse input order and records the
// pool it was given.
type reverseReranker struct {
	got []SearchResult
	err error
}

func (r *reverseReranker) Rerank(_ context.Context, _ string, rs []SearchResult, _ int) ([]SearchResult, error) {
	r.got = append([]SearchResult(nil), rs...)
	if r.err != nil {
		return nil, r.err
	}
	out := make([]SearchResult, len(rs))
	for i, x := range rs {
		x.Score = float32(i + 1) // last input gets the highest score
		out[len(rs)-1-i] = x
	}
	return out, nil
}

func stageInput(n int) []SearchResult {
	rs := make([]SearchResult, n)
	for i := range rs {
		rs[i] = SearchResult{ID: int64(i), Score: float32(n - i)}
	}
	return rs
}

func ids(rs []SearchResult) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func TestRerankStage_ReplacePoolAndTopK(t *testing.T) {
	rr := &reverseReranker{}
	got := rerankStage(context.Background(), rr, "how does search work", stageInput(10), 3, SearchConfig{RerankPool: 5})
	if len(rr.got) != 5 {
		t.Fatalf("pool = %d, want 5", len(rr.got))
	}
	if want := []int64{4, 3, 2}; !equalIDs(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
}

func TestRerankStage_PoolNeverBelowTopK(t *testing.T) {
	rr := &reverseReranker{}
	rerankStage(context.Background(), rr, "a natural question here", stageInput(10), 6, SearchConfig{RerankPool: 2})
	if len(rr.got) != 6 {
		t.Fatalf("pool = %d, want topK 6", len(rr.got))
	}
}

func TestRerankStage_SkipIdentifiers(t *testing.T) {
	rr := &reverseReranker{}
	got := rerankStage(context.Background(), rr, "LeannSearcher", stageInput(5), 3, SearchConfig{RerankSkipIdentifiers: true})
	if rr.got != nil {
		t.Fatal("reranker called for an identifier query")
	}
	if want := []int64{0, 1, 2}; !equalIDs(ids(got), want) {
		t.Fatalf("got %v, want first-stage %v", ids(got), want)
	}
	// Without the gate the reranker runs.
	rerankStage(context.Background(), rr, "LeannSearcher", stageInput(5), 3, SearchConfig{})
	if rr.got == nil {
		t.Fatal("reranker not called without the gate")
	}
}

func TestRerankStage_ErrorKeepsOrder(t *testing.T) {
	rr := &reverseReranker{err: errors.New("down")}
	got := rerankStage(context.Background(), rr, "why is it slow", stageInput(5), 2, SearchConfig{})
	if want := []int64{0, 1}; !equalIDs(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
}

func TestIsIdentifierQuery(t *testing.T) {
	cases := map[string]bool{
		"":                                false,
		"LeannSearcher":                   true,
		"parse_args":                      true,
		"search":                          true,
		"func NewServer":                  true,
		"pkg.Func usage":                  true,
		"rerank results":                  false,
		"how does the reranker work":      false,
		"where is getConfig defined here": false,
	}
	for q, want := range cases {
		if got := IsIdentifierQuery(q); got != want {
			t.Errorf("IsIdentifierQuery(%q) = %v, want %v", q, got, want)
		}
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
