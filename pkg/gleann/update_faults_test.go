package gleann

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// failingBuildBackend fails to build the vector file.
type failingBuildBackend struct{ BackendBuilder }

func (failingBuildBackend) Build(context.Context, [][]float32) ([]byte, error) {
	return nil, errors.New("backend refused")
}

// updateItems changes b.md and adds z.md.
func updateItems() []Item {
	return []Item{
		{Text: "banana bread with walnuts", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "zucchini loaf", Metadata: map[string]any{"source": "z.md", "chunk_index": 0}},
	}
}

// A full rebuild of the vector file refuses what the embedder returned when it
// is unusable, and leaves the published index untouched.
func TestUpdateFullRebuildRejectsBadEmbeddings(t *testing.T) {
	cases := []struct {
		name string
		emb  *badEmbedder
		want string
	}{
		{"embedder error", &badEmbedder{err: errors.New("provider down")}, "compute embeddings: provider down"},
		{"too few vectors", &badEmbedder{vecs: func(n int) [][]float32 { return make([][]float32, n-1) }}, "vectors for"},
		{"zero dimensions", &badEmbedder{vecs: vecsOfDims(0)}, "zero-dimensional"},
		{"other dimensions than the index", &badEmbedder{vecs: vecsOfDims(4)}, "dimension"},
		{"ragged", &badEmbedder{vecs: vecsOfDims(8, 8, 3)}, "embedding row 2 has 3 dims"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, b := fxBuilt(t)
			b.embedder = tc.emb
			_, err := b.Compact(context.Background(), "idx")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := f2Meta(t, base, "vector_generation"); got != "1" {
				t.Errorf("vector_generation = %q after a failed update, want 1", got)
			}
			if h, err := CheckIndexHealth(dir, "idx"); err != nil || h.NeedsRepair() {
				t.Errorf("a failed update must leave a healthy index: %+v, %v", h, err)
			}
		})
	}
}

func TestUpdateFullRebuildReportsBackendFailure(t *testing.T) {
	_, _, b := fxBuilt(t)
	b.backend = failingBuildBackend{b.backend}
	_, err := b.Compact(context.Background(), "idx")
	if err == nil || !strings.Contains(err.Error(), "build index: backend refused") {
		t.Fatalf("err = %v", err)
	}
}

