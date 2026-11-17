package gleann

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// A loaded searcher notices a publication of another handle (Stale), a
// search that straddles the publication is refused instead of returning
// passages resolved through renumbered slots (ErrIndexChanged), and Refresh
// reloads in place so the next search is right.
func TestSearcher_FreshnessAcrossPublication(t *testing.T) {
	t.Setenv("GLEANN_DEAD_MIN", "0")
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if stale, err := s.Stale(ctx); stale || err != nil {
		t.Fatalf("fresh searcher: stale=%v err=%v", stale, err)
	}

	b2, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", f2UpdateItems(), []string{"b.md"}); err != nil {
		t.Fatal(err)
	}

	if stale, err := s.Stale(ctx); !stale || err != nil {
		t.Fatalf("after another handle's sync: stale=%v err=%v", stale, err)
	}
	if _, err := s.Search(ctx, "cherry tart dessert", WithTopK(1), WithHybridAlpha(1.0)); !errors.Is(err, ErrIndexChanged) {
		t.Fatalf("Search over renumbered slots = %v, want ErrIndexChanged", err)
	}
	reloaded, err := s.Refresh(ctx)
	if err != nil || !reloaded {
		t.Fatalf("Refresh = %v, %v", reloaded, err)
	}
	for _, want := range []string{"apricot jam preserves", "cherry tart dessert", "date syrup cake"} {
		rs, err := s.Search(ctx, want, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil || len(rs) != 1 || rs[0].Text != want {
			t.Errorf("Search(%q) = %+v, %v", want, rs, err)
		}
	}
	if reloaded, err := s.Refresh(ctx); reloaded || err != nil {
		t.Errorf("Refresh of a fresh searcher = %v, %v", reloaded, err)
	}
}

// Graph rows written by another handle make a searcher stale (the attach
// decision is taken at Load) without any vector change, and a vector-only
// Search is not refused by a graph change.
func TestSearcher_FreshnessGraphChange(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.GraphDB() != nil {
		t.Fatal("no graph yet")
	}

	st, err := store.Open(filepath.Join(dir, "idx", "idx.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = graphstore.New(st).ApplyFileDelta(ctx, graphstore.FileDelta{
		File: "a.go", GraphHash: "h",
		Symbols: []graphstore.Symbol{{FQN: "m.A", Kind: "function", Name: "A", StartLine: 1, EndLine: 2}},
	})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}

	if stale, err := s.Stale(ctx); !stale || err != nil {
		t.Fatalf("after a graph write: stale=%v err=%v", stale, err)
	}
	if _, err := s.Search(ctx, "banana bread", WithTopK(1), WithHybridAlpha(1.0)); err != nil {
		t.Fatalf("a graph change must not refuse a search: %v", err)
	}
	if reloaded, err := s.Refresh(ctx); !reloaded || err != nil {
		t.Fatalf("Refresh = %v, %v", reloaded, err)
	}
	if s.GraphDB() == nil {
		t.Fatal("graph not attached after Refresh")
	}
}

// A reader that opens the index between the publication's commit and its
// rename rolls the publication forward itself (recoverVectorPublication); the
// publisher's own rename then finds the staging file gone and must finish the
// generation instead of failing.
func TestPublish_ReaderRolledForwardFirst(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	b.crashHook = func(point string) error {
		if point != crashAfterCommit {
			return nil
		}
		// A reader opens the index right now.
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
		if err := s.Load(ctx, "idx"); err != nil {
			return err
		}
		return s.Close()
	}
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err != nil {
		t.Fatalf("UpdateIndex with a concurrent reader: %v", err)
	}
	if got := f2Meta(t, filepath.Join(dir, "idx", "idx"), "vector_generation"); got != "2" {
		t.Errorf("vector_generation = %q, want 2", got)
	}
	f2SelfRetrieval(t, dir, embedder, f2UpdateTexts())
}
