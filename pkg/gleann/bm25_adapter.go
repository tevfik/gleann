package gleann

import (
	"github.com/tevfik/gleann/modules/bm25"
)

// BM25Adapter wraps the bm25.Scorer to implement the gleann.Scorer interface.
type BM25Adapter struct {
	scorer *bm25.Scorer
}

// NewBM25Adapter creates a new BM25Adapter.
func NewBM25Adapter() *BM25Adapter {
	return &BM25Adapter{
		scorer: bm25.NewScorer(),
	}
}

// NewBM25AdapterWithParams creates a new BM25Adapter with custom parameters.
func NewBM25AdapterWithParams(k1, b float64) *BM25Adapter {
	return &BM25Adapter{
		scorer: bm25.NewScorerWithParams(k1, b),
	}
}

// Score scores the query against the given passages.
// Returns a slice of float32 scores, one per passage, in the same order.
func (a *BM25Adapter) Score(query string, passages []Passage) []float32 {
	// Ensure all passages are indexed.
	for _, p := range passages {
		a.scorer.AddDocument(p.ID, p.Text)
	}

	allScores := a.scorer.Score(query)
	result := make([]float32, len(passages))
	for i, p := range passages {
		result[i] = allScores[p.ID]
	}
	return result
}

// AddDocuments adds passages to the BM25 index.
func (a *BM25Adapter) AddDocuments(passages []Passage) {
	for _, p := range passages {
		a.scorer.AddDocument(p.ID, p.Text)
	}
}

// AddDocument adds a single passage to the BM25 index.
// This is suitable for streaming large corpora via ForEachPassage
// without loading all passages into memory at once.
func (a *BM25Adapter) AddDocument(p Passage) {
	a.scorer.AddDocument(p.ID, p.Text)
}

// IndexedCount returns the number of documents in the BM25 index.
func (a *BM25Adapter) IndexedCount() int {
	return a.scorer.DocCount()
}
