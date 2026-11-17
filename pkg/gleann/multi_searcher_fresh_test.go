package gleann

import (
	"context"
	"testing"
)

// A MultiSearcher keeps working when an index is published by another process
// while it is held: a delta-only publication is absorbed in place, a merge
// (new main vector file, renumbered slots) reloads that searcher, and neither
// surfaces ErrIndexChanged to the caller.
func TestMultiSearcherRefreshesStaleSearchers(t *testing.T) {
	dir := t.TempDir()
	b, rec, _ := incrBuilder(t, dir)
	ctx := context.Background()

	ms, err := LoadMultiSearcher(ctx, Config{IndexDir: dir, Backend: "hnsw"}, rec.testEmbedder, []string{"idx"})
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	find := func(text string) {
		t.Helper()
		rs, err := ms.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("Search(%q): %v", text, err)
		}
		if len(rs) != 1 || rs[0].Text != text {
			t.Fatalf("Search(%q) = %+v", text, rs)
		}
	}
	find("banana bread")

	// A delta-only sync.
	if err := b.UpdateIndex(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, nil); err != nil {
		t.Fatal(err)
	}
	find("date syrup cake")

	// A merge: the main file is replaced.
	b.limits = incrementalLimits{}
	if err := b.UpdateIndex(ctx, "idx", []Item{{Text: "elderberry jam spread thick", Metadata: map[string]any{"source": "e.md", "chunk_index": 0}}}, []string{"a.md"}); err != nil {
		t.Fatal(err)
	}
	find("elderberry jam spread thick")
	find("cherry tart dessert")
}
