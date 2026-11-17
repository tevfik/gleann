package server

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newTestSearcher(t *testing.T, name string, size int64) *gleann.LeannSearcher {
	t.Helper()
	s := gleann.NewSearcher(gleann.DefaultConfig(), nil)
	s.SetEstimatedSize(size)
	return s
}

func TestServerSearcherCache_SizeAwareEviction(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.SearcherCacheBudget = 300 * 1024 * 1024 // 300 MB

	srv := &Server{
		config:       cfg,
		searchers:    make(map[string]*gleann.LeannSearcher),
		searcherRefs: make(map[*gleann.LeannSearcher]*searcherRef),
	}

	// 1. Add index A (100 MB)
	sA := newTestSearcher(t, "idx-a", 100*1024*1024)
	srv.mu.Lock()
	srv.searchers["idx-a"] = sA
	srv.touchSearcherLRULocked("idx-a")
	toClose := srv.enforceSearcherBudgetLocked()
	srv.mu.Unlock()
	if len(toClose) != 0 {
		t.Errorf("expected 0 evicted, got %d", len(toClose))
	}
	if srv.cachedSearcherCount() != 1 {
		t.Fatalf("expected 1 cached searcher, got %d", srv.cachedSearcherCount())
	}

	// 2. Add index B (100 MB)
	sB := newTestSearcher(t, "idx-b", 100*1024*1024)
	srv.mu.Lock()
	srv.searchers["idx-b"] = sB
	srv.touchSearcherLRULocked("idx-b")
	toClose = srv.enforceSearcherBudgetLocked()
	srv.mu.Unlock()
	if len(toClose) != 0 {
		t.Errorf("expected 0 evicted, got %d", len(toClose))
	}
	if srv.cachedSearcherCount() != 2 {
		t.Fatalf("expected 2 cached searchers, got %d", srv.cachedSearcherCount())
	}

	// 3. Add index C (150 MB) -> Total 350 MB > 300 MB budget
	// Least-recently-used idx-a should be evicted.
	sC := newTestSearcher(t, "idx-c", 150*1024*1024)
	srv.mu.Lock()
	srv.searchers["idx-c"] = sC
	srv.touchSearcherLRULocked("idx-c")
	toClose = srv.enforceSearcherBudgetLocked()
	srv.mu.Unlock()

	if len(toClose) != 1 || toClose[0] != sA {
		t.Errorf("expected idx-a to be evicted, got %v", toClose)
	}
	if srv.cachedSearcherCount() != 2 {
		t.Fatalf("expected 2 cached searchers (idx-b and idx-c), got %d", srv.cachedSearcherCount())
	}
	srv.mu.RLock()
	if _, ok := srv.searchers["idx-a"]; ok {
		t.Error("idx-a should not be in cache")
	}
	if _, ok := srv.searchers["idx-b"]; !ok {
		t.Error("idx-b should be in cache")
	}
	if _, ok := srv.searchers["idx-c"]; !ok {
		t.Error("idx-c should be in cache")
	}
	srv.mu.RUnlock()

	// 4. Touch idx-b via acquireSearcher so idx-b is now more recently used than idx-c
	ctx := context.Background()
	_, releaseB, err := srv.acquireSearcher(ctx, "idx-b")
	if err != nil {
		t.Fatalf("acquireSearcher(idx-b): %v", err)
	}
	releaseB()

	// 5. Add index D (100 MB) -> Total = 100(B) + 150(C) + 100(D) = 350 MB > 300 MB
	// Since idx-b was touched, idx-c is now the least-recently used and should be evicted!
	sD := newTestSearcher(t, "idx-d", 100*1024*1024)
	srv.mu.Lock()
	srv.searchers["idx-d"] = sD
	srv.touchSearcherLRULocked("idx-d")
	toClose = srv.enforceSearcherBudgetLocked()
	srv.mu.Unlock()

	if len(toClose) != 1 || toClose[0] != sC {
		t.Errorf("expected idx-c to be evicted after idx-b was touched, got %v", toClose)
	}
	srv.mu.RLock()
	if _, ok := srv.searchers["idx-c"]; ok {
		t.Error("idx-c should have been evicted")
	}
	if _, ok := srv.searchers["idx-b"]; !ok {
		t.Error("idx-b should remain in cache")
	}
	if _, ok := srv.searchers["idx-d"]; !ok {
		t.Error("idx-d should remain in cache")
	}
	srv.mu.RUnlock()
}

func TestServerSearcherCache_EnvBudget(t *testing.T) {
	t.Setenv("GLEANN_SEARCHER_CACHE_BUDGET", "200MB")

	srv := &Server{
		config:       gleann.DefaultConfig(),
		searchers:    make(map[string]*gleann.LeannSearcher),
		searcherRefs: make(map[*gleann.LeannSearcher]*searcherRef),
	}

	if budget := srv.searcherCacheBudget(); budget != 200*1024*1024 {
		t.Errorf("expected budget 200MB from env, got %d", budget)
	}
}
