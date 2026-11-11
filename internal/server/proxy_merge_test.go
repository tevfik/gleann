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
	got := mergeProxyHits(per, []string{"a", "b", "c"}, 3)
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
	if all := mergeProxyHits(per, []string{"a", "b"}, 0); len(all) != 5 {
		t.Errorf("k=0 keeps all: %d", len(all))
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
