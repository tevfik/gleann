// Package bm25 implements the Okapi BM25 ranking function for text retrieval.
package bm25

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Scorer implements the BM25 scoring algorithm.
type Scorer struct {
	mu sync.RWMutex

	// Parameters.
	k1 float64 // term frequency saturation parameter (default: 1.5)
	b  float64 // length normalization parameter (default: 0.75)

	// Index.
	docCount  int
	avgDocLen float64
	totalLen  int

	// Term -> doc frequency.
	df map[string]int

	// Doc ID -> term frequencies.
	docTermFreqs map[int64]map[string]int

	// Doc ID -> length.
	docLens map[int64]int
}

// NewScorer creates a new BM25 scorer with default parameters.
func NewScorer() *Scorer {
	return &Scorer{
		k1:           1.5,
		b:            0.75,
		df:           make(map[string]int),
		docTermFreqs: make(map[int64]map[string]int),
		docLens:      make(map[int64]int),
	}
}

// NewScorerWithParams creates a BM25 scorer with custom parameters.
func NewScorerWithParams(k1, b float64) *Scorer {
	s := NewScorer()
	s.k1 = k1
	s.b = b
	return s
}

// AddDocument adds a document to the BM25 index.
func (s *Scorer) AddDocument(id int64, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addDocumentLocked(id, text)
}

func (s *Scorer) addDocumentLocked(id int64, text string) {
	tokens := tokenize(text)
	tf := make(map[string]int)
	for _, token := range tokens {
		tf[token]++
	}

	// Track which terms appear in this document (for df).
	for term := range tf {
		s.df[term]++
	}

	s.docTermFreqs[id] = tf
	s.docLens[id] = len(tokens)
	s.totalLen += len(tokens)
	s.docCount++
	s.avgDocLen = float64(s.totalLen) / float64(s.docCount)
}

// AddDocuments adds multiple documents at once.
func (s *Scorer) AddDocuments(ids []int64, texts []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range ids {
		s.addDocumentLocked(ids[i], texts[i])
	}
}

// HasDocument returns true if the document is already indexed.
func (s *Scorer) HasDocument(id int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.docTermFreqs[id]
	return ok
}

// Score computes BM25 scores for the query against all indexed documents.
// Returns a map from document ID to score.
func (s *Scorer) Score(query string) map[int64]float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryTerms := tokenize(query)
	scores := make(map[int64]float32)

	for docID, tf := range s.docTermFreqs {
		docLen := float64(s.docLens[docID])
		score := float64(0)

		for _, term := range queryTerms {
			termFreq, ok := tf[term]
			if !ok {
				continue
			}

			// IDF component.
			docFreq := s.df[term]
			idf := math.Log(1 + (float64(s.docCount)-float64(docFreq)+0.5)/(float64(docFreq)+0.5))

			// TF component with length normalization.
			tfNorm := (float64(termFreq) * (s.k1 + 1)) /
				(float64(termFreq) + s.k1*(1-s.b+s.b*docLen/s.avgDocLen))

			score += idf * tfNorm
		}

		if score > 0 {
			scores[docID] = float32(score)
		}
	}

	return scores
}

// ScoreDocIDs computes BM25 scores for the query against specific document IDs.
func (s *Scorer) ScoreDocIDs(query string, ids []int64) []float32 {
	allScores := s.Score(query)
	scores := make([]float32, len(ids))
	for i, id := range ids {
		scores[i] = allScores[id]
	}
	return scores
}

// TopK returns the top K document IDs sorted by BM25 score.
func (s *Scorer) TopK(query string, k int) ([]int64, []float32) {
	scores := s.Score(query)

	type scored struct {
		id    int64
		score float32
	}
	items := make([]scored, 0, len(scores))
	for id, score := range scores {
		items = append(items, scored{id: id, score: score})
	}

	// Sort descending by score.
	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	if len(items) > k {
		items = items[:k]
	}

	ids := make([]int64, len(items))
	resultScores := make([]float32, len(items))
	for i, item := range items {
		ids[i] = item.id
		resultScores[i] = item.score
	}

	return ids, resultScores
}

// DocCount returns the number of documents in the index.
func (s *Scorer) DocCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docCount
}

// tokenize splits text into lowercase tokens.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	// Simple stop word removal.
	filtered := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) > 1 && !isStopWord(w) {
			filtered = append(filtered, w)
		}
	}
	return filtered
}

var stopWords = map[string]bool{
	"the": true, "is": true, "at": true, "which": true, "on": true,
	"a": true, "an": true, "and": true, "or": true, "but": true,
	"in": true, "to": true, "for": true, "of": true, "with": true,
	"by": true, "from": true, "as": true, "it": true, "that": true,
	"this": true, "be": true, "are": true, "was": true, "were": true,
	"been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true,
	"could": true, "should": true, "may": true, "might": true,
	"not": true, "no": true, "so": true, "if": true, "then": true,
}

func isStopWord(word string) bool {
	return stopWords[word]
}
