package gleann

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
)

// The directory of an index that cannot be created is the first failure of a
// build.
func TestBuildPassagesCannotCreateTheIndexDirectory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := newFormat2Builder(t, blocker, 8) // the index directory's parent is a file
	_, err := b.BuildPassages("idx", f2Items())
	if err == nil || !strings.Contains(err.Error(), "create index directory") {
		t.Fatalf("err = %v", err)
	}
}

// The metadata of a lexical-only build that cannot be written fails the build.
func TestBuildPassagesCannotWriteMetadata(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := os.MkdirAll(filepath.Join(dir, "idx", "idx.meta.json", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := b.BuildPassages("idx", f2Items())
	if err == nil || !strings.Contains(err.Error(), "write metadata") {
		t.Fatalf("err = %v", err)
	}
}

// A database that is neither damaged nor unsupported but cannot be opened is
// an error, not a reason to throw the index away.
func TestBuildPassagesKeepsAnUnopenableDatabase(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := os.MkdirAll(filepath.Join(dir, "idx", "idx.db", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := b.BuildPassages("idx", f2Items())
	if err == nil || !strings.Contains(err.Error(), "add passages") {
		t.Fatalf("err = %v", err)
	}
	if b.DatabaseRecreated() {
		t.Error("an index whose database cannot be opened must not be recreated")
	}
	if _, err := os.Stat(filepath.Join(dir, "idx", "idx.db", "x")); err != nil {
		t.Errorf("the path was destroyed: %v", err)
	}
}

// A damaged database is recreated and the caller is told to rebuild the graph;
// when the damaged files cannot be removed the build refuses.
func TestBuildPassagesRecreatesADamagedDatabase(t *testing.T) {
	t.Run("recreated", func(t *testing.T) {
		dir := t.TempDir()
		b, _ := newFormat2Builder(t, dir, 8)
		if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "idx", "idx.db"), []byte(strings.Repeat("not a database ", 200)), 0o644); err != nil {
			t.Fatal(err)
		}
		ids, err := b.BuildPassages("idx", f2Items())
		if err != nil {
			t.Fatalf("BuildPassages: %v", err)
		}
		if len(ids) != 4 || !b.DatabaseRecreated() {
			t.Fatalf("ids = %v, recreated = %v", ids, b.DatabaseRecreated())
		}
	})
	t.Run("cannot be removed", func(t *testing.T) {
		dir := t.TempDir()
		b, _ := newFormat2Builder(t, dir, 8)
		idxDir := filepath.Join(dir, "idx")
		if err := os.MkdirAll(filepath.Join(idxDir, "idx.db-shm", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(idxDir, "idx.db"), []byte(strings.Repeat("not a database ", 200)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := b.BuildPassages("idx", f2Items())
		if err == nil || !strings.Contains(err.Error(), "removing the old format-2 store") {
			t.Fatalf("err = %v", err)
		}
	})
}

// A new database that cannot be created is reported with its path context.
func TestBuildPassagesFormat2CannotCreateTheDatabase(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := newFormat2Builder(t, dir, 8)
	_, err := b.buildPassagesFormat2(context.Background(), filepath.Join(blocker, "idx"), "idx", f2Items(), nil)
	if err == nil || !strings.Contains(err.Error(), "opening format-2 store") {
		t.Fatalf("err = %v", err)
	}
}

// A rebuild takes the source directory of the previous metadata when the
// builder has none, and it drops the passages of a source that vanished.
func TestRebuildInPlaceUsesRecordedSourceDirAndSourcelessPassages(t *testing.T) {
	dir, base, b := fxBuilt(t)
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("apple pie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.SourceDir = srcDir }); err != nil {
		t.Fatal(err)
	}
	// A passage without a source goes away with the rebuild; its retraction is
	// a no-op for the graph.
	fxExec(t, base, `INSERT INTO passages (pid, source, chunk_no, kind, text, meta) VALUES ('p-orphan', '', 0, 'text', 'orphan text', '')`)

	if _, err := b.BuildPassages("idx", f2Items()[:2]); err != nil {
		t.Fatalf("BuildPassages: %v", err)
	}
	rows := f2Rows(t, base)
	if _, ok := rows["orphan text"]; ok {
		t.Error("the sourceless passage must be replaced by the rebuild")
	}
	if len(rows) != 2 {
		t.Errorf("passages after the rebuild = %d, want 2", len(rows))
	}
	files, err := fxOpen(t, base).Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := files["a.md"]; !ok || f.Size != int64(len("apple pie")) || f.Hash == "" {
		t.Errorf("files row of a.md = %+v (present %v), want the size and hash of the file in the recorded source dir", f, ok)
	}
}

// The in-place replacement reports each step that fails and rolls back.
func TestReplacePassagesInPlaceFailures(t *testing.T) {
	cases := []struct {
		name string
		ddl  string
		want string
	}{
		{"unrecoverable publication", "", "recovering interrupted vector publication"},
		{"replacing the passages", fxFail("_ins", "INSERT", "passages", ""), ""},
		{"recording the metadata", failMeta("vector_generation"), `setting store meta "vector_generation"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, base, b := fxBuilt(t)
			if tc.ddl == "" {
				st := fxOpen(t, base)
				if err := st.SetMeta(context.Background(), "pending_vector_file", "idx.index.tmp"); err != nil {
					t.Fatal(err)
				}
				if err := st.SetMeta(context.Background(), "pending_vector_generation", "junk"); err != nil {
					t.Fatal(err)
				}
				st.Close()
			} else {
				fxExec(t, base, tc.ddl)
			}
			_, err := b.BuildPassages("idx", f2Items()[:2])
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if tc.ddl != "" && len(f2Rows(t, base)) != 4 {
				t.Error("a failed replacement must leave the old passages")
			}
		})
	}
}

// A staging failure on one source of a brand-new index names the source.
func TestApplyStagedSourcesNamesTheFailingSource(t *testing.T) {
	_, base, _ := fxBuilt(t)
	st := fxOpen(t, base)
	fxExec(t, base, fxFail("_ins", "INSERT", "passages", "NEW.source = 'q.md'"))
	items := []Item{{Text: "quince paste", Metadata: map[string]any{"source": "q.md", "chunk_index": 0}}}
	_, err := applyStagedSources(context.Background(), st, analysis.New(""), items, "")
	if err == nil || !strings.Contains(err.Error(), `applying source "q.md"`) {
		t.Fatalf("err = %v", err)
	}
}

// The same failure inside a caller-owned transaction.
func TestApplySourceGroupsTxNamesTheFailingSource(t *testing.T) {
	_, base, _ := fxBuilt(t)
	st := fxOpen(t, base)
	fxExec(t, base, fxFail("_ins", "INSERT", "passages", "NEW.source = 'q.md'"))
	tx, err := st.BeginWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	items := []Item{{Text: "quince paste", Metadata: map[string]any{"source": "q.md", "chunk_index": 0}}}
	_, err = applySourceGroupsTx(context.Background(), tx, st, analysis.New(""), items, 10, "")
	if err == nil || !strings.Contains(err.Error(), `applying source "q.md"`) {
		t.Fatalf("err = %v", err)
	}
}

// BuildVectors fails cleanly when the backend, the old format check, the
// recovery or the slot assignment fails, and keeps the index lexical-only.
func TestBuildVectorsFailures(t *testing.T) {
	prep := func(t *testing.T) (*LeannBuilder, string, []Item, []int64) {
		t.Helper()
		dir := t.TempDir()
		b, _ := newFormat2Builder(t, dir, 8)
		items := f2Items()
		ids, err := b.BuildPassages("idx", items)
		if err != nil {
			t.Fatal(err)
		}
		return b, filepath.Join(dir, "idx", "idx"), items, ids
	}
	ctx := context.Background()

	t.Run("backend", func(t *testing.T) {
		b, _, items, ids := prep(t)
		b.backend = failingBuildBackend{b.backend}
		err := b.BuildVectors(ctx, "idx", items, ids)
		if err == nil || !strings.Contains(err.Error(), "build index: backend refused") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("old format", func(t *testing.T) {
		b, base, items, ids := prep(t)
		if err := os.Remove(base + ".meta.json"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".passages.db", []byte("old bbolt data"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := b.BuildVectors(ctx, "idx", items, ids)
		var old *OldFormatError
		if !errors.As(err, &old) {
			t.Fatalf("err = %v, want an OldFormatError", err)
		}
	})
	t.Run("pending publication unrecoverable", func(t *testing.T) {
		b, base, items, ids := prep(t)
		st := fxOpen(t, base)
		if err := st.SetMeta(ctx, "pending_vector_file", "idx.index.tmp"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMeta(ctx, "pending_vector_generation", "junk"); err != nil {
			t.Fatal(err)
		}
		st.Close()
		err := b.BuildVectors(ctx, "idx", items, ids)
		if err == nil || !strings.Contains(err.Error(), "recovering interrupted vector publication") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("slot assignment", func(t *testing.T) {
		b, base, items, ids := prep(t)
		fxExec(t, base, fxFail("_upd", "UPDATE", "passages", ""))
		err := b.BuildVectors(ctx, "idx", items, ids)
		if err == nil || !strings.Contains(err.Error(), "injected fault") {
			t.Fatalf("err = %v", err)
		}
		m, err := GetIndexMeta(b.config.IndexDir, "idx")
		if err != nil || m.VectorReady || !m.LexicalOnly {
			t.Fatalf("meta after a failed vector build = %+v (%v), want lexical-only", m, err)
		}
	})
}

// A configured vector dtype is recorded in the metadata of the built index.
func TestBuildVectorsRecordsTheVectorDType(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	b.config.HNSWConfig.VectorDType = deltaDTypeF16
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil || m.VectorDType != deltaDTypeF16 {
		t.Fatalf("VectorDType = %q (%v), want %q", m.VectorDType, err, deltaDTypeF16)
	}
}

// Compact on an index whose passages cannot be counted reports it.
func TestCompactStoredReportsACountFailure(t *testing.T) {
	_, _, b := fxBuilt(t)
	err := b.compactStored(fxCancelled(), "idx", &UpdateStats{})
	if err == nil || !strings.Contains(err.Error(), "counting passages") {
		t.Fatalf("err = %v", err)
	}
}

// currentVectorSlots derives the high-water mark from the slots when the
// index predates the meta key, and reports a failing read.
func TestCurrentVectorSlots(t *testing.T) {
	_, base, _ := fxBuilt(t)
	st := fxOpen(t, base)
	ctx := context.Background()
	got, err := currentVectorSlots(ctx, st)
	if err != nil || got != 4 {
		t.Fatalf("slots = %d (%v), want 4", got, err)
	}
	for _, v := range []string{"", "junk", "-3"} {
		if err := st.SetMeta(ctx, metaVectorSlots, v); err != nil {
			t.Fatal(err)
		}
		if got, err := currentVectorSlots(ctx, st); err != nil || got != 4 {
			t.Errorf("with the key %q: slots = %d (%v), want 4 derived from the passages", v, got, err)
		}
	}
	if _, err := currentVectorSlots(fxCancelled(), st); err == nil {
		t.Error("an unreadable meta table must be reported")
	}
	fxExec(t, base, `ALTER TABLE passages RENAME COLUMN vec_slot TO vec_slot_x`, `DELETE FROM meta WHERE key = '`+metaVectorSlots+`'`)
	if _, err := currentVectorSlots(ctx, st); err == nil {
		t.Error("an unreadable slot span must be reported")
	}
}

// The generation helpers fall back to the published generation and to zero.
func TestVectorGenerationFallbacks(t *testing.T) {
	_, base, _ := fxBuilt(t)
	st := fxOpen(t, base)
	ctx := context.Background()
	if got := currentVectorMainGeneration(ctx, st); got != 1 {
		t.Fatalf("main generation = %d, want 1", got)
	}
	if err := st.SetMeta(ctx, metaVectorMainGeneration, "junk"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(ctx, "vector_generation", "7"); err != nil {
		t.Fatal(err)
	}
	if got := currentVectorMainGeneration(ctx, st); got != 7 {
		t.Errorf("a garbled main generation must fall back to the published one, got %d", got)
	}
	if err := st.SetMeta(ctx, "vector_generation", "junk"); err != nil {
		t.Fatal(err)
	}
	if got := currentVectorGeneration(ctx, st); got != 0 {
		t.Errorf("a garbled generation counts as never published, got %d", got)
	}
}
