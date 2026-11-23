package gleann

import (
	"context"
	"fmt"
	"testing"
)

// lossyIDScorer is linearIDScorer whose graph search misses the given slots,
// like an approximate graph search that fails to reach the true nearest
// neighbours; VectorDistances stays exact.
type lossyIDScorer struct {
	linearIDScorer
	miss map[int64]bool
}

func (b lossyIDScorer) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	ids, dists, err := b.linearIDScorer.Search(ctx, query, topK+len(b.miss))
	if err != nil {
		return nil, nil, fmt.Errorf("linear search: %w", err)
	}
	var outIDs []int64
	var outDists []float32
	for i, id := range ids {
		if !b.miss[id] && len(outIDs) < topK {
			outIDs, outDists = append(outIDs, id), append(outDists, dists[i])
		}
	}
	return outIDs, outDists, nil
}

func (b lossyIDScorer) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return b.Search(ctx, query, topK)
}

// TestFilteredSearch_FewMatchesScoredExactly: a filter with few matches must
// return the TRUE top-k among them even when the widen-until-k loop already
// found k results. Here the approximate leg misses the best match (slot 0) yet
// returns k passing results from the rest; the old code answered with those and
// only fell back to exact scoring when fewer than k passed.
func TestFilteredSearch_FewMatchesScoredExactly(t *testing.T) {
	items := make([]Item, 120)
	for i := range items {
		lang := "rust"
		if i%12 == 0 { // 10 matches: slots 0, 12, ..., 108
			lang = "go"
		}
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": lang}}
	}
	s := newLinearSearcher(t, items, lossyIDScorer{linearIDScorer{linearBackend{n: 120}}, map[int64]bool{0: true}})
	rs, err := s.Search(context.Background(), "doc", WithTopK(3), WithHybridAlpha(1.0),
		WithMetadataFilters([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Scores must not depend on the filter: the exact path reuses the loop's
	// fixed normalizer (the largest distance among the first retrieveK=50
	// candidates, which the lossy backend returns as slots 1..50: 0.50), so a
	// passage scores exactly as in an unfiltered search with the same retrieveK.
	for _, r := range rs {
		slot := 0
		if _, err := fmt.Sscanf(r.Text, "doc %d", &slot); err != nil {
			t.Fatalf("unexpected text %q", r.Text)
		}
		want := 1 - float32(slot)*0.01/0.50
		if d := r.Score - want; d > 1e-5 || d < -1e-5 {
			t.Errorf("%s: score %v, want %v (loop normalizer)", r.Text, r.Score, want)
		}
	}
	want := []string{"doc 0", "doc 12", "doc 24"}
	if len(rs) != len(want) {
		t.Fatalf("got %d results, want %d", len(rs), len(want))
	}
	for i, r := range rs {
		if r.Text != want[i] {
			t.Errorf("result %d = %q, want %q (the true top-3 among the matches)", i, r.Text, want[i])
		}
	}
}

func TestFilterExactMatches(t *testing.T) {
	for _, tc := range []struct{ passed, examined, total, want int }{
		{10, 100, 10000, 1000},
		{1, 3, 10, 4},
		{0, 100, 10000, 0},
		{5, 0, 10000, -1},
		{5, 100, 0, -1},
	} {
		if got := filterExactMatches(tc.passed, tc.examined, tc.total); got != tc.want {
			t.Errorf("filterExactMatches(%d, %d, %d) = %d, want %d", tc.passed, tc.examined, tc.total, got, tc.want)
		}
	}
}

// prunedIDScorer is lossyIDScorer whose stored vectors were pruned: scoring
// explicit IDs fails, as the hnsw searcher's VectorDistances does.
type prunedIDScorer struct{ lossyIDScorer }

func (prunedIDScorer) VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error) {
	return nil, fmt.Errorf("passage %d has pruned embeddings", ids[0])
}

// TestFilteredSearch_FewMatchesFallsBackWithoutVectors: when the matches cannot
// be scored by vector (pruned embeddings, or a backend without VectorIDScorer),
// the few-matches path must neither replace the loop's k results with a lexical
// ranking nor leave the result short.
func TestFilteredSearch_FewMatchesFallsBackWithoutVectors(t *testing.T) {
	items := make([]Item, 120)
	for i := range items {
		lang := "rust"
		if i%12 == 0 {
			lang = "go"
		}
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": lang}}
	}
	lossy := lossyIDScorer{linearIDScorer{linearBackend{n: 120}}, map[int64]bool{0: true}}
	filter := WithMetadataFilters([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}})
	for name, backend := range map[string]BackendSearcher{
		"pruned":      prunedIDScorer{lossy},
		"no-idscorer": lossy.linearIDScorer.linearBackend,
	} {
		t.Run(name, func(t *testing.T) {
			s := newLinearSearcher(t, items, backend)
			rs, err := s.Search(context.Background(), "doc", WithTopK(3), WithHybridAlpha(1.0), filter)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rs) != 3 {
				t.Fatalf("got %d results, want 3", len(rs))
			}
			for _, r := range rs {
				if r.VectorDistance == nil {
					t.Errorf("%s has no vector distance: a lexical ranking replaced the loop's results", r.Text)
				}
			}
		})
	}
}

func TestFilterExactWorthIt(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		passed, examined, total int
		want                    bool
	}{
		{"1% of 100K", 1, 100, 100_000, true},
		{"10% of 100K", 10, 100, 100_000, true},
		{"no pass yet counts as one", 0, 50, 100_000, true},
		{"a fifth or more is not restrictive", 25, 100, 10_000, false},
		{"more than filterExactMax matches", 15, 100, 100_000, false},
		{"index too large for the scan", 1, 1000, 1_000_000, false},
		{"nothing examined", 0, 0, 100_000, false},
	} {
		if got := filterExactWorthIt(tc.passed, tc.examined, tc.total); got != tc.want {
			t.Errorf("%s: filterExactWorthIt(%d, %d, %d) = %v, want %v", tc.name, tc.passed, tc.examined, tc.total, got, tc.want)
		}
	}
}
