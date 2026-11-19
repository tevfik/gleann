package diskann

import (
	"context"
	"math/rand"
	"sort"
	"testing"
)

func TestDefaultRerankK(t *testing.T) {
	for _, tc := range []struct{ configured, topK, searchL, want int }{
		{0, 10, 100, 100},  // unset: the whole candidate list
		{-1, 10, 100, 100}, // negative counts as unset
		{40, 10, 100, 40},  // configured wins
		{5, 10, 100, 10},   // never fewer than topK
		{0, 10, 8, 10},     // searchL below topK
	} {
		if got := DefaultRerankK(tc.configured, tc.topK, tc.searchL); got != tc.want {
			t.Errorf("DefaultRerankK(%d, %d, %d) = %d, want %d", tc.configured, tc.topK, tc.searchL, got, tc.want)
		}
	}
}

// bruteTopK returns the exact top-k IDs for q.
func bruteTopK(vecs [][]float32, q []float32, k int, dist DistanceFunc) map[int64]bool {
	type c struct {
		id int64
		d  float32
	}
	all := make([]c, len(vecs))
	for i, v := range vecs {
		all[i] = c{int64(i), dist(q, v)}
	}
	sort.Slice(all, func(a, b int) bool { return all[a].d < all[b].d })
	out := make(map[int64]bool, k)
	for _, x := range all[:k] {
		out[x.id] = true
	}
	return out
}

// The searcher used to rerank only 2*topK PQ candidates, whatever SearchL or
// SearchPQRerank said, which capped recall@10 near 0.91. It now reranks the
// whole candidate list by default and honours SearchPQRerank.
func TestSearcherRerankWidth(t *testing.T) {
	const (
		n, dims, queries, k = 2000, 64, 50, 10
	)
	rng := rand.New(rand.NewSource(7))
	vecs := randomVectors(rng, n, dims)
	qs := randomVectors(rng, queries, dims)

	config := DefaultConfig()
	ctx := context.Background()
	data, err := (&Builder{config: config}).Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	recall := func(rerank int) float64 {
		c := config
		c.DiskANNConfig.SearchPQRerank = rerank
		s := &Searcher{config: c}
		if err := s.Load(ctx, data, IndexMeta{Dimensions: dims, Backend: "diskann"}); err != nil {
			t.Fatalf("load: %v", err)
		}
		defer s.Close()
		dist := GetDistanceFunc(string(c.DiskANNConfig.DistanceMetric))
		hits := 0
		for _, q := range qs {
			ids, _, err := s.Search(ctx, q, k)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			truth := bruteTopK(vecs, q, k, dist)
			for _, id := range ids {
				if truth[id] {
					hits++
				}
			}
		}
		return float64(hits) / float64(queries*k)
	}

	narrow := recall(k) // rerank only topK: PQ order decides
	def := recall(0)    // default: all SearchL candidates
	t.Logf("recall@%d: rerank=%d %.3f, default %.3f", k, k, narrow, def)
	if def < 0.95 {
		t.Errorf("default recall@%d = %.3f, want >= 0.95", k, def)
	}
	if narrow >= def {
		t.Errorf("SearchPQRerank=%d is ignored: recall %.3f, default %.3f", k, narrow, def)
	}
}
