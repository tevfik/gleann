package gleann

import (
	"context"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// FTSScorer is the persisted FTS5 lexical leg wrapped as a Scorer (RFC 003
// section 8 step 3). It implements TopKScorer and PersistedScorer over a
// SQLite database. For a format-1 sidecar the lex rowids equal the bbolt
// passage IDs. For a format-2 index it reads <name>.db directly (no sidecar)
// and slotIDs is set: the searcher's ID space is vec_slot there, so TopK maps
// lex rowids to slots and Score maps slots back to rowids. Because it is
// persisted, Searcher.Load skips the in-RAM BM25 build for it.
type FTSScorer struct {
	store    *store.Store
	analyzer analysis.Analyzer
	opts     store.LexQueryOptions
	// slotIDs marks a format-2 index: results are translated between lex
	// rowids (the store's internal IDs) and the searcher's IDs — vec_slot for
	// a vector-addressed passage, the negative rowid for a slotless one.
	slotIDs bool
}

var (
	_ Scorer          = (*FTSScorer)(nil)
	_ TopKScorer      = (*FTSScorer)(nil)
	_ PersistedScorer = (*FTSScorer)(nil)
)

// Persisted reports that the lexical index lives on disk, so Searcher.Load
// skips the passage scan and AddDocuments build (RFC 003 section 3.1).
func (f *FTSScorer) Persisted() bool { return true }

// AddDocuments is a no-op: the lexical index is persisted, not built in RAM.
func (f *FTSScorer) AddDocuments(passages []Passage) {}

// Score scores the query against the given passages through the persisted
// index (ScoreIDs), returning one score per passage in the same order.
// Passages the query does not match score 0. A store error degrades to zero
// scores rather than failing the whole hybrid search.
func (f *FTSScorer) Score(query string, passages []Passage) []float32 {
	out := make([]float32, len(passages))
	ctx := context.Background()
	q := store.LexQuery{Text: query, Analyzer: f.analyzer, Options: f.opts}

	rowids := make([]int64, len(passages))
	if f.slotIDs {
		// The passages carry searcher IDs: vec_slots for vector-addressed
		// passages, negative rowids for slotless ones (see PassageStore).
		// Translate both to lex rowids first.
		rowidOf := make(map[int64]int64, len(passages))
		var slots []int64
		for _, p := range passages {
			if p.ID < 0 {
				rowidOf[p.ID] = -p.ID
			} else {
				slots = append(slots, p.ID)
			}
		}
		if len(slots) > 0 {
			ps, err := f.store.BySlot(ctx, slots)
			if err != nil {
				return out
			}
			for _, p := range ps {
				if p.VecSlot != nil {
					rowidOf[*p.VecSlot] = p.ID
				}
			}
		}
		rowids = rowids[:0]
		for _, rid := range rowidOf {
			rowids = append(rowids, rid)
		}
		scores, err := f.store.ScoreIDs(ctx, q, rowids)
		if err != nil {
			return out
		}
		for i, p := range passages {
			if rid, ok := rowidOf[p.ID]; ok {
				out[i] = scores[rid]
			}
		}
		return out
	}

	for i, p := range passages {
		rowids[i] = p.ID
	}
	scores, err := f.store.ScoreIDs(ctx, q, rowids)
	if err != nil {
		return out
	}
	for i, id := range rowids {
		out[i] = scores[id]
	}
	return out
}

// TopK returns the top-k passage IDs and scores (higher is better) from the
// persisted index. A store error degrades to no results.
func (f *FTSScorer) TopK(query string, k int) ([]int64, []float32) {
	ctx := context.Background()
	hits, err := f.store.Search(ctx, store.LexQuery{Text: query, Analyzer: f.analyzer, Options: f.opts}, k)
	if err != nil {
		return nil, nil
	}
	if f.slotIDs {
		// Translate lex rowids to searcher IDs, keeping the store's ranking
		// (bm25, then rowid): a vec_slot for a vector-addressed passage, the
		// negative rowid for a slotless one (see PassageStore). No hit is
		// dropped: a slotless passage is still lexically retrievable.
		rowids := make([]int64, len(hits))
		for i, h := range hits {
			rowids[i] = h.ID
		}
		ps, err := f.store.GetMany(ctx, rowids)
		if err != nil {
			return nil, nil
		}
		slotOf := make(map[int64]int64, len(ps))
		for _, p := range ps {
			if p.VecSlot != nil {
				slotOf[p.ID] = *p.VecSlot
			}
		}
		ids := make([]int64, 0, len(hits))
		scores := make([]float32, 0, len(hits))
		for _, h := range hits {
			if slot, ok := slotOf[h.ID]; ok {
				ids = append(ids, slot)
			} else {
				ids = append(ids, -h.ID)
			}
			scores = append(scores, h.Score)
		}
		return ids, scores
	}
	ids := make([]int64, len(hits))
	scores := make([]float32, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
		scores[i] = h.Score
	}
	return ids, scores
}

// ScoreRowIDs scores the query against explicit lex rowids through the
// persisted index, returning a map from rowid to BM25 score (higher is better).
// Rowids the query does not match are absent from the map. It is the
// pre-selection seam for filtered search: the caller already knows the matching
// rowids from the passages table, so it scores them directly instead of
// retrieving and translating a top-k list. A store error degrades to an empty
// map rather than failing the search.
func (f *FTSScorer) ScoreRowIDs(query string, rowids []int64) map[int64]float32 {
	if len(rowids) == 0 {
		return map[int64]float32{}
	}
	q := store.LexQuery{Text: query, Analyzer: f.analyzer, Options: f.opts}
	scores, err := f.store.ScoreIDs(context.Background(), q, rowids)
	if err != nil {
		return map[int64]float32{}
	}
	return scores
}

// Close releases the database handle. For a format-2 index the handle is
// shared with the passage store; sql.DB.Close is safe to call twice.
func (f *FTSScorer) Close() error { return f.store.Close() }
