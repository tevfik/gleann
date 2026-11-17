package indexing

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/testutil/format1"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

var errEmbedderDown = errors.New("embedder is down")

// flakyComputer embeds deterministically into 8 dimensions (the size the
// fake Ollama of newEnv uses) and fails while down is set, or for the next
// failNext calls.
type flakyComputer struct {
	model    string
	down     atomic.Bool
	failNext atomic.Int32
	calls    atomic.Int32
}

func (f *flakyComputer) Compute(_ context.Context, texts []string) ([][]float32, error) {
	f.calls.Add(1)
	if f.down.Load() || f.failNext.Add(-1) >= 0 {
		return nil, errEmbedderDown
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 8)
		for j := range v {
			v[j] = float32((len(t)+j*5)%11) / 11
		}
		v[len(t)%8]++
		out[i] = v
	}
	return out, nil
}

func (f *flakyComputer) ComputeSingle(ctx context.Context, s string) ([]float32, error) {
	v, err := f.Compute(ctx, []string{s})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (f *flakyComputer) Dimensions() int   { return 8 }
func (f *flakyComputer) ModelName() string { return f.model }

// flakyEnv is an env whose index is built with a flakyComputer.
func flakyEnv(t *testing.T) (*testEnv, *flakyComputer, Options) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	env := newEnv(t)
	fc := &flakyComputer{model: env.cfg.EmbeddingModel}
	fc.failNext.Store(0)
	opts := env.options(nil)
	opts.Embedder = fc
	opts.NoEmbeddingDiskCache = true
	return env, fc, opts
}

// When the incremental update fails the sync falls back to a full rebuild;
// when that fails too, the previous index is restored untouched.
func TestSyncFallsBackToFullRebuildAndRollsBack(t *testing.T) {
	env, fc, opts := flakyEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "fb", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "fb")

	// Embedder down for the whole sync: the rebuild fails and is rolled back.
	writeFile(t, filepath.Join(docs, "extra.go"), "package main\n\nfunc PelicanBrief() int { return 9 }\n")
	fc.down.Store(true)
	var out bytes.Buffer
	o := opts
	w := &lockedBuffer{b: &out}
	o.Stdout, o.Stderr = w, w
	_, err := Sync(context.Background(), SyncOptions{Options: o, Name: "fb"})
	if err == nil || !strings.Contains(err.Error(), "previous index restored") || !errors.Is(err, errEmbedderDown) {
		t.Fatalf("err = %v, want the rolled-back rebuild error", err)
	}
	if !strings.Contains(out.String(), "falling back to full rebuild") {
		t.Errorf("fallback not announced:\n%s", out.String())
	}
	assertUntouched(t, env, "fb", *built)
	if HasSyncBackup(env.cfg.IndexDir, "fb") {
		t.Error("the rollback copy was left behind")
	}

	// The embedder fails once: the incremental update fails, the full
	// rebuild succeeds and indexes the new file.
	fc.down.Store(false)
	fc.failNext.Store(1)
	res, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "fb"})
	if err != nil {
		t.Fatalf("Sync after one failure: %v", err)
	}
	if !res.FullRebuild || res.Update != nil {
		t.Fatalf("result = %+v, want a full rebuild", res)
	}
	if got := passageSet(t, env.cfg, "fb"); !strings.Contains(strings.Join(got, "\n"), "PelicanBrief") {
		t.Errorf("the rebuilt index lacks the new file: %v", got)
	}
	after, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "fb")
	if !after.CreatedAt.Equal(built.CreatedAt) || after.SourceDir != built.SourceDir {
		t.Errorf("the rebuild lost the index identity: %+v -> %+v", built, after)
	}
}

// A sync limited to a mode keeps the files of the other mode: they are not
// eligible in this mode, but they still exist and are not deletions.
func TestSyncModeKeepsFilesOfTheOtherMode(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	writeFile(t, filepath.Join(docs, "table.csv"), "city,bird\nlisbon,swift\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "mx", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "mx")

	for _, mode := range []Mode{ModeDocs, ModeCode} {
		var out bytes.Buffer
		opts := env.options(&out)
		opts.Mode = mode
		res, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "mx"})
		if err != nil {
			t.Fatalf("%s sync: %v", mode, err)
		}
		if !res.UpToDate || len(res.Deleted) != 0 {
			t.Errorf("%s sync = %+v, want up to date with nothing deleted", mode, res)
		}
		want := map[Mode]string{ModeDocs: "Sync mode: DOCUMENTS", ModeCode: "Sync mode: CODE"}[mode]
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s sync did not announce its mode:\n%s", mode, out.String())
		}
	}
	if after := passageSet(t, env.cfg, "mx"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("a mode-limited sync changed the index:\n%v\n%v", before, after)
	}

	// A file of the other mode that really was deleted is removed.
	if err := os.Remove(filepath.Join(docs, "main.go")); err != nil {
		t.Fatal(err)
	}
	opts := env.options(nil)
	opts.Mode = ModeDocs
	res, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "mx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || filepath.Base(res.Deleted[0]) != "main.go" {
		t.Errorf("deleted = %v, want main.go", res.Deleted)
	}
}

