package gleann

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fxScorer returns the persisted lexical leg of the standard index.
func fxScorer(t *testing.T, base string, slotIDs bool) *FTSScorer {
	t.Helper()
	sp := fxPassages(t, base, true)
	return &FTSScorer{store: sp.st, analyzer: sp.analyzer, slotIDs: slotIDs, opts: LexicalQueryOptions()}
}

// A lexical table that went missing degrades the lexical leg to "no hits"
// instead of failing the whole hybrid search.
func TestFTSScorerDegradesWhenTheLexicalTableIsGone(t *testing.T) {
	_, base, _ := fxBuilt(t)
	for _, slotIDs := range []bool{true, false} {
		name := "rowids"
		if slotIDs {
			name = "slots"
		}
		t.Run(name, func(t *testing.T) {
			f := fxScorer(t, base, slotIDs)
			if ids, scores := f.TopK("banana", 3); len(ids) != 1 || len(scores) != 1 {
				t.Fatalf("TopK on the intact index = %v, %v; want the banana passage", ids, scores)
			}
			passages := []Passage{{ID: 2}, {ID: 3}}
			if got := f.Score("banana", passages); len(got) != 2 || got[0] == 0 && got[1] == 0 {
				t.Fatalf("Score on the intact index = %v, want a positive score for banana", got)
			}
		})
	}
	fxExec(t, base, `DROP TABLE lex`)
	for _, slotIDs := range []bool{true, false} {
		f := fxScorer(t, base, slotIDs)
		if ids, scores := f.TopK("banana", 3); ids != nil || scores != nil {
			t.Errorf("slotIDs=%v: TopK on a missing lexical table = %v, %v; want nothing", slotIDs, ids, scores)
		}
		got := f.Score("banana", []Passage{{ID: 2}, {ID: -3}})
		if len(got) != 2 || got[0] != 0 || got[1] != 0 {
			t.Errorf("slotIDs=%v: Score on a missing lexical table = %v, want zeros", slotIDs, got)
		}
		if got := f.ScoreRowIDs("banana", []int64{1, 2}); len(got) != 0 {
			t.Errorf("slotIDs=%v: ScoreRowIDs on a missing lexical table = %v, want empty", slotIDs, got)
		}
		if got := f.ExplainTerms("banana", -3); len(got) != 0 {
			t.Errorf("slotIDs=%v: ExplainTerms on a missing lexical table = %v, want nil", slotIDs, got)
		}
	}
}

func TestFTSScorerEdgeInputs(t *testing.T) {
	_, base, _ := fxBuilt(t)
	f := fxScorer(t, base, true)
	if got := f.ScoreRowIDs("banana", nil); got == nil || len(got) != 0 {
		t.Errorf("ScoreRowIDs without rowids = %v, want an empty non-nil map", got)
	}
	// A negative ID is the negated rowid of a slotless passage; an unknown
	// slot explains nothing.
	fxExec(t, base, `UPDATE passages SET vec_slot = NULL WHERE text = 'banana bread'`)
	var rowid int64
	for _, p := range f2Rows(t, base) {
		if p.Text == "banana bread" {
			rowid = p.ID
		}
	}
	if terms := f.ExplainTerms("banana", -rowid); len(terms) == 0 {
		t.Error("ExplainTerms of a slotless passage explained nothing")
	}
	if terms := f.ExplainTerms("banana", 999); terms != nil {
		t.Errorf("ExplainTerms of an unknown slot = %v, want nil", terms)
	}
}

// The removed storage environment variables are named once, in a notice.
func TestNoticeRemovedEnv(t *testing.T) {
	for _, k := range []string{"GLEANN_STORE_FORMAT", "GLEANN_LEXICAL", "GLEANN_LEXICAL_OPTS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	var buf bytes.Buffer
	NoticeRemovedEnv(&buf)
	if buf.Len() != 0 {
		t.Fatalf("no variable set, notice = %q", buf.String())
	}
	t.Setenv("GLEANN_STORE_FORMAT", "1")
	t.Setenv("GLEANN_LEXICAL_OPTS", "x")
	NoticeRemovedEnv(&buf)
	got := buf.String()
	if !strings.Contains(got, "GLEANN_STORE_FORMAT, GLEANN_LEXICAL_OPTS ignored") || strings.Contains(got, "GLEANN_LEXICAL,") {
		t.Errorf("notice = %q, want exactly the two variables that are set", got)
	}
}

// A delta path that cannot be read for a reason other than absence is an error,
// not an empty delta.
func TestReadVectorDeltaReportsAnUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	if d, err := readVectorDelta(filepath.Join(dir, "absent.delta")); d != nil || err != nil {
		t.Fatalf("a missing delta = %v, %v; want nil, nil", d, err)
	}
	if _, err := readVectorDelta(dir); err == nil || !strings.Contains(err.Error(), "reading delta file") {
		t.Fatalf("a directory as the delta: %v", err)
	}
}

