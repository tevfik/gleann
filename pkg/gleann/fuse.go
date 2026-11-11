package gleann

import "sort"

// FuseAcrossIndexes ranks the results of several indexes together and returns them sorted, best first.
//
// Each index's Score is a fusion of that index's own vector and BM25 ranks, normalized so that its best passage
// scores about 1 however relevant it is: comparing Scores of different indexes puts every index's best passages on
// top, and in a conversation with many documents the one that answers the question can lose its place to the others'
// best guesses. When the results come from two or more indexes and all carry their raw vector distance (the indexes
// share one embedding model, so distances compare), this ranks them by reciprocal rank fusion (k = 60) over the
// global orders of the raw values: distance ascending, and BM25 score descending for the results that have one, weighted
// by alpha as in a single index (0 = BM25 only, 1 = vector only). Score becomes that fusion, scaled so a passage first
// in both orders scores 1. Otherwise the results are sorted by Score as before. Reranked results keep their
// cross-encoder scores, which already compare across indexes.
//
// Limits: BM25 scores use each index's own term statistics, and distances compare only between indexes embedded
// the same way (same model and the same passage/query prefixes), so mixing an index built with E5 prefixes and one
// built without makes the vector order approximate.
func FuseAcrossIndexes(results []SearchResult, indexes int, alpha float32) []SearchResult {
	if scores, ok := CrossIndexScores(results, indexes, alpha); ok {
		for i := range results {
			results[i].Score = scores[i]
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

// CrossIndexScores returns the scores FuseAcrossIndexes ranks by, in the order of results, or false when the results
// cannot be ranked together that way (one index, or a result without its raw values): then their Scores stay.
func CrossIndexScores(results []SearchResult, indexes int, alpha float32) ([]float32, bool) {
	if indexes < 2 || len(results) < 2 {
		return nil, false
	}
	for _, r := range results {
		if r.Reranked {
			return nil, false // cross-encoder scores already compare across indexes
		}
		if r.VectorDistance == nil && (alpha > 0 || r.BM25Score == nil) {
			return nil, false
		}
	}
	const kRRF = 60.0
	fused := make([]float32, len(results))
	order := make([]int, len(results))
	for i := range order {
		order[i] = i
	}
	if alpha > 0 {
		vec := append([]int(nil), order...)
		sort.SliceStable(vec, func(a, b int) bool { return *results[vec[a]].VectorDistance < *results[vec[b]].VectorDistance })
		for rank, i := range vec {
			fused[i] += alpha / (kRRF + float32(rank+1))
		}
	}
	if alpha < 1 {
		var lex []int
		for _, i := range order {
			if results[i].BM25Score != nil {
				lex = append(lex, i)
			}
		}
		sort.SliceStable(lex, func(a, b int) bool { return *results[lex[a]].BM25Score > *results[lex[b]].BM25Score })
		for rank, i := range lex {
			fused[i] += (1 - alpha) / (kRRF + float32(rank+1))
		}
	}
	best := float32(1 / (kRRF + 1))
	for i := range fused {
		fused[i] /= best
	}
	return fused, true
}
