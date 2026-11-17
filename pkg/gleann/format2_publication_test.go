package gleann

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Crash-safety of the format-2 vector publication (RFC 003 section 3.4): a
// crash at any boundary — before the commit, after the commit but before the
// rename, after the rename but before the generation finish — must leave a
// state the next open resolves, with every vector hit still addressing the
// passage whose text produced it. The crashes are injected through the
// builder's crashHook field (never a package global).

// crashAt returns a builder hook that fails at one publication boundary.
func crashAt(point string) func(string) error {
	return func(p string) error {
		if p == point {
			return fmt.Errorf("injected crash at %s", p)
		}
		return nil
	}
}

// f2UpdateItems replaces a.md and adds d.md. Every text length stays distinct
// across the resulting corpus so the test embedder's self-retrieval is exact
// (see f2Items).
func f2UpdateItems() []Item {
	return []Item{
		{Text: "apricot jam preserves", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}
}

// f2UpdateTexts is the corpus after applying f2UpdateItems to f2Items.
func f2UpdateTexts() []string {
	return []string{"apricot jam preserves", "banana bread", "cherry tart dessert", "date syrup cake"}
}

// f2SelfRetrieval opens the index for search — which runs the publication
// recovery — and checks that every passage text retrieves exactly itself
// through the vector leg: a hit that maps to the wrong passage fails here.
func f2SelfRetrieval(t *testing.T, dir string, embedder *testEmbedder, texts []string) {
	t.Helper()
	ctx := context.Background()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load after recovery: %v", err)
	}
	defer s.Close()
	if !s.VectorAvailable() {
		t.Fatal("vectors not available after recovery")
	}
	for _, text := range texts {
		rs, err := s.Search(ctx, text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("Search(%q): %v", text, err)
		}
		if len(rs) != 1 || rs[0].Text != text {
			t.Errorf("self-retrieval of %q returned %+v; a vector hit maps to the wrong passage", text, rs)
		}
	}
}

// f2Healthy asserts index doctor reports no problems.
func f2Healthy(t *testing.T, dir string) {
	t.Helper()
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatalf("CheckIndexHealth: %v", err)
	}
	if len(h.Problems) != 0 {
		t.Errorf("doctor after recovery: %v", h.Problems)
	}
}

// A crash after the commit but before the rename: the database already
// describes the new passages and marks the publication pending, while the
// old vector file is still live. Recovery must roll the rename forward.
func TestFormat2_PublicationCrashAfterCommit(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	b.limits = incrementalLimits{} // every update rebuilds the main file: its publication is under test here
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")

	b.crashHook = crashAt(crashAfterCommit)
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err == nil {
		t.Fatal("UpdateIndex must fail at the injected crash")
	}
	if got := f2Meta(t, base, "pending_vector_file"); got != "idx.index.tmp\nidx.delta.tmp" {
		t.Fatalf("pending_vector_file = %q, want the main file and the empty delta", got)
	}
	if got := f2Meta(t, base, "pending_vector_generation"); got != "2" {
		t.Fatalf("pending_vector_generation = %q, want 2", got)
	}
	if _, err := os.Stat(base + ".index.tmp"); err != nil {
		t.Fatalf("staged vector file missing after crash: %v", err)
	}
	if _, ok := f2Rows(t, base)["date syrup cake"]; !ok {
		t.Fatal("the committed passages are missing after the crash")
	}
	// Doctor sees the pending publication and names the repair.
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatalf("CheckIndexHealth: %v", err)
	}
	if len(h.Problems) == 0 || !strings.Contains(strings.Join(h.Problems, "\n"), "pending") {
		t.Errorf("doctor must report the pending publication, got %v", h.Problems)
	}

	// Reopen: recovery rolls the publication forward, and every vector hit
	// maps to the passage whose text produced it.
	f2SelfRetrieval(t, dir, embedder, f2UpdateTexts())
	f2Healthy(t, dir)
	if _, err := os.Stat(base + ".index.tmp"); !os.IsNotExist(err) {
		t.Errorf("staging file survived recovery: %v", err)
	}
	if got := f2Meta(t, base, "vector_generation"); got != "2" {
		t.Errorf("vector_generation after recovery = %q, want 2", got)
	}
	if got := f2Meta(t, base, "pending_vector_file"); got != "" {
		t.Errorf("pending_vector_file after recovery = %q, want empty", got)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil || m.VectorGeneration != 2 {
		t.Errorf("meta.json vector_generation = %+v (%v), want 2", m, err)
	}
}

