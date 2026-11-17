package gleann

import (
	"context"
	"testing"
)

func TestBM25AdapterTopK(t *testing.T) {
	a := NewBM25Adapter()
	a.AddDocument(Passage{ID: 1, Text: "the quick brown fox"})
	a.AddDocument(Passage{ID: 2, Text: "a lazy dog sleeps"})
	a.AddDocument(Passage{ID: 3, Text: "quick quick quick foxes"})
	if a.IndexedCount() != 3 {
		t.Fatalf("IndexedCount = %d", a.IndexedCount())
	}
	ids, scores := a.TopK("quick fox", 2)
	if len(ids) == 0 || len(ids) != len(scores) || len(ids) > 2 {
		t.Fatalf("TopK = %v, %v", ids, scores)
	}
	for i := 1; i < len(scores); i++ {
		if scores[i] > scores[i-1] {
			t.Errorf("scores must be sorted descending: %v", scores)
		}
	}
	if ids, _ := a.TopK("nonexistentterm", 2); len(ids) != 0 {
		t.Errorf("no matching term should give no hits, got %v", ids)
	}
}

func TestRecordedPassagePrefixMissingIndex(t *testing.T) {
	if got := recordedPassagePrefix(t.TempDir(), "absent"); got != "" {
		t.Errorf("prefix of a missing index = %q, want empty", got)
	}
}

func TestMustGetBackendPanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustGetBackend of an unregistered backend must panic")
		}
	}()
	if MustGetBackend("hnsw") == nil {
		t.Error("the built-in hnsw backend must resolve")
	}
	MustGetBackend("covc-no-such-backend")
}

func TestNullSearcher(t *testing.T) {
	var s Searcher = NullSearcher{}
	res, err := s.Search(context.Background(), "anything")
	if err != nil || len(res) != 0 {
		t.Errorf("Search = %v, %v", res, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}
