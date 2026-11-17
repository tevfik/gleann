package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// This file covers the format-2 write and read paths (RFC 003 section 7.1):
// build/search round trip, stable pids across an insertion, per-source sync
// that leaves the other sources untouched, and compaction that keeps vec_slot
// in step with the vector file.

// newFormat2Builder builds through NewBuilder with GLEANN_STORE_FORMAT=2, the
// same path the CLI takes. The literal newTestBuilder builders keep
// storeFormat 0, which means format 1.
func newFormat2Builder(t *testing.T, dir string, dim int) (*LeannBuilder, *testEmbedder) {
	t.Helper()
	initTestHNSW()
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	embedder := &testEmbedder{dim: dim}
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return b, embedder
}

// f2Items are passages whose texts all differ in length: the test embedder's
// vectors depend only on text length, so every passage gets a distinct vector
// and a query equal to a passage text retrieves exactly that passage.
func f2Items() []Item {
	return []Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "apple pie recipe with cinnamon sugar", Metadata: map[string]any{"source": "a.md", "chunk_index": 1}},
		{Text: "banana bread", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "cherry tart dessert", Metadata: map[string]any{"source": "c.md", "chunk_index": 0}},
	}
}

// f2Rows reads every passage of a format-2 store, keyed by text (the fixture
// texts are unique). It exposes the internal ID, pid and vec_slot the
// abstraction hides from searchers.
func f2Rows(t *testing.T, basePath string) map[string]store.Passage {
	t.Helper()
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		t.Fatalf("open format-2 store: %v", err)
	}
	defer st.Close()
	out := make(map[string]store.Passage)
	if err := st.ForEach(context.Background(), func(p store.Passage) error {
		out[p.Text] = p
		return nil
	}); err != nil {
		t.Fatalf("read passages: %v", err)
	}
	return out
}

// f2Meta reads one meta key of a format-2 store.
func f2Meta(t *testing.T, basePath, key string) string {
	t.Helper()
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		t.Fatalf("open format-2 store: %v", err)
	}
	defer st.Close()
	v, err := st.Meta(context.Background(), key)
	if err != nil {
		t.Fatalf("read meta %q: %v", key, err)
	}
	return v
}

func TestFormat2_BuildSearchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	base := filepath.Join(dir, "idx", "idx")
	if StoreFormatOf(base) != 2 {
		t.Fatal("a build with GLEANN_STORE_FORMAT=2 must produce a format-2 index")
	}
	if _, err := os.Stat(base + ".db"); err != nil {
		t.Fatalf("format-2 store missing: %v", err)
	}
	// No bbolt store and no format-1 lexical sidecar for a format-2 index.
	if _, err := os.Stat(base + ".passages.db"); !os.IsNotExist(err) {
		t.Errorf("format-2 build left a bbolt passage store: %v", err)
	}
	if _, err := os.Stat(base + ".lex.db"); !os.IsNotExist(err) {
		t.Errorf("format-2 build left a format-1 lexical sidecar: %v", err)
	}
	meta, err := GetIndexMeta(dir, "idx")
	if err != nil || meta.StoreFormat != 2 {
		t.Errorf("meta store_format = %+v (%v), want 2", meta, err)
	}
	if gen := f2Meta(t, base, "vector_generation"); gen != "1" {
		t.Errorf("vector_generation after build = %q, want 1", gen)
	}

	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()
	if !s.VectorAvailable() {
		t.Fatal("vectors not available after a format-2 build")
	}
	if _, ok := s.scorer.(*FTSScorer); !ok {
		t.Fatalf("format-2 Load must use the persisted lex table, got %T", s.scorer)
	}

	// Vector leg: a query equal to a passage text must retrieve that passage
	// (its vector is the only exact match).
	for _, it := range f2Items() {
		rs, err := s.Search(ctx, it.Text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("vector search %q: %v", it.Text, err)
		}
		if len(rs) != 1 || rs[0].Text != it.Text {
			t.Errorf("vector search %q returned %+v, want the passage itself", it.Text, rs)
		}
	}

	// BM25-only leg through the persisted lex table.
	rs, err := s.SearchBM25(ctx, "banana", 5)
	if err != nil {
		t.Fatalf("SearchBM25: %v", err)
	}
	if len(rs) != 1 || rs[0].Text != "banana bread" {
		t.Errorf("SearchBM25(banana) = %+v, want the banana bread passage", rs)
	}
	// The default hybrid search finds it too.
	rs, err = s.Search(ctx, "banana bread", WithTopK(1))
	if err != nil || len(rs) != 1 || rs[0].Text != "banana bread" {
		t.Errorf("hybrid search = %+v (%v), want the banana bread passage", rs, err)
	}
}

