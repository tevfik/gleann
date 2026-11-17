package bm25

import (
	"reflect"
	"testing"
)

// With b = 0 BM25 ignores document length: the same term frequency scores
// the same in a short and a long document. With the default b it does not.
func TestNewScorerWithParams_LengthNormalisation(t *testing.T) {
	docs := map[int64]string{
		1: "kernel",
		2: "kernel scheduler memory allocator interrupt driver",
		3: "unrelated words only",
	}
	flat := NewScorerWithParams(1.2, 0)
	def := NewScorer()
	for id, text := range docs {
		flat.AddDocument(id, text)
		def.AddDocument(id, text)
	}
	fs := flat.ScoreDocIDs("kernel", []int64{1, 2})
	if fs[0] != fs[1] || fs[0] <= 0 {
		t.Fatalf("b=0 scores = %v, want equal positive scores", fs)
	}
	ds := def.ScoreDocIDs("kernel", []int64{1, 2})
	if ds[0] <= ds[1] {
		t.Fatalf("default b scores = %v, want the short document ahead", ds)
	}
}

func TestHasDocument(t *testing.T) {
	s := NewScorer()
	if s.HasDocument(7) {
		t.Fatal("HasDocument(7) on an empty scorer = true")
	}
	s.AddDocument(7, "hello world")
	if !s.HasDocument(7) || s.HasDocument(8) {
		t.Fatalf("HasDocument(7)=%v HasDocument(8)=%v, want true false", s.HasDocument(7), s.HasDocument(8))
	}
}

// Unknown IDs score zero in their slot, and the guards (empty query, no
// candidate IDs) return zeroed slices of the right length.
func TestScoreDocIDs_UnknownAndEmpty(t *testing.T) {
	s := NewScorer()
	s.AddDocument(1, "vector index search")
	got := s.ScoreDocIDs("vector", []int64{99, 1})
	if len(got) != 2 || got[0] != 0 || got[1] <= 0 {
		t.Fatalf("ScoreDocIDs(unknown, known) = %v, want [0 >0]", got)
	}
	if got := s.ScoreDocIDs("the", []int64{1}); !reflect.DeepEqual(got, []float32{0}) {
		t.Fatalf("stop-word-only query = %v, want [0]", got)
	}
	if got := s.ScoreDocIDs("vector", nil); len(got) != 0 {
		t.Fatalf("no ids = %v, want empty", got)
	}
}

// TopK cuts the ranking at k and keeps the best documents first.
func TestTopK_Truncates(t *testing.T) {
	s := NewScorer()
	s.AddDocument(1, "graph graph graph")
	s.AddDocument(2, "graph")
	s.AddDocument(3, "graph database")
	s.AddDocument(4, "nothing relevant")
	ids, scores := s.TopK("graph", 2)
	if len(ids) != 2 || len(scores) != 2 {
		t.Fatalf("TopK(k=2) = %v %v, want two results", ids, scores)
	}
	if ids[0] != 1 || scores[0] < scores[1] {
		t.Fatalf("TopK order = %v %v, want doc 1 first, descending", ids, scores)
	}
}

func TestSplitCamelWordsEmpty(t *testing.T) {
	if got := splitCamelWords(""); got != nil {
		t.Fatalf("splitCamelWords(\"\") = %v, want nil", got)
	}
	// The exported Tokenize is the same tokenizer the scorer indexes with.
	if got, want := Tokenize("parseHTTPRequest"), tokenize("parseHTTPRequest"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize = %v, tokenize = %v", got, want)
	}
}
