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
	if oldTF, exists := s.docTermFreqs[id]; exists {
		// Document already indexed. Subtract previous stats to prevent IDF/stat drift.
		for term := range oldTF {
			s.df[term]--
			if s.df[term] <= 0 {
				delete(s.df, term)
			}
		}
		s.totalLen -= s.docLens[id]
		s.docCount--
	}

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
// Evaluates only the candidate documents in O(len(ids) * terms) instead of scanning the full corpus.
func (s *Scorer) ScoreDocIDs(query string, ids []int64) []float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryTerms := tokenize(query)
	scores := make([]float32, len(ids))
	if s.docCount == 0 || len(ids) == 0 || len(queryTerms) == 0 {
		return scores
	}

	for i, id := range ids {
		tf, ok := s.docTermFreqs[id]
		if !ok {
			continue
		}
		docLen := float64(s.docLens[id])
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
			scores[i] = float32(score)
		}
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

// splitCamelWords splits camelCase, PascalCase, or letter-digit compound words into sub-words.
func splitCamelWords(s string) []string {
	var words []string
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return nil
	}

	start := 0
	for i := 1; i < n; i++ {
		prev := runes[i-1]
		curr := runes[i]

		// Lowercase -> Uppercase (e.g. "openStore" -> "open", "Store")
		if unicode.IsLower(prev) && unicode.IsUpper(curr) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}

		// Upper -> Upper followed by Lower (e.g. "ASTChunker" -> "AST", "Chunker")
		if i+1 < n && unicode.IsUpper(prev) && unicode.IsUpper(curr) && unicode.IsLower(runes[i+1]) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}

		// Letter <-> Digit transition (e.g. "sha256" -> "sha", "256", "256bit" -> "256", "bit")
		if (unicode.IsLetter(prev) && unicode.IsDigit(curr)) || (unicode.IsDigit(prev) && unicode.IsLetter(curr)) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}
	}
	if start < n {
		words = append(words, string(runes[start:]))
	}
	return words
}

// tokenize splits text into lowercase tokens, expanding camelCase/PascalCase compound words.
func tokenize(text string) []string {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	filtered := make([]string, 0, len(words)*2)
	for _, w := range words {
		lower := strings.ToLower(w)
		if len(lower) > 1 && !isStopWord(lower) {
			filtered = append(filtered, lower)
		}

		// If the word contains camelCase/PascalCase or letter-digit transitions, add sub-tokens.
		parts := splitCamelWords(w)
		if len(parts) > 1 {
			for _, part := range parts {
				partLower := strings.ToLower(part)
				if len(partLower) > 1 && !isStopWord(partLower) && partLower != lower {
					filtered = append(filtered, partLower)
				}
			}
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

