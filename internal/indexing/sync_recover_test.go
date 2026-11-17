package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// A killed rebuild leaves the only good copy in <dir>.sync-bak and a partial
// <dir>. The next sync must restore it, not delete it.
func TestSyncRestoresBackupAfterKilledRebuild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		partial func(t *testing.T, dir string)
	}{
		{"missing", func(t *testing.T, dir string) {}},
		{"passages only", func(t *testing.T, dir string) {
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "m.passages.db"), []byte("garbage"), 0o644)
		}},
		{"lexical-only meta", func(t *testing.T, dir string) {
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "m.meta.json"), []byte(`{"name":"m","lexical_only":true}`), 0o644)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			docs := buildFor(t, env, "m")
			want, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")
			dir := filepath.Join(env.cfg.IndexDir, "m")
			if err := os.Rename(dir, SyncBackupPath(env.cfg.IndexDir, "m")); err != nil {
				t.Fatal(err)
			}
			tc.partial(t, dir)

			res, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "m", DocsDir: docs})
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if res == nil {
				t.Fatal("no result")
			}
			if HasSyncBackup(env.cfg.IndexDir, "m") {
				t.Error("backup left behind")
			}
			assertUntouched(t, env, "m", *want)
		})
	}
}

// A complete index next to a backup means the rebuild finished and the process
// died before cleaning up: the backup is stale and goes.
func TestSyncDiscardsStaleBackup(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	want, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")
	dir := filepath.Join(env.cfg.IndexDir, "m")
	if err := os.CopyFS(SyncBackupPath(env.cfg.IndexDir, "m"), os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "m", DocsDir: docs}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if HasSyncBackup(env.cfg.IndexDir, "m") {
		t.Error("stale backup not removed")
	}
	assertUntouched(t, env, "m", *want)
}

// withIndexRollback itself must not delete a leftover backup before the rebuild.
func TestRollbackKeepsGoodBackupFromKilledRun(t *testing.T) {
	env := newEnv(t)
	buildFor(t, env, "m")
	want, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")
	dir := filepath.Join(env.cfg.IndexDir, "m")
	if err := os.Rename(dir, SyncBackupPath(env.cfg.IndexDir, "m")); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "m.passages.db"), []byte("garbage"), 0o644)

	boom := errors.New("embedder down")
	err := withIndexRollback(env.cfg.IndexDir, "m", newPrinter(nil, nil), func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, env, "m", *want)
	if HasSyncBackup(env.cfg.IndexDir, "m") {
		t.Error("backup left behind")
	}
}

// Another live process owns the backup: do not touch it.
func TestRecoverLeavesBackupOfALiveRewrite(t *testing.T) {
	idx := t.TempDir()
	bak := SyncBackupPath(idx, "m")
	if err := os.MkdirAll(bak, 0o755); err != nil {
		t.Fatal(err)
	}
	release, err := acquireRewriteGuard(idx, "m")
	if err != nil {
		t.Fatal(err)
	}
	_, err = RecoverSyncBackup(context.Background(), idx, "m")
	release()
	if !errors.Is(err, ErrIndexBusy) {
		t.Fatalf("err = %v, want ErrIndexBusy", err)
	}
	if !HasSyncBackup(idx, "m") {
		t.Error("backup of a live rewrite was removed")
	}
}