// An update that the incremental path hands back (here: a passage without a
// slot) is a noop without changes, and an update that removes every source
// empties the index.
func TestUpdateNoopAndEmptied(t *testing.T) {
	_, base, b := fxBuilt(t)
	fxExec(t, base, `UPDATE passages SET vec_slot = NULL WHERE source = 'c.md'`)
	ctx := context.Background()
	st, err := b.UpdateIndexWithStats(ctx, "idx", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != UpdateNoop || st.Passages != 4 {
		t.Fatalf("stats = %+v, want a noop over 4 passages", st)
	}
	st, err = b.UpdateIndexWithStats(ctx, "idx", nil, []string{"a.md", "b.md", "c.md"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != UpdateEmptied || st.Removed != 4 {
		t.Fatalf("stats = %+v, want an emptied index", st)
	}
	if _, err := os.Stat(base + ".index"); !os.IsNotExist(err) {
		t.Errorf("the vector file of an emptied index must be removed: %v", err)
	}
}

func TestUpdateRefusesAnIndexWithoutItsDatabase(t *testing.T) {
	_, base, b := fxBuilt(t)
	if err := os.Remove(base + ".db"); err != nil {
		t.Fatal(err)
	}
	err := b.UpdateIndex(context.Background(), "idx", updateItems(), nil)
	if err == nil || !strings.Contains(err.Error(), "record store") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateReportsAnUnrecoverablePublication(t *testing.T) {
	_, base, b := fxBuilt(t)
	st := fxOpen(t, base)
	ctx := context.Background()
	if err := st.SetMeta(ctx, "pending_vector_file", "idx.index.tmp"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(ctx, "pending_vector_generation", "junk"); err != nil {
		t.Fatal(err)
	}
	err := b.UpdateIndex(ctx, "idx", updateItems(), nil)
	if err == nil || !strings.Contains(err.Error(), "recovering interrupted vector publication") {
		t.Fatalf("err = %v", err)
	}
}

// Each database step of the publication transaction aborts the update with
// its own error and the index stays as it was.
func TestUpdatePublicationTransactionFailures(t *testing.T) {
	cases := []struct {
		name   string
		ddl    string
		items  []Item
		remove []string
		want   string
	}{
		{"removing a dropped source", fxFail("_del", "DELETE", "passages", "OLD.source = 'a.md'"), nil, []string{"a.md"}, `removing source "a.md"`},
		{"removing a changed source", fxFail("_del", "DELETE", "passages", "OLD.source = 'b.md'"), updateItems(), nil, `removing source "b.md"`},
		{"renumbering the slots", fxFail("_upd", "UPDATE", "passages", ""), updateItems(), nil, "reassigning slots"},
		{"writing a new source", fxFail("_ins", "INSERT", "passages", "NEW.source = 'z.md'"), updateItems(), nil, `applying source "z.md"`},
		{"slotless source", fxFail("_ins", "INSERT", "passages", "NEW.source = 'c.md'"), updateItems(), nil, `assigning slots to source "c.md"`},
		{"slot high-water mark", failMeta(metaVectorSlots), updateItems(), nil, "vector_slots"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, b := fxBuilt(t)
			// A slotless passage sends the update down the full rebuild path.
			fxExec(t, base, `UPDATE passages SET vec_slot = NULL WHERE source = 'c.md'`, tc.ddl)
			err := b.UpdateIndex(context.Background(), "idx", tc.items, tc.remove)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := f2Meta(t, base, "pending_vector_file"); got != "" {
				t.Errorf("a rolled-back publication left pending_vector_file = %q", got)
			}
			if got := f2Meta(t, base, "vector_generation"); got != "1" {
				t.Errorf("vector_generation = %q after a rolled-back update, want 1", got)
			}
			_ = dir
		})
	}
}

// Slotless passages (damage left by a lexical-only rebuild) get their dense
// slot from a forced rebuild, so the repaired index passes doctor.
func TestUpdateRepairsSlotlessPassages(t *testing.T) {
	dir, base, b := fxBuilt(t)
	fxExec(t, base, `UPDATE passages SET vec_slot = NULL WHERE source = 'c.md'`)
	if _, err := b.Compact(context.Background(), "idx"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	for text, p := range f2Rows(t, base) {
		if p.VecSlot == nil {
			t.Errorf("passage %q still has no slot after the repair", text)
		}
	}
	f2Healthy(t, dir)
}

// emptyFormat2 reports each step that fails.
func TestEmptyFormat2Failures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, base string)
		ctx   context.Context
		want  string
	}{
		{"listing files", nil, fxCancelled(), "listing files"},
		{"removing a source", func(t *testing.T, base string) {
			fxExec(t, base, fxFail("_del", "DELETE", "passages", "OLD.source = 'b.md'"))
		}, context.Background(), `removing source "`},
		{"vector file cannot be removed", func(t *testing.T, base string) {
			if err := os.Remove(base + ".index"); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(base+".index", "x"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, context.Background(), "remove vector file .index"},
		{"recording the generation", func(t *testing.T, base string) {
			fxExec(t, base, failMeta("vector_generation"))
		}, context.Background(), "setting vector_generation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, base, b := fxBuilt(t)
			st := fxOpen(t, base)
			if tc.setup != nil {
				tc.setup(t, base)
			}
			err := b.emptyFormat2(tc.ctx, "idx", base, st, &UpdateStats{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("sourceless passages", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		st := fxOpen(t, base)
		fxExec(t, base, fxFail("_del", "DELETE", "passages", "OLD.source = ''"),
			`INSERT INTO passages (pid, source, chunk_no, kind, text, meta) VALUES ('p-x', '', 0, 'text', 'orphan', '')`)
		err := b.emptyFormat2(context.Background(), "idx", base, st, &UpdateStats{})
		if err == nil || !strings.Contains(err.Error(), "removing sourceless passages") {
			t.Fatalf("err = %v", err)
		}
	})
}

// A recorded vector generation that meta.json has not caught up with (a crash
// between the finish step and the meta.json write) is synced on the next open.
func TestRecoverSyncsStaleMetaJSON(t *testing.T) {
	dir, base, _ := fxBuilt(t)
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.VectorGeneration = 0 }); err != nil {
		t.Fatal(err)
	}
	if err := recoverVectorPublication(context.Background(), fxOpen(t, base), base); err != nil {
		t.Fatal(err)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil || m.VectorGeneration != 1 || !m.VectorReady {
		t.Fatalf("meta.json after recovery = %+v (%v), want generation 1, vector ready", m, err)
	}
}

// sourceFate classifies a source that no longer has passages.
func TestSourceFate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kept.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	notDir := filepath.Join(dir, "kept.md") // a file used as a directory: stat fails with ENOTDIR
	cases := []struct {
		name, source, sourceDir string
		want                    store.SourceFate
	}{
		{"empty source", "", dir, store.SourceGone},
		{"relative without a source dir", "kept.md", "", store.SourceUnknown},
		{"kept", "kept.md", dir, store.SourceKept},
		{"gone", "missing.md", dir, store.SourceGone},
		{"absolute", filepath.Join(dir, "kept.md"), "", store.SourceKept},
		{"not examinable", "child.md", notDir, store.SourceUnknown},
	}
	for _, tc := range cases {
		if tc.name == "not examinable" && runtime.GOOS == "windows" {
			continue // windows reports a path below a file as not found (ERROR_PATH_NOT_FOUND)
		}
		got := sourceFate(tc.source, tc.sourceDir)
		if got != tc.want {
			t.Errorf("%s: sourceFate = %v, want %v", tc.name, got, tc.want)
		}
	}
}
