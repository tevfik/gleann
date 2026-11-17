package gleann

import (
	"context"
	"strings"
	"testing"
)

// A passage that only the delta holds is found by its words as well: the
// lexical leg reads the passage store, which the incremental publication
// updated in the same transaction.
func TestIncrementalDeltaPassageFoundLexically(t *testing.T) {
	t.Setenv("GLEANN_LEXICAL", "fts5")
	t.Setenv("GLEANN_LEXICAL_OPTS", "and,df=0.2")
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	st, err := b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "the zorblaxquintessence paragraph was added later on", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != UpdateInPlace {
		t.Fatalf("mode = %s, want an in-place update", st.Mode)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, alpha := range []float32{0.5, 0.7} {
		rs, err := s.Search(ctx, "zorblaxquintessence", WithTopK(3), WithHybridAlpha(alpha))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range rs {
			found = found || strings.Contains(r.Text, "zorblaxquintessence")
		}
		if !found {
			t.Errorf("alpha %v: the delta passage was not found by its word: %v", alpha, rs)
		}
	}
}
