package mcp

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newTestSearcher(t *testing.T, size int64) *gleann.LeannSearcher {
	t.Helper()
	s := gleann.NewSearcher(gleann.DefaultConfig(), nil)
	s.SetEstimatedSize(size)
	return s
}

func TestMCPSearcherCache_SizeAwareEviction(t *testing.T) {
	cfg := Config{
		IndexDir:            t.TempDir(),
		SearcherCacheBudget: 250 * 1024 * 1024, // 250 MB
	}
	s := NewServer(cfg)
	defer s.Close()

	// 1. Add A (100MB)
	sA := newTestSearcher(t, 100*1024*1024)
	s.searcherMu.Lock()
	s.searchers["idx-a"] = sA
	s.searcherLRU = append(s.searcherLRU, "idx-a")
	s.enforceCacheBudgetLocked()
	s.searcherMu.Unlock()

	if len(s.searchers) != 1 {
		t.Fatalf("expected 1 cached searcher, got %d", len(s.searchers))
	}

	// 2. Add B (100MB) -> Total 200MB <= 250MB
	sB := newTestSearcher(t, 100*1024*1024)
	s.searcherMu.Lock()
	s.searchers["idx-b"] = sB
	s.searcherLRU = append(s.searcherLRU, "idx-b")
	s.enforceCacheBudgetLocked()
	s.searcherMu.Unlock()

	if len(s.searchers) != 2 {
		t.Fatalf("expected 2 cached searchers, got %d", len(s.searchers))
	}

	// 3. Add C (100MB) -> Total 300MB > 250MB -> idx-a evicted
	sC := newTestSearcher(t, 100*1024*1024)
	s.searcherMu.Lock()
	s.searchers["idx-c"] = sC
	s.searcherLRU = append(s.searcherLRU, "idx-c")
	s.enforceCacheBudgetLocked()
	s.searcherMu.Unlock()

	if len(s.searchers) != 2 {
		t.Fatalf("expected 2 cached searchers (idx-b, idx-c), got %d", len(s.searchers))
	}
	if _, ok := s.searchers["idx-a"]; ok {
		t.Error("idx-a should have been evicted")
	}
	if _, ok := s.searchers["idx-b"]; !ok {
		t.Error("idx-b should be present")
	}
	if _, ok := s.searchers["idx-c"]; !ok {
		t.Error("idx-c should be present")
	}

	// 4. Touch idx-b
	s.searcherMu.Lock()
	s.touchLRU("idx-b")
	s.searcherMu.Unlock()

	// 5. Add D (100MB) -> Total 300MB > 250MB -> idx-c (oldest) evicted
	sD := newTestSearcher(t, 100*1024*1024)
	s.searcherMu.Lock()
	s.searchers["idx-d"] = sD
	s.searcherLRU = append(s.searcherLRU, "idx-d")
	s.enforceCacheBudgetLocked()
	s.searcherMu.Unlock()

	if len(s.searchers) != 2 {
		t.Fatalf("expected 2 cached searchers (idx-b, idx-d), got %d", len(s.searchers))
	}
	if _, ok := s.searchers["idx-c"]; ok {
		t.Error("idx-c should have been evicted")
	}
	if _, ok := s.searchers["idx-b"]; !ok {
		t.Error("idx-b should remain")
	}
	if _, ok := s.searchers["idx-d"]; !ok {
		t.Error("idx-d should remain")
	}
}

func TestMCPSearcherCache_EnvBudget(t *testing.T) {
	t.Setenv("GLEANN_SEARCHER_CACHE_BUDGET", "500MB")
	s := NewServer(Config{IndexDir: t.TempDir()})
	defer s.Close()

	if budget := s.searcherBudget(); budget != 500*1024*1024 {
		t.Errorf("expected 500MB from env, got %d", budget)
	}
}