func TestFormat2_StablePIDAcrossInsertion(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	before := f2Rows(t, base)

	// Insert a chunk at the top of a.md: every chunk ordinal of the file
	// shifts, but the unchanged chunks keep their text and must keep their pid.
	newA := []Item{
		{Text: "new leading chunk of the file", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md", "chunk_index": 1}},
		{Text: "apple pie recipe with cinnamon sugar", Metadata: map[string]any{"source": "a.md", "chunk_index": 2}},
	}
	if err := b.UpdateIndex(ctx, "idx", newA, nil); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	after := f2Rows(t, base)
	for _, text := range []string{"apple pie recipe", "apple pie recipe with cinnamon sugar", "banana bread", "cherry tart dessert"} {
		if after[text].PID == "" || after[text].PID != before[text].PID {
			t.Errorf("pid of %q changed: %q -> %q", text, before[text].PID, after[text].PID)
		}
	}
	if got := after["apple pie recipe"].ChunkNo; got != 1 {
		t.Errorf("chunk_no of the shifted chunk = %d, want 1", got)
	}
	if after["new leading chunk of the file"].PID == "" {
		t.Error("the inserted chunk has no pid")
	}
	if len(after) != 5 {
		t.Errorf("store holds %d passages after the insertion, want 5", len(after))
	}
}

func TestFormat2_SyncKeepsOtherSourcesUntouched(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	before := f2Rows(t, base)

	// Change only b.md; a.md and c.md are not part of the update.
	newB := []Item{{Text: "banana bread with walnuts", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}}}
	st, err := b.UpdateIndexWithStats(ctx, "idx", newB, nil)
	if err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	if st.Mode != UpdateCompact || st.Passages != 4 {
		t.Errorf("update stats = %+v, want a compaction to 4 passages", st)
	}
	after := f2Rows(t, base)
	for _, text := range []string{"apple pie recipe", "apple pie recipe with cinnamon sugar", "cherry tart dessert"} {
		if after[text].ID != before[text].ID {
			t.Errorf("internal ID of untouched %q changed: %d -> %d", text, before[text].ID, after[text].ID)
		}
		if after[text].PID != before[text].PID {
			t.Errorf("pid of untouched %q changed: %q -> %q", text, before[text].PID, after[text].PID)
		}
	}
	if _, ok := after["banana bread"]; ok {
		t.Error("the old b.md passage survived the sync")
	}
	if _, ok := after["banana bread with walnuts"]; !ok {
		t.Error("the new b.md passage is missing")
	}

	// Slots stay dense: 4 passages at 0..3, none without a slot.
	db, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count, maxSlot, slotless, err := db.SlotSpan(context.Background())
	if err != nil || count != 4 || maxSlot != 3 || slotless != 0 {
		t.Errorf("slot span after sync = (%d, %d, %d, %v), want (4, 3, 0, nil)", count, maxSlot, slotless, err)
	}
}

func TestFormat2_CompactionKeepsSlotsAligned(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")

	// Remove a.md (slots 0 and 1): the remaining passages are renumbered and
	// the vector file rebuilt, so every slot must still address its own text.
	st, err := b.UpdateIndexWithStats(ctx, "idx", nil, []string{"a.md"})
	if err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	if st.Mode != UpdateCompact || st.Removed != 2 || st.Passages != 2 {
		t.Fatalf("removal stats = %+v, want compact with 2 removed and 2 left", st)
	}
	if gen := f2Meta(t, base, "vector_generation"); gen != "2" {
		t.Errorf("vector_generation after compaction = %q, want 2", gen)
	}

	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, text := range []string{"banana bread", "cherry tart dessert"} {
		rs, err := s.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("vector search %q: %v", text, err)
		}
		if len(rs) != 1 || rs[0].Text != text {
			t.Errorf("after compaction, vector search %q returned %+v: vec_slot points at the wrong passage", text, rs)
		}
	}
	if rs, err := s.SearchBM25(ctx, "apple", 5); err != nil || len(rs) != 0 {
		t.Errorf("removed source still lexically searchable: %+v (%v)", rs, err)
	}
	s.Close()

	// A repair (Compact) renumbers densely again and keeps the alignment.
	if _, err := b.Compact(ctx, "idx"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if gen := f2Meta(t, base, "vector_generation"); gen != "3" {
		t.Errorf("vector_generation after Compact = %q, want 3", gen)
	}
	s2 := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s2.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load after Compact: %v", err)
	}
	defer s2.Close()
	for _, text := range []string{"banana bread", "cherry tart dessert"} {
		rs, err := s2.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil || len(rs) != 1 || rs[0].Text != text {
			t.Errorf("after Compact, vector search %q returned %+v (%v)", text, rs, err)
		}
	}
}