// A crash after the rename but before the finish: the new vector file is
// live and the pending keys remain. Recovery must finish the generation.
func TestFormat2_PublicationCrashAfterRename(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	b.limits = incrementalLimits{} // every update rebuilds the main file: its publication is under test here
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")

	b.crashHook = crashAt(crashAfterRename)
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err == nil {
		t.Fatal("UpdateIndex must fail at the injected crash")
	}
	if _, err := os.Stat(base + ".index.tmp"); !os.IsNotExist(err) {
		t.Fatalf("the rename must have happened before this crash: %v", err)
	}
	if got := f2Meta(t, base, "pending_vector_file"); got != "idx.index.tmp\nidx.delta.tmp" {
		t.Fatalf("pending_vector_file = %q, want the main file and the empty delta", got)
	}
	if got := f2Meta(t, base, "vector_generation"); got != "1" {
		t.Fatalf("vector_generation before the finish = %q, want the old 1", got)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatalf("CheckIndexHealth: %v", err)
	}
	if len(h.Problems) == 0 || !strings.Contains(strings.Join(h.Problems, "\n"), "pending") {
		t.Errorf("doctor must report the pending publication, got %v", h.Problems)
	}

	f2SelfRetrieval(t, dir, embedder, f2UpdateTexts())
	f2Healthy(t, dir)
	if got := f2Meta(t, base, "vector_generation"); got != "2" {
		t.Errorf("vector_generation after recovery = %q, want 2", got)
	}
	if got := f2Meta(t, base, "pending_vector_generation"); got != "" {
		t.Errorf("pending_vector_generation after recovery = %q, want empty", got)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil || m.VectorGeneration != 2 {
		t.Errorf("meta.json vector_generation = %+v (%v), want 2", m, err)
	}
}

// A crash before the commit: the transaction rolled back, so the database
// still matches the old vector file and only a stale staging file remains.
// Recovery must delete it and leave the old generation intact.
func TestFormat2_PublicationCrashBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	b.limits = incrementalLimits{} // every update rebuilds the main file: its publication is under test here
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	oldTexts := []string{"apple pie recipe", "apple pie recipe with cinnamon sugar", "banana bread", "cherry tart dessert"}

	b.crashHook = crashAt(crashBeforeCommit)
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err == nil {
		t.Fatal("UpdateIndex must fail at the injected crash")
	}
	if got := f2Meta(t, base, "pending_vector_file"); got != "" {
		t.Fatalf("pending_vector_file = %q, want empty (the commit never happened)", got)
	}
	if _, err := os.Stat(base + ".index.tmp"); err != nil {
		t.Fatalf("stale staging file missing: %v", err)
	}
	rows := f2Rows(t, base)
	if _, ok := rows["date syrup cake"]; ok {
		t.Fatal("uncommitted passages survived the crash")
	}
	if _, ok := rows["apple pie recipe"]; !ok {
		t.Fatal("the old passages must survive a crash before the commit")
	}

	// Reopen: recovery deletes the stale staging file; the old vector file
	// still serves the old passages correctly.
	f2SelfRetrieval(t, dir, embedder, oldTexts)
	f2Healthy(t, dir)
	if _, err := os.Stat(base + ".index.tmp"); !os.IsNotExist(err) {
		t.Errorf("stale staging file survived recovery: %v", err)
	}
	if got := f2Meta(t, base, "vector_generation"); got != "1" {
		t.Errorf("vector_generation = %q, want the unchanged 1", got)
	}

	// A clean update then publishes normally.
	b.crashHook = nil
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err != nil {
		t.Fatalf("UpdateIndex after recovery: %v", err)
	}
	f2SelfRetrieval(t, dir, embedder, f2UpdateTexts())
	f2Healthy(t, dir)
	if got := f2Meta(t, base, "vector_generation"); got != "2" {
		t.Errorf("vector_generation after the clean update = %q, want 2", got)
	}
}

