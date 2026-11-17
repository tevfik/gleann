package gleann

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type textLogEmbedder struct {
	*testEmbedder
	texts []string
}

func (r *textLogEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	r.texts = append(r.texts, texts...)
	return r.testEmbedder.Compute(ctx, texts)
}

func incrBuilder(t *testing.T, dir string) (*LeannBuilder, *textLogEmbedder, string) {
	t.Helper()
	b, e := newFormat2Builder(t, dir, 8)
	rec := &textLogEmbedder{testEmbedder: e}
	b.embedder = rec
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	rec.texts = nil
	return b, rec, filepath.Join(dir, "idx", "idx")
}

// Add, change and delete embed only the changed chunks and leave every other
// slot alone; a dead slot is never handed out again.
func TestIncrementalUpdateEmbedsOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	b, rec, base := incrBuilder(t, dir)
	ctx := context.Background()
	before := f2Rows(t, base)

	// add
	st, err := b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, nil)
	if err != nil || st.Mode != UpdateInPlace || st.Embedded != 1 || len(rec.texts) != 1 {
		t.Fatalf("add: %+v %v texts=%v", st, err, rec.texts)
	}
	if got := f2Meta(t, base, metaVectorSlots); got != "5" {
		t.Fatalf("vector_slots after add = %q, want 5", got)
	}
	// delete c.md: embeds nothing
	rec.texts = nil
	st, err = b.UpdateIndexWithStats(ctx, "idx", nil, []string{"c.md"})
	if err != nil || st.Mode != UpdateInPlace || len(rec.texts) != 0 || st.Removed != 1 {
		t.Fatalf("delete: %+v %v texts=%v", st, err, rec.texts)
	}
	// add again: the dead slot 3 is not reused
	st, err = b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "elderberry jam spread thick", Metadata: map[string]any{"source": "e.md", "chunk_index": 0}}}, nil)
	if err != nil || st.Mode != UpdateInPlace {
		t.Fatalf("re-add: %+v %v", st, err)
	}
	after := f2Rows(t, base)
	if got := *after["elderberry jam spread thick"].VecSlot; got != 5 {
		t.Errorf("new slot = %d, want 5 (dead slots are never reused)", got)
	}
	if got := f2Meta(t, base, metaVectorSlots); got != "6" {
		t.Errorf("vector_slots = %q, want 6", got)
	}
	for _, text := range []string{"apple pie recipe", "banana bread"} {
		if *after[text].VecSlot != *before[text].VecSlot {
			t.Errorf("slot of untouched %q changed", text)
		}
	}
	f2SelfRetrieval(t, dir, rec.testEmbedder, []string{"apple pie recipe", "banana bread", "date syrup cake", "elderberry jam spread thick"})
	f2Healthy(t, dir)
}

// An update that crosses a limit merges: the main file is rebuilt from the
// stored vectors, only the new texts are embedded, and the slots are dense.
func TestIncrementalLimitMergesWithoutReembedding(t *testing.T) {
	dir := t.TempDir()
	b, rec, base := incrBuilder(t, dir)
	ctx := context.Background()
	b.limits = incrementalLimits{deltaMin: 0, deltaShare: 0, deadShare: 1, deadSharePruned: 1}
	st, err := b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, nil)
	if err != nil || st.Mode != UpdateMerge || st.Embedded != 1 || len(rec.texts) != 1 {
		t.Fatalf("merge: %+v %v texts=%v", st, err, rec.texts)
	}
	if got := f2Meta(t, base, metaVectorSlots); got != "5" {
		t.Errorf("vector_slots after merge = %q, want 5", got)
	}
	if got := f2Meta(t, base, metaVectorMainGeneration); got == "1" || got == "" {
		t.Errorf("a merge must replace the main file generation, got %q", got)
	}

	// Compaction: dead slots are removed and the slots are dense again.
	b.limits = incrementalLimits{deltaMin: 100, deltaShare: 1, deadShare: 0.1, deadSharePruned: 0.1}
	rec.texts = nil
	st, err = b.UpdateIndexWithStats(ctx, "idx", nil, []string{"a.md"})
	if err != nil || st.Mode != UpdateMerge || len(rec.texts) != 0 {
		t.Fatalf("compaction: %+v %v texts=%v", st, err, rec.texts)
	}
	if got := f2Meta(t, base, metaVectorSlots); got != "3" {
		t.Errorf("vector_slots after compaction = %q, want 3 (dense again)", got)
	}
	f2SelfRetrieval(t, dir, rec.testEmbedder, []string{"banana bread", "cherry tart dessert", "date syrup cake"})
	f2Healthy(t, dir)
}

// Vectors that sit in the delta survive a merge too.
func TestIncrementalMergeKeepsDeltaVectors(t *testing.T) {
	dir := t.TempDir()
	b, rec, _ := incrBuilder(t, dir)
	ctx := context.Background()
	if _, err := b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, nil); err != nil {
		t.Fatal(err)
	}
	b.limits = incrementalLimits{deltaMin: 0, deltaShare: 0, deadShare: 1, deadSharePruned: 1}
	rec.texts = nil
	st, err := b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "elderberry jam spread thick", Metadata: map[string]any{"source": "e.md", "chunk_index": 0}}}, nil)
	if err != nil || st.Mode != UpdateMerge || len(rec.texts) != 1 {
		t.Fatalf("merge: %+v %v texts=%v", st, err, rec.texts)
	}
	f2SelfRetrieval(t, dir, rec.testEmbedder, []string{"apple pie recipe", "banana bread", "cherry tart dessert", "date syrup cake", "elderberry jam spread thick"})
	f2Healthy(t, dir)
}

// A crash at each publication point of a delta-only update leaves an index
// that recovers to the new state with every hit resolving to its own text.
func TestIncrementalPublicationCrashPoints(t *testing.T) {
	for _, point := range []string{crashBeforeCommit, crashAfterCommit, crashAfterRename} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			b, rec, base := incrBuilder(t, dir)
			b.crashHook = crashAt(point)
			ctx := context.Background()
			if err := b.UpdateIndex(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, []string{"c.md"}); err == nil {
				t.Fatal("UpdateIndex must fail at the injected crash")
			}
			want := []string{"apple pie recipe", "banana bread"}
			if point != crashBeforeCommit {
				want = append(want, "date syrup cake")
			} else {
				want = append(want, "cherry tart dessert")
			}
			f2SelfRetrieval(t, dir, rec.testEmbedder, want)
			f2Healthy(t, dir)
			for _, suffix := range []string{".index.tmp", ".delta.tmp"} {
				if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
					t.Errorf("%s survived recovery: %v", suffix, err)
				}
			}
		})
	}
}

// A crash at any point of a merge publication (two files, the main one first)
// recovers to the new state with every hit resolving to its own text.
func TestIncrementalMergeCrashPoints(t *testing.T) {
	for _, point := range []string{crashBeforeCommit, crashAfterCommit, crashAfterMergeCommit, crashAfterRenameMain, crashAfterRename, crashAfterFinish} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			b, rec, _ := incrBuilder(t, dir)
			b.limits = incrementalLimits{deltaMin: 0, deltaShare: 0, deadShare: 1, deadSharePruned: 1}
			b.crashHook = crashAt(point)
			ctx := context.Background()
			_ = b.UpdateIndex(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, []string{"c.md"})
			want := []string{"apple pie recipe", "banana bread"}
			if point != crashBeforeCommit {
				want = append(want, "date syrup cake")
			} else {
				want = append(want, "cherry tart dessert")
			}
			f2SelfRetrieval(t, dir, rec.testEmbedder, want)
			f2Healthy(t, dir)
		})
	}
}