// The doctor's format-2 branch: a fresh index is healthy, a meta.json that
// lost its store_format field is flagged, and a repair restores the field.
func TestFormat2_HealthCheck(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if h.NeedsRepair() || !h.Dense || h.Passages != 4 || h.MaxID != 3 {
		t.Errorf("fresh format-2 index reported unhealthy: %+v", h)
	}

	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.StoreFormat = 0 }); err != nil {
		t.Fatal(err)
	}
	h, err = CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if !h.NeedsRepair() || !h.Repairable {
		t.Fatalf("a meta.json without store_format must be flagged repairable: %+v", h)
	}
	if got := h.Problems[0]; !strings.Contains(got, "does not record store_format 2") {
		t.Errorf("unexpected problem: %q", got)
	}
	if _, err := b.Compact(ctx, "idx"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	h, err = CheckIndexHealth(dir, "idx")
	if err != nil || h.NeedsRepair() {
		t.Errorf("repair must restore the store_format field: %+v (%v)", h, err)
	}
}

// Passages without a vector slot are damage the slot span alone can hide:
// three passages at slots {0, 2} plus one slotless row look contiguous
// (count 3, highest slot 2). Load must still warn, and a repair must make
// every passage vector-addressed and dense again.
func TestFormat2_SlotlessDamageWarnsAndRepairs(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	// Distinct text lengths: the test embedder's vectors depend only on
	// length, so each passage is the unique exact hit for its own text.
	items := []Item{
		{Text: "alpha one", Metadata: map[string]any{"source": "x.md", "chunk_index": 0}},
		{Text: "beta two", Metadata: map[string]any{"source": "y.md", "chunk_index": 0}},
		{Text: "gamma three", Metadata: map[string]any{"source": "z.md", "chunk_index": 0}},
	}
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")

	// Delete y.md (slot 1) without renumbering, then add a lexical-only
	// passage: slots {0, 2} plus one slotless row.
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RemoveSource(ctx, "y.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplySource(ctx, store.SourceDelta{
		Source:   "w.md",
		Passages: []store.NewPassage{{Text: "delta four", Kind: "prose", Tokens: "delta four"}},
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	w := s.Warnings()
	s.Close()
	if len(w) != 1 || !strings.Contains(w[0], "not contiguous") || !strings.Contains(w[0], "gleann index repair idx") {
		t.Fatalf("slotless damage must warn and name the fix: %v", w)
	}
	if h, err := CheckIndexHealth(dir, "idx"); err != nil || !h.NeedsRepair() {
		t.Fatalf("health check must flag the slotless passage: %+v (%v)", h, err)
	}

	// Repair (what gleann index repair runs) makes every passage dense and
	// vector-addressed, including the formerly slotless one.
	if _, err := b.Compact(ctx, "idx"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if h, err := CheckIndexHealth(dir, "idx"); err != nil || h.NeedsRepair() {
		t.Fatalf("still unhealthy after repair: %+v (%v)", h, err)
	}
	s2 := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s2.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if w := s2.Warnings(); len(w) != 0 {
		t.Errorf("a repaired index still warns: %v", w)
	}
	for _, text := range []string{"alpha one", "gamma three", "delta four"} {
		rs, err := s2.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil || len(rs) != 1 || rs[0].Text != text {
			t.Errorf("vector search %q after repair = %+v (%v), want the passage itself", text, rs, err)
		}
	}
	if rs, err := s2.SearchBM25(ctx, "delta", 5); err != nil || len(rs) != 1 || rs[0].Text != "delta four" {
		t.Errorf("the rewritten passage lost its lexical row: %+v (%v)", rs, err)
	}
	if rs, err := s2.SearchBM25(ctx, "beta", 5); err != nil || len(rs) != 0 {
		t.Errorf("removed source still searchable after repair: %+v (%v)", rs, err)
	}
}

func TestFormat2_RemoveIndexRemovesLockAndStore(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// A migration or build leaves the lock file beside the index directory.
	lock, err := store.AcquireLock(filepath.Join(dir, "idx.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveIndex(dir, "idx"); err != nil {
		t.Fatalf("RemoveIndex: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dir, "idx"),
		filepath.Join(dir, "idx.lock"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("RemoveIndex left %s behind: %v", path, err)
		}
	}
}