// The same boundaries during the FIRST vector publication of a staged build
// (BuildPassages stores the passages slotless; BuildVectors assigns the slots
// in the publication transaction).
func TestFormat2_BuildVectorsCrashRecovers(t *testing.T) {
	for _, point := range []string{crashBeforeCommit, crashAfterCommit, crashAfterRename, crashAfterFinish} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			b, embedder := newFormat2Builder(t, dir, 8)
			b.limits = incrementalLimits{}
			b.limits = incrementalLimits{} // every update rebuilds the main file: its publication is under test here
			ctx := context.Background()
			items := f2Items()
			ids, err := b.BuildPassages("idx", items)
			if err != nil {
				t.Fatalf("BuildPassages: %v", err)
			}
			base := filepath.Join(dir, "idx", "idx")
			b.crashHook = crashAt(point)
			if err := b.BuildVectors(ctx, "idx", items, ids); err == nil {
				t.Fatal("BuildVectors must fail at the injected crash")
			}

			if point == crashBeforeCommit {
				// Rolled back: the passages are still slotless and there is
				// no vector file; the lexical leg serves them (negative IDs)
				// and recovery deletes the stale staging file.
				s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
				if err := s.Load(ctx, "idx"); err != nil {
					t.Fatalf("Load of the staged index: %v", err)
				}
				rs, err := s.SearchBM25(ctx, "banana", 5)
				if err != nil || len(rs) == 0 || rs[0].Text != "banana bread" {
					t.Fatalf("SearchBM25 of the staged index = %+v (%v)", rs, err)
				}
				if rs[0].ID >= 0 {
					t.Errorf("slotless hit ID = %d, want a negative rowid", rs[0].ID)
				}
				if p, err := s.passages.Get(rs[0].ID); err != nil || p.Text != rs[0].Text {
					t.Errorf("Get(%d) = %+v (%v), want the hit's own text", rs[0].ID, p, err)
				}
				s.Close()
				if _, err := os.Stat(base + ".index.tmp"); !os.IsNotExist(err) {
					t.Errorf("stale staging file survived recovery: %v", err)
				}
				// Retrying the vector build completes the publication.
				b.crashHook = nil
				if err := b.BuildVectors(ctx, "idx", items, ids); err != nil {
					t.Fatalf("BuildVectors retry: %v", err)
				}
			}

			texts := []string{"apple pie recipe", "apple pie recipe with cinnamon sugar", "banana bread", "cherry tart dessert"}
			f2SelfRetrieval(t, dir, embedder, texts)
			f2Healthy(t, dir)
			if got := f2Meta(t, base, "vector_generation"); got != "1" {
				t.Errorf("vector_generation = %q, want 1", got)
			}
			m, err := GetIndexMeta(dir, "idx")
			if err != nil || m.VectorGeneration != 1 || !m.VectorReady {
				t.Errorf("meta.json after recovery = %+v (%v), want generation 1 and vector-ready", m, err)
			}
		})
	}
}

// Doctor cross-checks the generation mirrored in meta.json against the
// store's: a crash between the finish and the metadata write leaves a
// mismatch, and a repair (Compact) republishes and resolves it.
func TestFormat2_GenerationMismatchReported(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	b.limits = incrementalLimits{} // every update rebuilds the main file: its publication is under test here
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Simulate the lost metadata write: the store published generation 1,
	// meta.json never recorded it.
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.VectorGeneration = 0 }); err != nil {
		t.Fatalf("UpdateIndexMeta: %v", err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatalf("CheckIndexHealth: %v", err)
	}
	joined := strings.Join(h.Problems, "\n")
	if !strings.Contains(joined, "vector generation") || !strings.Contains(joined, "repair") {
		t.Fatalf("doctor must report the generation mismatch with a repair hint, got %v", h.Problems)
	}
	if _, err := b.Compact(ctx, "idx"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	f2Healthy(t, dir)
	f2SelfRetrieval(t, dir, embedder, []string{"apple pie recipe", "apple pie recipe with cinnamon sugar", "banana bread", "cherry tart dessert"})
}

// removeDBFiles must report a removal that failed for a reason other than a
// missing file (on Windows: the database is open in another process), so a
// rebuild refuses instead of reopening and reusing the old database. A
// non-empty directory at the path stands in for the undeletable file.
func TestRemoveDBFilesReportsFailure(t *testing.T) {
	dir := t.TempDir()
	if err := removeDBFiles(filepath.Join(dir, "absent.db")); err != nil {
		t.Fatalf("missing files must not be an error: %v", err)
	}
	stuck := filepath.Join(dir, "stuck.db")
	if err := os.MkdirAll(filepath.Join(stuck, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeDBFiles(stuck); err == nil {
		t.Fatal("removeDBFiles must report a removal that failed")
	}
}
