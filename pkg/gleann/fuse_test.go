package gleann

import "testing"

func f32(v float32) *float32 { return &v }

// Results of two indexes rank by their raw distances and BM25 scores, not by each index's own (rank-based) Score.
func TestFuseAcrossIndexesRanksByRawRelevance(t *testing.T) {
	res := []SearchResult{
		{ID: 1, Score: 1.0, VectorDistance: f32(0.40), BM25Score: f32(1.5)},  // index a's best guess, far from the query
		{ID: 2, Score: 0.95, VectorDistance: f32(0.12), BM25Score: f32(8.0)}, // index b's answer
		{ID: 3, Score: 0.97, VectorDistance: f32(0.30)},                      // index a, no BM25 hit
	}
	got := FuseAcrossIndexes(res, 2, 0.7)
	// 2 is first in both orders; 1 (third by distance, second by BM25) passes 3 (second by distance, no BM25 hit).
	if got[0].ID != 2 || got[1].ID != 1 || got[2].ID != 3 {
		t.Fatalf("order %d %d %d", got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].Score < 0.999 || got[0].Score > 1.0001 {
		t.Errorf("first in both orders: score %v, want 1", got[0].Score)
	}
}

// One index, or results without raw values (older indexes, other searchers), keep their Scores.
func TestFuseAcrossIndexesFallsBackToScore(t *testing.T) {
	one := []SearchResult{{ID: 1, Score: 0.2, VectorDistance: f32(0.1)}, {ID: 2, Score: 0.9, VectorDistance: f32(0.5)}}
	if got := FuseAcrossIndexes(one, 1, 0.7); got[0].ID != 2 || got[0].Score != 0.9 {
		t.Errorf("one index: %+v", got[0])
	}
	mixed := []SearchResult{{ID: 1, Score: 0.2, VectorDistance: f32(0.1)}, {ID: 2, Score: 0.9}}
	if _, ok := CrossIndexScores(mixed, 2, 0.7); ok {
		t.Error("a result without its distance must keep the Scores")
	}
	// BM25 only (alpha 0): BM25 scores are enough.
	lex := []SearchResult{{ID: 1, Score: 1, BM25Score: f32(2)}, {ID: 2, Score: 1, BM25Score: f32(9)}}
	if got := FuseAcrossIndexes(lex, 2, 0); got[0].ID != 2 {
		t.Errorf("BM25 only: first %d", got[0].ID)
	}
}

func TestCrossIndexScoresKeepsRerankedScores(t *testing.T) {
	f := func(v float32) *float32 { return &v }
	rs := []SearchResult{
		{ID: 1, Score: 2.5, VectorDistance: f(0.5), Reranked: true},
		{ID: 2, Score: 7.1, VectorDistance: f(0.1), Reranked: true},
	}
	if _, ok := CrossIndexScores(rs, 2, 0.7); ok {
		t.Fatal("reranked results must keep their cross-encoder scores")
	}
	out := FuseAcrossIndexes(rs, 2, 0.7)
	if out[0].ID != 2 || out[0].Score != 7.1 {
		t.Fatalf("got %+v, want ID 2 first with its reranker score", out[0])
	}
}
