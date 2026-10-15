package gleann

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// failingEmbedder fails every Compute once armed.
type failingEmbedder struct {
	testEmbedder
	fail bool
}

func (e *failingEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if e.fail {
		return nil, errors.New("embedder offline")
	}
	return e.testEmbedder.Compute(ctx, texts)
}

func buildFour(t *testing.T) (*LeannBuilder, string, string) {
	t.Helper()
	dir := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)
	items := []Item{
		{Text: "alpha from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "beta from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "gamma from file-b", Metadata: map[string]any{"source": "b.md"}},
		{Text: "delta from file-c", Metadata: map[string]any{"source": "c.md"}},
	}
	if err := builder.Build(context.Background(), "idx", items); err != nil {
		t.Fatal(err)
	}
	return builder, dir, filepath.Join(dir, "idx", "idx")
}

func passageTexts(t *testing.T, base string) (texts []string, dense bool) {
	t.Helper()
	pm := NewReadOnlyPassageManager(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	defer pm.Close()
	dense, err := pm.IsDense()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pm.All() {
		texts = append(texts, p.Text)
	}
	return texts, dense
}

// Appending must not renumber anything: existing passages keep their IDs.
func TestUpdateIndex_AppendStaysInPlace(t *testing.T) {
	builder, _, base := buildFour(t)
	if err := builder.UpdateIndex(context.Background(), "idx", []Item{{Text: "epsilon from file-d", Metadata: map[string]any{"source": "d.md"}}}, nil); err != nil {
		t.Fatal(err)
	}
	pm := NewReadOnlyPassageManager(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	defer pm.Close()
	want := []string{"alpha from file-a", "beta from file-a", "gamma from file-b", "delta from file-c", "epsilon from file-d"}
	for i, w := range want {
		p, err := pm.Get(int64(i))
		if err != nil || p.Text != w {
			t.Errorf("passage %d = %q (%v), want %q", i, p.Text, err, w)
		}
	}
}

// An index that earlier in-place removals left with gaps in its passage IDs is
// repaired by the next update instead of being extended further out of step.
func TestUpdateIndex_HealsAnIndexWithGaps(t *testing.T) {
	builder, _, base := buildFour(t)
	pm := NewPassageManager(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.RemoveBySource([]string{"a.md"}); err != nil { // the old in-place removal
		t.Fatal(err)
	}
	_ = pm.Close()
	if _, dense := passageTexts(t, base); dense {
		t.Fatal("fixture should have gaps")
	}

	if err := builder.UpdateIndex(context.Background(), "idx", []Item{{Text: "epsilon from file-d", Metadata: map[string]any{"source": "d.md"}}}, nil); err != nil {
		t.Fatal(err)
	}
	texts, dense := passageTexts(t, base)
	if !dense || len(texts) != 3 {
		t.Errorf("after healing: dense=%v texts=%v, want dense with 3 passages", dense, texts)
	}
}

// If the embedder fails, nothing on disk may have changed.
func TestUpdateIndex_EmbedFailureLeavesIndexUntouched(t *testing.T) {
	builder, _, base := buildFour(t)
	fe := &failingEmbedder{testEmbedder: testEmbedder{dim: 8}, fail: true}
	builder.embedder = fe

	// The bbolt file's bytes can change merely by opening it, so compare the
	// passages' content; the vector index is a plain file, compare its bytes.
	snap := func() (idx []byte, passages []string) {
		i, err := os.ReadFile(base + ".index")
		if err != nil {
			t.Fatal(err)
		}
		texts, _ := passageTexts(t, base)
		return i, texts
	}
	idx0, texts0 := snap()

	for name, call := range map[string]func() error{
		"compacting removal": func() error {
			return builder.UpdateIndex(context.Background(), "idx", []Item{{Text: "new", Metadata: map[string]any{"source": "a.md"}}}, []string{"a.md"})
		},
		"append": func() error {
			return builder.UpdateIndex(context.Background(), "idx", []Item{{Text: "new", Metadata: map[string]any{"source": "z.md"}}}, nil)
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("%s: expected an error from the offline embedder", name)
		}
		idx1, texts1 := snap()
		if !bytes.Equal(idx0, idx1) || !reflect.DeepEqual(texts0, texts1) {
			t.Errorf("%s: index files changed although the update failed", name)
		}
	}
}

// Removing the last passages empties the index: the removed text is gone and a
// later add builds it afresh (the backends cannot hold an empty index).
func TestUpdateIndex_EmptyingAndRefilling(t *testing.T) {
	builder, _, base := buildFour(t)
	ctx := context.Background()
	if err := builder.UpdateIndex(ctx, "idx", nil, []string{"a.md", "b.md", "c.md"}); err != nil {
		t.Fatalf("emptying: %v", err)
	}
	if texts, _ := passageTexts(t, base); len(texts) != 0 {
		t.Fatalf("emptied index still holds %v", texts)
	}
	if _, err := os.Stat(base + ".index"); err == nil {
		t.Error("vector file of an emptied index should be gone")
	}

	if err := builder.AddToIndex(ctx, "idx", []Item{{Text: "fresh start", Metadata: map[string]any{"source": "n.md"}}}); err != nil {
		t.Fatalf("refilling: %v", err)
	}
	texts, dense := passageTexts(t, base)
	if len(texts) != 1 || texts[0] != "fresh start" || !dense {
		t.Errorf("after refill: %v dense=%v", texts, dense)
	}
	meta, err := GetIndexMeta(builder.config.IndexDir, "idx")
	if err != nil || !meta.VectorReady || meta.NumPassages != 1 {
		t.Errorf("meta after refill: %+v (%v)", meta, err)
	}
}

func TestUpdateIndexWithStats_ReportsTheModeAndTheCost(t *testing.T) {
	ctx := context.Background()
	item := func(text, src string) Item { return Item{Text: text, Metadata: map[string]any{"source": src}} }

	t.Run("noop", func(t *testing.T) {
		builder, _, _ := buildFour(t)
		st, err := builder.UpdateIndexWithStats(ctx, "idx", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode != UpdateNoop || st.Embedded != 0 || st.Passages != 4 {
			t.Errorf("noop stats: %+v", st)
		}
	})
	t.Run("append is in place and embeds only the new text", func(t *testing.T) {
		builder, _, _ := buildFour(t)
		st, err := builder.UpdateIndexWithStats(ctx, "idx", []Item{item("epsilon", "d.md")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode != UpdateInPlace || st.Added != 1 || st.Removed != 0 || st.Embedded != 1 || st.Passages != 5 {
			t.Errorf("in-place stats: %+v", st)
		}
		if st.Elapsed <= 0 {
			t.Errorf("elapsed not recorded: %+v", st)
		}
	})
	t.Run("replacing a file compacts and re-embeds everything", func(t *testing.T) {
		builder, _, _ := buildFour(t)
		st, err := builder.UpdateIndexWithStats(ctx, "idx", []Item{item("alpha v2", "a.md")}, []string{"a.md"})
		if err != nil {
			t.Fatal(err)
		}
		// 4 passages, 2 of them from a.md: 2 removed, 2 kept + 1 new = 3 embedded.
		if st.Mode != UpdateCompact || st.Removed != 2 || st.Added != 1 || st.Embedded != 3 || st.Passages != 3 {
			t.Errorf("compact stats: %+v", st)
		}
	})
	t.Run("removing everything empties the index", func(t *testing.T) {
		builder, _, _ := buildFour(t)
		st, err := builder.UpdateIndexWithStats(ctx, "idx", nil, []string{"a.md", "b.md", "c.md"})
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode != UpdateEmptied || st.Removed != 4 || st.Passages != 0 {
			t.Errorf("emptied stats: %+v", st)
		}
	})
	t.Run("a failing embedder still reports what was attempted", func(t *testing.T) {
		builder, _, _ := buildFour(t)
		builder.embedder = &failingEmbedder{testEmbedder: testEmbedder{dim: 8}, fail: true}
		st, err := builder.UpdateIndexWithStats(ctx, "idx", []Item{item("x", "z.md")}, nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		if st.Embedded != 1 {
			t.Errorf("attempted embeddings not recorded: %+v", st)
		}
	})
}

func TestCompact_ReportsStats(t *testing.T) {
	builder, _, _ := buildFour(t)
	st, err := builder.Compact(context.Background(), "idx")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != UpdateCompact || st.Embedded != 4 || st.Passages != 4 || st.Removed != 0 {
		t.Errorf("Compact stats: %+v", st)
	}
}

// reportingEmbedder reports progress the way the real embedders do: through
// SetProgressCallback, as it finishes batches.
type reportingEmbedder struct {
	testEmbedder
	cb func(done, total int)
}

func (e *reportingEmbedder) SetProgressCallback(cb func(done, total int)) { e.cb = cb }

func (e *reportingEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	out, err := e.testEmbedder.Compute(ctx, texts)
	if e.cb != nil {
		half := len(texts) / 2
		e.cb(half, len(texts))
		e.cb(len(texts), len(texts))
	}
	return out, err
}

// Compacting re-embeds every remaining passage, so it reports progress like a
// build does.
func TestCompact_ReportsProgress(t *testing.T) {
	builder, _, _ := buildFour(t)
	builder.embedder = &reportingEmbedder{testEmbedder: testEmbedder{dim: 8}}
	type tick struct {
		phase       string
		done, total int
	}
	var ticks []tick
	builder.SetProgressCallback(func(phase string, done, total int) { ticks = append(ticks, tick{phase, done, total}) })

	if _, err := builder.Compact(context.Background(), "idx"); err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 || ticks[0] != (tick{"vector_index", 2, 4}) || ticks[1] != (tick{"vector_index", 4, 4}) {
		t.Errorf("progress ticks = %+v", ticks)
	}
}
