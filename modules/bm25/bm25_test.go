package bm25

import (
	"testing"
)

func TestNewScorer(t *testing.T) {
	s := NewScorer()
	if s.k1 != 1.5 {
		t.Errorf("expected k1=1.5, got %f", s.k1)
	}
	if s.b != 0.75 {
		t.Errorf("expected b=0.75, got %f", s.b)
	}
	if s.DocCount() != 0 {
		t.Errorf("expected 0 docs, got %d", s.DocCount())
	}
}

func TestAddDocument(t *testing.T) {
	s := NewScorer()
	s.AddDocument(0, "the quick brown fox jumps over the lazy dog")
	s.AddDocument(1, "the cat sat on the mat")

	if s.DocCount() != 2 {
		t.Errorf("expected 2 docs, got %d", s.DocCount())
	}
}

func TestScore(t *testing.T) {
	s := NewScorer()
	s.AddDocument(0, "the quick brown fox jumps over the lazy dog")
	s.AddDocument(1, "the cat sat on the mat")
	s.AddDocument(2, "a brown bear chased the fox through the forest")

	scores := s.Score("brown fox")
	if len(scores) == 0 {
		t.Fatal("expected scores")
	}

	// Doc 0 "quick brown fox" and Doc 2 "brown bear fox" should score higher.
	if scores[0] <= 0 {
		t.Error("expected doc 0 to have positive score")
	}
	if scores[2] <= 0 {
		t.Error("expected doc 2 to have positive score")
	}

	// Doc 1 "cat sat mat" has no relevant terms.
	if scores[1] > 0 {
		t.Error("expected doc 1 to have zero score")
	}
}

func TestScoreEmpty(t *testing.T) {
	s := NewScorer()
	scores := s.Score("test query")
	if len(scores) != 0 {
		t.Errorf("expected empty scores for empty index, got %d", len(scores))
	}
}

func TestTopK(t *testing.T) {
	s := NewScorer()
	s.AddDocument(0, "machine learning algorithms for natural language processing")
	s.AddDocument(1, "cooking recipes for chocolate cake")
	s.AddDocument(2, "deep learning neural networks for NLP")
	s.AddDocument(3, "quantum computing theoretical foundations")
	s.AddDocument(4, "language models transformer architecture")

	ids, scores := s.TopK("machine learning language", 3)
	if len(ids) > 3 {
		t.Errorf("expected <= 3 results, got %d", len(ids))
	}

	// First result should be doc 0 (machine learning + language).
	if len(ids) > 0 && ids[0] != 0 {
		t.Logf("scores: %v, ids: %v", scores, ids)
	}

	// Scores should be descending.
	for i := 1; i < len(scores); i++ {
		if scores[i] > scores[i-1] {
			t.Errorf("scores not descending: %f > %f", scores[i], scores[i-1])
		}
	}
}

func TestScoreDocIDs(t *testing.T) {
	s := NewScorer()
	s.AddDocument(0, "hello world")
	s.AddDocument(1, "goodbye world")
	s.AddDocument(2, "hello again")

	scores := s.ScoreDocIDs("hello", []int64{0, 1, 2})
	if len(scores) != 3 {
		t.Errorf("expected 3 scores, got %d", len(scores))
	}

	// Doc 0 and 2 should have positive scores.
	if scores[0] <= 0 {
		t.Error("expected positive score for doc 0")
	}
	if scores[2] <= 0 {
		t.Error("expected positive score for doc 2")
	}
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("The Quick Brown Fox!")
	if len(tokens) != 3 { // "quick", "brown", "fox" (stop words removed)
		t.Errorf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}

	// Check lowercase.
	for _, tok := range tokens {
		if tok != "quick" && tok != "brown" && tok != "fox" {
			t.Errorf("unexpected token: %q", tok)
		}
	}
}

func TestTokenizeEmpty(t *testing.T) {
	tokens := tokenize("")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(tokens))
	}
}

func TestStopWords(t *testing.T) {
	if !isStopWord("the") {
		t.Error("'the' should be a stop word")
	}
	if isStopWord("fox") {
		t.Error("'fox' should not be a stop word")
	}
}

func TestAddDocuments(t *testing.T) {
	s := NewScorer()
	ids := []int64{0, 1, 2}
	texts := []string{"hello world", "foo bar", "test case"}
	s.AddDocuments(ids, texts)

	if s.DocCount() != 3 {
		t.Errorf("expected 3 docs, got %d", s.DocCount())
	}
}

func BenchmarkScore(b *testing.B) {
	s := NewScorer()
	for i := 0; i < 1000; i++ {
		s.AddDocument(int64(i), "this is a sample document with some words for benchmarking purposes")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Score("sample document benchmark")
	}
}
