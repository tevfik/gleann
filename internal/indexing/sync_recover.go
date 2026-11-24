package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"

	"github.com/tevfik/gleann/internal/fsretry"
	"github.com/tevfik/gleann/pkg/gleann"
)

// A full rebuild during Sync moves the live index <dir> to <dir>.sync-bak and
// writes the new one into <dir> (withIndexRollback). If the process is killed
// in between, <dir> is partial or missing and the backup holds the only good
// copy. The next run must not throw it away: recoverSyncBackup decides which
// of the two is the index and clears the other.

// syncBackupSuffix is appended to an index directory to name its rollback copy.
const syncBackupSuffix = ".sync-bak"

// ErrIndexBusy is returned when another gleann process is rewriting the index.
var ErrIndexBusy = errors.New("index is being rewritten by another gleann process")

// SyncBackupPath returns where the rollback copy of index name lives.
func SyncBackupPath(indexDir, name string) string {
	return filepath.Join(indexDir, name) + syncBackupSuffix
}

// HasSyncBackup reports whether a rollback copy of the index is on disk: a
// sync is running right now, or one was interrupted and the index may need
// recovering (the next `gleann index sync <name>` does that).
func HasSyncBackup(indexDir, name string) bool {
	_, err := os.Stat(SyncBackupPath(indexDir, name))
	return err == nil
}

// acquireRewriteGuard takes a cross-process exclusive lock for rewriting index
// name. It is a bbolt file lock (flock / LockFileEx), which the OS releases
// when the process dies, so a killed sync never leaves the index "busy". The
// in-process lockIndex does not protect against other processes.
func acquireRewriteGuard(indexDir, name string) (func(), error) {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating index directory: %w", err)
	}
	db, err := bbolt.Open(filepath.Join(indexDir, name+".rewrite.lock"), 0o600, &bbolt.Options{Timeout: 100 * time.Millisecond})
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("%w (%q)", ErrIndexBusy, name)
		}
		return nil, fmt.Errorf("locking index %q: %w", name, err)
	}
	return func() { _ = db.Close() }, nil
}

// indexComplete reports whether <indexDir>/<name> is a whole, usable index and
// no worse than the rollback copy: its metadata reads, its passage store opens
// and agrees with the metadata, a vector index is present when one is expected,
// and it still has vectors if the previous index had them.
func indexComplete(indexDir, name string, prev *gleann.IndexMeta) bool {
	meta, err := gleann.GetIndexMeta(indexDir, name)
	if err != nil {
		return false
	}
	if prev != nil && prev.VectorReady && !meta.VectorReady {
		return false // a lexical-only leftover of an interrupted rebuild
	}
	h, err := gleann.CheckIndexHealth(indexDir, name)
	return err == nil && !h.NeedsRepair()
}

// RecoverSyncBackup settles a rollback copy left by an interrupted sync. It
// reports what it did: "" when there was no backup, "restored" when the backup
// became the index again, "discarded" when the index was complete and the
// backup stale. ErrIndexBusy means a live process owns the backup.
func RecoverSyncBackup(ctx context.Context, indexDir, name string) (string, error) {
	if !HasSyncBackup(indexDir, name) {
		return "", nil
	}
	unlock, err := lockIndex(ctx, indexDir, name)
	if err != nil {
		return "", err
	}
	defer unlock()
	release, err := acquireRewriteGuard(indexDir, name)
	if err != nil {
		return "", err
	}
	defer release()
	return recoverSyncBackupLocked(indexDir, name)
}

// recoverSyncBackupLocked is RecoverSyncBackup for a caller that holds both
// the in-process lock and the rewrite guard.
func recoverSyncBackupLocked(indexDir, name string) (string, error) {
	dir := filepath.Join(indexDir, name)
	bak := SyncBackupPath(indexDir, name)
	if _, err := os.Stat(bak); err != nil {
		return "", nil
	}
	prev := readBackupMeta(bak, name)
	if indexComplete(indexDir, name, prev) {
		if err := os.RemoveAll(bak); err != nil {
			return "", fmt.Errorf("removing the stale rollback copy %s: %w", bak, err)
		}
		return "discarded", nil
	}
	// <dir> is partial or missing: the backup is the index. Clear the partial
	// directory first; a rename onto an existing directory fails on Windows.
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clearing the partial index %s (the good copy is in %s): %w", dir, bak, err)
	}
	if err := fsretry.Rename(bak, dir); err != nil {
		return "", fmt.Errorf("restoring the index from %s: %w", bak, err)
	}
	return "restored", nil
}

// readBackupMeta reads the metadata inside the rollback copy; its files keep
// the index's name, not the directory's.
func readBackupMeta(bak, name string) *gleann.IndexMeta {
	data, err := os.ReadFile(filepath.Join(bak, name+".meta.json"))
	if err != nil {
		return nil
	}
	var m gleann.IndexMeta
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return &m
}

// PendingSyncBackups lists the indexes under indexDir that have a rollback
// copy on disk: a sync is rewriting them or was interrupted.
func PendingSyncBackups(indexDir string) []string {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); e.IsDir() && strings.HasSuffix(n, syncBackupSuffix) {
			names = append(names, strings.TrimSuffix(n, syncBackupSuffix))
		}
	}
	return names
}
