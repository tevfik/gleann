package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// holdLock takes the in-process index lock so a second caller can only give
// up through its context.
func holdLock(t *testing.T, indexDir, name string) {
	t.Helper()
	unlock, err := lockIndex(context.Background(), indexDir, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
}

func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// Operations on an index that another operation holds give up when their
// context ends instead of waiting forever.
func TestOperationsOnABusyIndexHonourTheContext(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "busy")
	if err := os.MkdirAll(SyncBackupPath(env.cfg.IndexDir, "busy"), 0o755); err != nil {
		t.Fatal(err)
	}
	holdLock(t, env.cfg.IndexDir, "busy")

	if _, err := Sync(shortCtx(t), SyncOptions{Options: env.options(nil), Name: "busy"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Sync: %v", err)
	}
	if _, err := Repair(shortCtx(t), RepairOptions{Options: env.options(nil), Name: "busy"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Repair: %v", err)
	}
	if _, err := RecoverSyncBackup(shortCtx(t), env.cfg.IndexDir, "busy"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RecoverSyncBackup: %v", err)
	}
	if _, err := RebuildIndex(shortCtx(t), "busy", docs, env.options(nil)); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RebuildIndex: %v", err)
	}
}

// The cross-process rewrite guard reports what is wrong with its lock file.
func TestAcquireRewriteGuardFailures(t *testing.T) {
	root := t.TempDir()
	notADir := filepath.Join(root, "file")
	writeFile(t, notADir, "x")
	if _, err := acquireRewriteGuard(notADir, "ix"); err == nil || !strings.Contains(err.Error(), "creating index directory") {
		t.Errorf("index directory that is a file: %v", err)
	}

	// A directory where the lock file should be cannot be opened as a lock.
	idx := t.TempDir()
	if err := os.MkdirAll(filepath.Join(idx, "ix.rewrite.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRewriteGuard(idx, "ix"); err == nil || !strings.Contains(err.Error(), `locking index "ix"`) {
		t.Errorf("lock path that is a directory: %v", err)
	}
	if err := os.MkdirAll(SyncBackupPath(idx, "ix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverSyncBackup(context.Background(), idx, "ix"); err == nil || !strings.Contains(err.Error(), "locking index") {
		t.Errorf("RecoverSyncBackup with an unusable guard: %v", err)
	}
	err := withIndexRollback(idx, "ix", newPrinter(nil, nil), func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "locking index") {
		t.Errorf("withIndexRollback with an unusable guard: %v", err)
	}
}

// A backup without readable metadata has no "previous" index to compare with.
func TestReadBackupMetaMissing(t *testing.T) {
	if m := readBackupMeta(t.TempDir(), "ix"); m != nil {
		t.Errorf("readBackupMeta without a file = %+v", m)
	}
}

// readOnlyIndexDir makes dir read-only for the test, or skips where
// permission bits do not stop the owner (Windows, root).
func readOnlyIndexDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not make a directory read-only on Windows")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	probe := filepath.Join(dir, "probe")
	if f, err := os.Create(probe); err == nil {
		f.Close()
		_ = os.Remove(probe)
		t.Skip("running with privileges that ignore directory permissions")
	}
}

// Settling a rollback copy reports a filesystem that refuses the cleanup or
// the restore, naming where the good copy is.
func TestRecoverSyncBackupFilesystemFailures(t *testing.T) {
	t.Run("stale backup cannot be removed", func(t *testing.T) {
		env := newEnv(t)
		buildFor(t, env, "m")
		bak := SyncBackupPath(env.cfg.IndexDir, "m")
		writeFile(t, filepath.Join(bak, "leftover"), "x")
		readOnlyIndexDir(t, env.cfg.IndexDir)
		_, err := recoverSyncBackupLocked(env.cfg.IndexDir, "m")
		mustContain(t, err, "removing the stale rollback copy")
	})
	t.Run("partial index cannot be cleared", func(t *testing.T) {
		idx := t.TempDir()
		writeFile(t, filepath.Join(SyncBackupPath(idx, "m"), "m.meta.json"), `{"name":"m"}`)
		writeFile(t, filepath.Join(idx, "m", "junk"), "partial")
		readOnlyIndexDir(t, idx)
		_, err := recoverSyncBackupLocked(idx, "m")
		mustContain(t, err, "clearing the partial index")
	})
	t.Run("backup cannot be moved back", func(t *testing.T) {
		idx := t.TempDir()
		writeFile(t, filepath.Join(SyncBackupPath(idx, "m"), "m.meta.json"), `{"name":"m"}`)
		readOnlyIndexDir(t, idx)
		_, err := recoverSyncBackupLocked(idx, "m")
		mustContain(t, err, "restoring the index from")
	})
}

// If the rebuild function removes the rollback copy and then fails, the
// previous index cannot be restored; the error says so and where to look.
func TestWithIndexRollbackRestoreFailure(t *testing.T) {
	idx := t.TempDir()
	dir := filepath.Join(idx, "demo")
	writeFile(t, filepath.Join(dir, "demo.meta.json"), `{"name":"demo","vector_ready":true}`)
	boom := errors.New("rebuild blew up")
	err := withIndexRollback(idx, "demo", newPrinter(nil, nil), func() error {
		if err := os.RemoveAll(SyncBackupPath(idx, "demo")); err != nil {
			t.Fatal(err)
		}
		return boom
	})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "restoring the previous index failed") {
		t.Fatalf("err = %v", err)
	}
}

// A guard held by a live rewrite stops a rollback-protected rewrite; a
// rollback copy left by a crash is settled first and reported.
func TestWithIndexRollbackSettlesInterruptedRun(t *testing.T) {
	idx := t.TempDir()
	dir := filepath.Join(idx, "demo")
	writeFile(t, filepath.Join(dir, "demo.meta.json"), `{"name":"demo","vector_ready":true}`)
	writeFile(t, filepath.Join(SyncBackupPath(idx, "demo"), "demo.meta.json"), `{"name":"demo"}`)
	var out strings.Builder
	if err := withIndexRollback(idx, "demo", newPrinter(&out, nil), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "previous sync of \"demo\" was interrupted") {
		t.Errorf("recovery not reported: %q", out.String())
	}
	if HasSyncBackup(idx, "demo") {
		t.Error("the stale backup survived")
	}
}

// Sync reports a source tree that cannot be scanned.
func TestSyncMissingSourceTree(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "gone")
	if err := os.RemoveAll(docs); err != nil {
		t.Fatal(err)
	}
	_, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "gone"})
	mustContain(t, err, "scanning workspace")
}

