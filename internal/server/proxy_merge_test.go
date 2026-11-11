package server

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestMergeProxyHitsRanksAcrossIndexes(t *testing.T) {
	per := map[string][]gleann.SearchResult{
		"a": {{ID: 1, Score: 0.9}, {ID: 2, Score: 0.4}, {ID: 3, Score: 0.3}},
		"b": {{ID: 4, Score: 0.8}, {ID: 5, Score: 0.4}},
		"c": {},
	}
	got := mergeProxyHits(per, []string{"a", "b", "c"}, 3, 0.7)
	want := []struct {
		index string
		id    int64
	}{{"a", 1}, {"b", 4}, {"a", 2}} // 0.4 tie: index a first (order of names)
	if len(got) != len(want) {
		t.Fatalf("got %d hits, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].index != w.index || got[i].result.ID != w.id {
			t.Errorf("hit %d: %s/%d, want %s/%d", i, got[i].index, got[i].result.ID, w.index, w.id)
		}
	}
	if all := mergeProxyHits(per, []string{"a", "b"}, 0, 0.7); len(all) != 5 {
		t.Errorf("k=0 keeps all: %d", len(all))
	}
}

// With raw relevance, several documents rank by it, not by each index's own score: index a's passages (its own
// best, score ~1) are far from the query and b's are close, so b's come first although its scores are lower. This
// is the failure it fixes: with many documents in a conversation, the one holding the answer was crowded out by
// the other documents' best guesses.
func TestMergeProxyHitsUsesRawRelevance(t *testing.T) {
	f := func(v float32) *float32 { return &v }
	per := map[string][]gleann.SearchResult{
		"a": {{ID: 1, Score: 0.99, VectorDistance: f(0.42), BM25Score: f(2.1)}, {ID: 2, Score: 0.95, VectorDistance: f(0.45)}},
		"b": {{ID: 3, Score: 0.96, VectorDistance: f(0.18), BM25Score: f(9.5)}, {ID: 4, Score: 0.94, VectorDistance: f(0.22), BM25Score: f(7.0)}},
	}
	got := mergeProxyHits(per, []string{"a", "b"}, 3, 0.7)
	if got[0].index != "b" || got[0].result.ID != 3 || got[1].result.ID != 4 || got[2].result.ID != 1 {
		t.Errorf("order %v/%d %v/%d %v/%d", got[0].index, got[0].result.ID, got[1].index, got[1].result.ID, got[2].index, got[2].result.ID)
	}
	if got[0].result.Score < 0.99 || got[0].result.Score > 1.0001 {
		t.Errorf("first in both orders scores 1, got %v", got[0].result.Score)
	}
	// One index: its own order and scores stay.
	one := mergeProxyHits(map[string][]gleann.SearchResult{"a": per["a"]}, []string{"a"}, 0, 0.7)
	if one[0].result.ID != 1 || one[0].result.Score != 0.99 {
		t.Errorf("single index changed: %+v", one[0].result)
	}
}

func TestProxyTopK(t *testing.T) {
	if k := proxyTopK(nil); k != 10 {
		t.Errorf("default %d", k)
	}
	if k := proxyTopK([]gleann.SearchOption{gleann.WithTopK(4)}); k != 4 {
		t.Errorf("WithTopK(4): %d", k)
	}
}
