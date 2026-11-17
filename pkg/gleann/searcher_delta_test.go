package gleann

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// deltaFixtureText is a passage text of a distinct length n, so the test
// embedder (whose vector depends only on the text length) gives every passage
// its own vector and a query equal to a text retrieves exactly that passage.
func deltaFixtureText(n int) string { return strings.Repeat("x", n) }

// deltaFixture builds a format-2 index of n passages (one per source s<i>.md,
// text length i+3) and returns the base path. It then emulates what an
// incremental sync publishes (RFC 005): the sources in kill lose their
// passages (dead slots), and the passages of the sources in add (length
// 100+i) get new slots from vector_slots and their vectors go to the delta.
func deltaFixture(t *testing.T, dir string, n int, kill []int, add int) (string, *testEmbedder) {
	t.Helper()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{Text: deltaFixtureText(i + 3), Metadata: map[string]any{"source": fmt.Sprintf("s%d.md", i), "chunk_index": 0}}
	}
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	st, err := openFormat2Store(base)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, i := range kill {
		if _, err := st.RemoveSource(ctx, fmt.Sprintf("s%d.md", i)); err != nil {
			t.Fatal(err)
		}
	}
	slots, err := currentVectorSlots(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	delta := newVectorDelta(8, deltaDTypeF32, "l2")
	an := analysis.New("")
	for i := 0; i < add; i++ {
		text := deltaFixtureText(100 + i)
		vec, _ := embedder.ComputeSingle(ctx, text)
		slot := slots + int64(i)
		if _, err := st.ApplySource(ctx, store.SourceDelta{
			Source: fmt.Sprintf("n%d.md", i),
			Passages: []store.NewPassage{{
				Text: text, Kind: "prose", Meta: "{}", VecSlot: &slot,
				Tokens: strings.Join(an.Tokens(text, analysis.Kind("prose")), " "),
			}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := delta.Append(slot, vec); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(base+suffixDelta, delta.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := currentVectorGeneration(ctx, st) + 1
	for k, v := range map[string]string{metaVectorSlots: strconv.FormatInt(slots+int64(add), 10), "vector_generation": strconv.Itoa(gen)} {
		if err := st.SetMeta(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.VectorGeneration = gen; m.NumPassages = n - len(kill) + add }); err != nil {
		t.Fatal(err)
	}
	return base, embedder
}

func loadDeltaSearcher(t *testing.T, dir string, embedder *testEmbedder) *LeannSearcher {
	t.Helper()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A passage that exists only in the delta is found through the vector leg,
// and a dead slot's hit never surfaces.
func TestSearcherDeltaFindsNewAndDropsDead(t *testing.T) {
	dir := t.TempDir()
	_, embedder := deltaFixture(t, dir, 20, []int{4, 5}, 3)
	s := loadDeltaSearcher(t, dir, embedder)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		text := deltaFixtureText(100 + i)
		rs, err := s.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil || len(rs) != 1 || rs[0].Text != text {
			t.Fatalf("delta passage %d: %+v (%v)", i, rs, err)
		}
	}
	// The query of a dead passage's text: its own vector is still in the main
	// file, but the hit must be dropped and the live nearest neighbour served.
	dead := deltaFixtureText(4 + 3)
	rs, err := s.Search(ctx, dead, WithTopK(5), WithHybridAlpha(1.0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 5 {
		t.Fatalf("a search with dead slots must still return k results, got %d", len(rs))
	}
	for _, r := range rs {
		if r.Text == dead || r.Text == deltaFixtureText(5+3) {
			t.Fatalf("a dead passage surfaced: %q", r.Text)
		}
	}
}

// With about 30 % of the slots dead the over-fetch keeps k results.
func TestSearcherOverfetchKeepsK(t *testing.T) {
	dir := t.TempDir()
	var kill []int
	for i := 0; i < 30; i++ { // slots 0..29 of 100: the 30 nearest to the query
		kill = append(kill, i)
	}
	_, embedder := deltaFixture(t, dir, 100, kill, 0)
	s := loadDeltaSearcher(t, dir, embedder)
	st := s.dstate.Load()
	if st == nil || st.dead != 30 || st.live != 70 {
		t.Fatalf("delta state = %+v, want 30 dead of 100", st)
	}
	if got := overfetch(10, st); got < 14 {
		t.Fatalf("overfetch(10) = %d, want pool + 30%%/70%% share", got)
	}
	// The query sits at the dead end: without over-fetch the first 10 hits
	// would all be dead.
	rs, err := s.Search(context.Background(), deltaFixtureText(3), WithTopK(10), WithHybridAlpha(1.0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 10 {
		t.Fatalf("got %d results, want 10", len(rs))
	}
}

// The slot counts: dead = vector_slots - live, whatever the history.
func TestSearcherDeltaStateCounts(t *testing.T) {
	dir := t.TempDir()
	_, embedder := deltaFixture(t, dir, 10, []int{1, 2, 3}, 2)
	s := loadDeltaSearcher(t, dir, embedder)
	st := s.dstate.Load()
	if st == nil || st.delta.Len() != 2 || st.live != 9 || st.dead != 3 {
		t.Fatalf("delta state = %+v, want 2 delta entries, 9 live, 3 dead", st)
	}
}

// A delta-only publication is absorbed by RefreshDelta in place, a main-file
// replacement is not.
func TestSearcherRefreshDelta(t *testing.T) {
	dir := t.TempDir()
	base, embedder := deltaFixture(t, dir, 10, nil, 1)
	s := loadDeltaSearcher(t, dir, embedder)
	ctx := context.Background()
	if c, err := s.Change(ctx); err != nil || c != IndexUnchanged {
		t.Fatalf("Change on a fresh load = %v, %v", c, err)
	}

	// Another process publishes a delta-only change: a new passage, new slot.
	st, err := openFormat2Store(base)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	slots, _ := currentVectorSlots(ctx, st)
	text := deltaFixtureText(200)
	vec, _ := embedder.ComputeSingle(ctx, text)
	d, err := readVectorDelta(base + suffixDelta)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	_ = d.Append(slots, vec)
	if err := os.WriteFile(base+suffixDelta, d.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	slot := slots
	if _, err := st.ApplySource(ctx, store.SourceDelta{Source: "late.md", Passages: []store.NewPassage{{Text: text, Kind: "prose", Meta: "{}", VecSlot: &slot, Tokens: "xxx"}}}); err != nil {
		t.Fatal(err)
	}
	_ = st.SetMeta(ctx, metaVectorSlots, strconv.FormatInt(slots+1, 10))
	_ = st.SetMeta(ctx, "vector_generation", strconv.Itoa(currentVectorGeneration(ctx, st)+1))

	if c, err := s.Change(ctx); err != nil || c != IndexDeltaChanged {
		t.Fatalf("Change after a delta publication = %v, %v; want IndexDeltaChanged", c, err)
	}
	if rs, _ := s.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0)); len(rs) == 1 && rs[0].Text == text {
		t.Fatal("the new passage must be invisible before the refresh")
	}
	if err := s.RefreshDelta(ctx); err != nil {
		t.Fatalf("RefreshDelta: %v", err)
	}
	rs, err := s.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
	if err != nil || len(rs) != 1 || rs[0].Text != text {
		t.Fatalf("after RefreshDelta: %+v (%v)", rs, err)
	}
	if c, _ := s.Change(ctx); c != IndexUnchanged {
		t.Fatalf("Change after RefreshDelta = %v", c)
	}

	// A main-file replacement needs a full reload, and a search that ran
	// against the old file is discarded.
	_ = st.SetMeta(ctx, metaVectorMainGeneration, "99")
	if c, _ := s.Change(ctx); c != IndexFullReload {
		t.Fatalf("Change after a main-file replacement = %v, want IndexFullReload", c)
	}
	if err := s.RefreshDelta(ctx); err == nil {
		t.Fatal("RefreshDelta must refuse when the main file changed")
	}
	if _, err := s.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0)); err != ErrIndexChanged {
		t.Fatalf("Search after a main-file replacement = %v, want ErrIndexChanged", err)
	}
}

// A delta of the wrong dimensions is refused instead of compared against
// vectors of another model.
func TestSearcherRejectsDeltaOfOtherDims(t *testing.T) {
	dir := t.TempDir()
	base, embedder := deltaFixture(t, dir, 5, nil, 1)
	bad := newVectorDelta(4, deltaDTypeF32, "l2")
	_ = bad.Append(5, []float32{1, 2, 3, 4})
	if err := os.WriteFile(base+suffixDelta, bad.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(context.Background(), "idx"); err == nil || !strings.Contains(err.Error(), "-dimensional") {
		t.Fatalf("Load with a foreign-dimension delta = %v, want a dimensions error", err)
	}
}