// The delta search keeps the k nearest entries and evicts a farther one when
// a nearer one arrives.
func TestDeltaSearchKeepsTheNearest(t *testing.T) {
	d := newVectorDelta(2, deltaDTypeF32, "l2")
	for slot, v := range map[int64][]float32{10: {5, 5}, 11: {1, 1}, 12: {0.1, 0}, 13: {9, 9}, 14: {0.2, 0}} {
		if err := d.Append(slot, v); err != nil {
			t.Fatal(err)
		}
	}
	ids, dists := d.Search([]float32{0, 0}, 2)
	if len(ids) != 2 || ids[0] != 12 || ids[1] != 14 {
		t.Fatalf("nearest two = %v (%v), want 12 then 14", ids, dists)
	}
	h := deltaHeap{{slot: 1, dist: 1}, {slot: 2, dist: 2}}
	if got := h.Pop().(deltaHit); got.slot != 2 || h.Len() != 1 {
		t.Errorf("Pop = %+v, len %d", got, h.Len())
	}
}

// A relative source directory cannot be related to an absolute file: the
// manifest falls back to the base name.
func TestManifestRelPathFallsBackToTheBaseName(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "sub", "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifestRelPath("relative-root", abs); got != "x.go" {
		t.Errorf("manifestRelPath = %q, want x.go", got)
	}
}

// Files that vanish or cannot be read are left out of a manifest, and the
// manifest of a directory that cannot be saved reports it.
func TestManifestSkipsUnreadableFilesAndReportsSaveFailures(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.md")
	if err := os.WriteFile(good, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []string{good, filepath.Join(dir, "gone.md")}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked.md")
		if err := os.WriteFile(locked, []byte("secret"), 0o000); err != nil {
			t.Fatal(err)
		}
		files = append(files, locked)
	}
	m, err := BuildManifest("idx", dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 {
		t.Errorf("BuildManifest entries = %v, want only good.md", m.Entries)
	}
	diff, next, err := QuickDiffDirectory(&Manifest{IndexName: "idx"}, dir, files)
	if err != nil || len(next.Entries) != 1 || len(diff.Added) != 1 {
		t.Errorf("QuickDiffDirectory = %+v, %+v, %v; want only good.md added", diff, next, err)
	}

	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(filepath.Join(blocker, "sub", "m.json"), m); err == nil {
		t.Error("SaveManifest under a file must fail")
	}
}

// A slot table doctor cannot read makes the index unrepairable.
func TestIndexHealthReportsDamagedStoreMeta(t *testing.T) {
	t.Run("unreadable slots", func(t *testing.T) {
		dir, base, _ := fxBuilt(t)
		fxExec(t, base, `ALTER TABLE passages RENAME COLUMN vec_slot TO vec_slot_x`)
		h, err := CheckIndexHealth(dir, "idx")
		if err != nil || h.Repairable || !strings.Contains(strings.Join(h.Problems, "\n"), "cannot be read") {
			t.Fatalf("health = %+v, %v", h, err)
		}
	})
}

// Reconcile reports a store it cannot read, and a removal that fails.
func TestReconcileFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("store unreadable", func(t *testing.T) {
		dir := t.TempDir()
		b, _ := newFormat2Builder(t, dir, 8)
		if _, err := b.Reconcile(ctx, "absent", t.TempDir()); err == nil || !strings.Contains(err.Error(), "load passages") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("removal fails", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		fxExec(t, base, fxFail("_del", "DELETE", "passages", ""))
		// None of the sources exist under the (empty) docs root.
		res, err := b.Reconcile(ctx, "idx", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "remove orphan passages") {
			t.Fatalf("err = %v", err)
		}
		if res == nil || res.RemovedPassages != 4 {
			t.Errorf("result = %+v, want the 4 passages counted before the failure", res)
		}
	})
}