// Sync refuses an index in the old store format with the migration hint and
// validates its arguments.
func TestSyncRefusesOldFormatAndBadArguments(t *testing.T) {
	env := newEnv(t)
	if err := format1.Write(format1.Index{Dir: env.cfg.IndexDir, Name: "old",
		Passages: []format1.Passage{{ID: 0, Text: "x", Metadata: map[string]any{"source": "a.md"}}},
		Meta:     map[string]any{"lexical_only": true, "source_dir": t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "old"}); err == nil || !strings.Contains(err.Error(), "gleann index migrate old") {
		t.Fatalf("old-format sync: %v", err)
	}
	//lint:ignore SA1012 Sync documents that a nil context means Background.
	if _, err := Sync(nil, SyncOptions{Options: env.options(nil)}); err == nil || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("nameless sync: %v", err)
	}
	res, err := SyncChanged(context.Background(), SyncOptions{Options: env.options(nil), Name: "old"}, []string{"", "  "})
	if err != nil || !res.UpToDate {
		t.Fatalf("SyncChanged without paths = %+v, %v; want up to date without work", res, err)
	}
}

// Sync with an explicit file list skips blank entries and treats missing
// files as deletions.
func TestSyncFilesList(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "fl", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docs, "new.go"), "package main\n\nfunc Ibis() {}\n")
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "fl", Files: []string{" ", "new.go", filepath.Join(docs, "gone.go")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || len(res.Deleted) != 1 || res.Update == nil {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(strings.Join(passageSet(t, env.cfg, "fl"), "\n"), "Ibis") {
		t.Error("the listed file was not indexed")
	}
}

// bootstrapTrackerFromIndex records the files an index already holds, and
// the eligible files older than the index, so a tracker that lost its state
// does not make the next sync re-read everything.
func TestBootstrapTrackerFromIndex(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "bt", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	meta, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "bt")
	old := filepath.Join(docs, "old.txt")
	writeFile(t, old, "written before the index time")
	past := meta.CreatedAt.Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(docs, "fresh.txt")
	writeFile(t, fresh, "written after the index time")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(fresh, future, future); err != nil {
		t.Fatal(err)
	}

	tr, err := vault.NewTracker(filepath.Join(t.TempDir(), "fresh-vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	var out bytes.Buffer
	base := filepath.Join(env.cfg.IndexDir, "bt", "bt")
	bootstrapTrackerFromIndex(context.Background(), tr, docs, base, []string{old, fresh, filepath.Join(docs, "missing.txt")}, meta.CreatedAt, newPrinter(&out, nil))

	has := func(p string) bool {
		rec, _ := tr.GetRecordByPath(context.Background(), p)
		return rec != nil
	}
	if !has(filepath.Join(docs, "main.go")) || !has(filepath.Join(docs, "README.md")) {
		t.Error("files of the index were not recorded")
	}
	if !has(old) {
		t.Error("an eligible file older than the index was not recorded")
	}
	if has(fresh) {
		t.Error("a file newer than the index was recorded as already indexed")
	}
	if !strings.Contains(out.String(), "Tracker synchronized") {
		t.Errorf("no summary line: %q", out.String())
	}

	// A second pass finds nothing missing and stays silent.
	out.Reset()
	bootstrapTrackerFromIndex(context.Background(), tr, docs, base, []string{old}, meta.CreatedAt, newPrinter(&out, nil))
	if out.Len() != 0 {
		t.Errorf("second bootstrap printed %q", out.String())
	}
}

func TestPendingSyncBackups(t *testing.T) {
	dir := t.TempDir()
	if got := PendingSyncBackups(filepath.Join(dir, "missing")); got != nil {
		t.Fatalf("missing dir: %v", got)
	}
	for _, d := range []string{"a" + syncBackupSuffix, "b" + syncBackupSuffix, "c"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "d"+syncBackupSuffix), "a file, not a backup")
	got := PendingSyncBackups(dir)
	sort.Strings(got)
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("PendingSyncBackups = %v, want a,b", got)
	}
}

// A rollback copy whose metadata is unreadable still restores over a
// missing index (it is the only copy there is).
func TestRecoverWithUnreadableBackupMeta(t *testing.T) {
	dir := t.TempDir()
	bak := SyncBackupPath(dir, "ix")
	writeFile(t, filepath.Join(bak, "ix.meta.json"), "{not json")
	if m := readBackupMeta(bak, "ix"); m != nil {
		t.Fatalf("readBackupMeta of junk = %+v", m)
	}
	what, err := RecoverSyncBackup(context.Background(), dir, "ix")
	if err != nil || what != "restored" {
		t.Fatalf("RecoverSyncBackup = %q, %v", what, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ix", "ix.meta.json")); err != nil {
		t.Fatal("the backup was not moved into place")
	}
}
