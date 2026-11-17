package server

import (
	"context"
	"errors"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// G4, delta publication: a CLI sync that only touches the delta file is
// absorbed by the cached searcher in place (the main vector file stays loaded)
// and the next search sees the new passage.
func TestSearcherCache_DeltaSyncRefreshesInPlace(t *testing.T) {
	s, h, cfg, emb := newFreshServer(t)
	ctx := context.Background()

	cached, release, err := s.acquireSearcher(ctx, "idx")
	if err != nil {
		t.Fatal(err)
	}
	release()

	b2, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", []gleann.Item{
		{Text: "date syrup cake and more", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	for _, want := range []string{"cherry tart dessert", "banana bread", "date syrup cake and more"} {
		if r := searchTop(t, h, "idx", want); len(r) != 1 || r[0].Text != want {
			t.Errorf("search %q after a delta sync = %+v", want, r)
		}
	}
	after, release2, err := s.acquireSearcher(ctx, "idx")
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if after != cached {
		t.Fatal("a delta-only publication must refresh the cached searcher in place, not replace it")
	}
}

// G4, merge publication: a reader that loaded before a sync replaced the main
// vector file never returns a wrong passage. It reports ErrIndexChanged (or the
// still-correct text) and the cache serves a reloaded searcher afterwards.
func TestSearcherCache_ReaderHeldAcrossMergeNeverReturnsWrongText(t *testing.T) {
	t.Setenv("GLEANN_DELTA_MIN", "0")
	t.Setenv("GLEANN_DELTA_SHARE", "0")
	s, h, cfg, emb := newFreshServer(t)
	ctx := context.Background()

	old, releaseOld, err := s.acquireSearcher(ctx, "idx")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOld()

	b2, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", []gleann.Item{
		{Text: "date syrup cake and more", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	for _, want := range []string{"cherry tart dessert", "banana bread"} {
		rs, err := old.Search(ctx, want, gleann.WithTopK(1), gleann.WithHybridAlpha(1.0))
		switch {
		case errors.Is(err, gleann.ErrIndexChanged):
		case err != nil:
			t.Fatalf("old reader, search %q: %v", want, err)
		case len(rs) == 1 && rs[0].Text != want:
			t.Errorf("old reader returned a wrong passage for %q: %+v", want, rs)
		}
	}
	for _, want := range []string{"cherry tart dessert", "banana bread", "date syrup cake and more"} {
		if r := searchTop(t, h, "idx", want); len(r) != 1 || r[0].Text != want {
			t.Errorf("search %q through the cache after a merge = %+v", want, r)
		}
	}
}