// A vault whose change detection fails is not fatal: every eligible file is
// treated as changed and the sync still completes.
func TestSyncSurvivesBrokenTracker(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "trk")
	// A manifest that cannot be read leaves the tracker to decide what changed.
	execDB(t, filepath.Join(env.cfg.IndexDir, "trk", "trk.db"), `ALTER TABLE files RENAME COLUMN mtime_ns TO zz`)
	// Zero timestamps: the bootstrap falls back to the creation time.
	_ = gleann.UpdateIndexMeta(env.cfg.IndexDir, "trk", func(m *gleann.IndexMeta) { m.UpdatedAt = time.Time{} })
	tr, err := vault.NewTracker(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	tr.Close()

	o := env.options(nil)
	o.Tracker = tr
	var out strings.Builder
	o.Stdout, o.Stderr = &out, &out
	writeFile(t, filepath.Join(docs, "late.txt"), "A late addition about quokkas.")
	if _, err := Sync(context.Background(), SyncOptions{Options: o, Name: "trk"}); err != nil {
		t.Fatalf("Sync: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "change detection failed") {
		t.Errorf("tracker failure not reported:\n%s", out.String())
	}
	if got := passageSet(t, env.cfg, "trk"); !strings.Contains(strings.Join(got, "\n"), "quokkas") {
		t.Errorf("late file not indexed: %v", got)
	}
}

// A sync whose incremental update fails while the caller's context ends
// returns the context error instead of falling back to a full rebuild.
func TestSyncCancelledDuringIncrementalUpdate(t *testing.T) {
	env, fc, opts := flakyEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "cx", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docs, "more.go"), "package main\n\nfunc More() int { return 3 }\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Embedder = &hookComputer{flakyComputer: fc, hook: cancel}
	_, err := Sync(ctx, SyncOptions{Options: opts, Name: "cx"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	_ = env
}

// hookComputer runs hook on the first embedding request and fails it.
type hookComputer struct {
	*flakyComputer
	hook func()
}

func (h *hookComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	h.hook()
	return nil, errEmbedderDown
}

func (h *hookComputer) ComputeSingle(ctx context.Context, s string) ([]float32, error) {
	h.hook()
	return nil, errEmbedderDown
}

// An embedder that cannot be started (no llama-server for the llamacpp
// provider) fails the operation with a clear error instead of a panic.
func TestEmbedderStartFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("PATH", t.TempDir())
	env := newEnv(t)
	docs := fixture(t)
	opts := env.options(nil)
	opts.Config.EmbeddingProvider = "llamacpp"
	opts.Config.EmbeddingModel = "no-such-model"

	_, err := Build(context.Background(), BuildOptions{Options: opts, Name: "ll", DocsDir: docs})
	mustContain(t, err, "initializing embedder")

	// An existing index synced with that provider cannot start it either.
	built := newEnv(t)
	buildFor(t, built, "ll2")
	o := built.options(nil)
	o.Config.EmbeddingProvider = "llamacpp"
	writeFile(t, filepath.Join(docs, "x.txt"), "x")
	_, err = RebuildIndex(context.Background(), "ll2", docs, o)
	mustContain(t, err, "initializing embedder")
}
