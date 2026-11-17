package mcp

import (
	"context"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

func freshFixture(t *testing.T) (*Server, gleann.Config, *testBudgetEmbedder) {
	t.Helper()
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	dir := t.TempDir()
	emb := &testBudgetEmbedder{dim: 8}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"

	items := []gleann.Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "banana bread", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "cherry tart dessert", Metadata: map[string]any{"source": "c.md", "chunk_index": 0}},
	}
	b1, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b1.Build(context.Background(), "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	srv := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom", EmbeddingModel: "test-model"})
	srv.embedder = emb
	t.Cleanup(srv.Close)
	return srv, glCfg, emb
}

func expectTop1(t *testing.T, s *gleann.LeannSearcher, texts ...string) {
	t.Helper()
	for _, want := range texts {
		rs, err := s.Search(context.Background(), want, gleann.WithTopK(1), gleann.WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("Search(%q): %v", want, err)
		}
		if len(rs) != 1 || rs[0].Text != want {
			t.Errorf("Search(%q) = %+v; stale or mismatched passage", want, rs)
		}
	}
}

// A CLI sync in another process that only touches the delta (the common
// incremental sync) is absorbed by the cached searcher in place: the main
// vector file stays loaded and the new passage is searchable.
func TestGetSearcher_DeltaSyncRefreshesInPlace(t *testing.T) {
	srv, glCfg, emb := freshFixture(t)
	ctx := context.Background()

	first, release, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if again, err := srv.getSearcher("idx"); err != nil || again != first {
		t.Fatalf("an unchanged index must reuse the cached searcher (err=%v)", err)
	}

	b2, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", []gleann.Item{
		{Text: "date syrup cake and more", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	second, release, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second != first {
		t.Fatal("a delta-only publication must not replace the cached searcher")
	}
	expectTop1(t, second, "cherry tart dessert", "banana bread", "date syrup cake and more")
}

// A publication that replaces the main vector file (a merge renumbers the
// slots) replaces the cached searcher; the old one stays open for the call
// that borrowed it and closes the moment that call releases it.
func TestGetSearcher_MergeReplacesAndClosesAfterLastBorrower(t *testing.T) {
	t.Setenv("GLEANN_DELTA_MIN", "0")
	t.Setenv("GLEANN_DELTA_SHARE", "0")
	srv, glCfg, emb := freshFixture(t)
	ctx := context.Background()

	first, releaseFirst, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	b2, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", []gleann.Item{
		{Text: "date syrup cake and more", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	second, releaseSecond, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if second == first {
		t.Fatal("the cached searcher was not replaced after the main vector file changed")
	}
	expectTop1(t, second, "cherry tart dessert", "banana bread", "date syrup cake and more")

	// The borrower of the retired searcher is still tracked, not closed.
	srv.searcherMu.Lock()
	ref := srv.searcherRefs[first]
	srv.searcherMu.Unlock()
	if ref == nil || ref.refs != 1 || !ref.retired {
		t.Fatalf("the retired searcher must be tracked as borrowed and retired, got %+v", ref)
	}
	// ...and the last release closes it at once (no grace timer).
	releaseFirst()
	srv.searcherMu.Lock()
	_, tracked := srv.searcherRefs[first]
	srv.searcherMu.Unlock()
	if tracked {
		t.Fatal("the retired searcher is still tracked after its last release")
	}
	if _, err := first.Search(ctx, "banana bread", gleann.WithTopK(1), gleann.WithHybridAlpha(1.0)); err == nil {
		t.Fatal("a retired searcher must be closed once its last borrower released it")
	}
}
