package vault

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.etcd.io/bbolt"
)

// The store keys files by hash and paths by name; an empty key is not a
// valid bbolt key, so an upsert with no hash or no path fails and, being one
// transaction, writes nothing.
func TestUpsert_EmptyKeysAreRejected(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()

	if err := tr.UpsertRecord(ctx, "", "/a/b.md", 1, 1); err == nil {
		t.Error("UpsertRecord with an empty hash succeeded")
	}
	if err := tr.UpsertRecord(ctx, "h", "", 1, 1); err == nil {
		t.Error("UpsertRecord with an empty path succeeded")
	}
	if p, err := tr.GetPathByHash(ctx, "h"); err == nil {
		t.Errorf("the hash of the failed upsert was stored: %q", p)
	}

	// The same inside a batch: the bad record rolls back the good one.
	err := tr.BatchUpsertRecords(ctx, []FileRecord{
		{Hash: "good", Path: "/a/good.md"},
		{Hash: "", Path: "/a/nohash.md"},
	})
	if err == nil {
		t.Error("BatchUpsertRecords with an empty hash succeeded")
	}
	err = tr.BatchUpsertRecords(ctx, []FileRecord{{Hash: "h2", Path: ""}})
	if err == nil {
		t.Error("BatchUpsertRecords with an empty path succeeded")
	}
	if p, err := tr.GetPathByHash(ctx, "good"); err == nil {
		t.Errorf("a record of the failed batch was stored: %q", p)
	}
}

// A path entry whose files bucket vanished reads as untracked.
func TestGetRecordByPath_FilesBucketMissing(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	if err := tr.UpsertRecord(ctx, "h", "/m/a.md", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.db.Update(func(tx *bbolt.Tx) error { return tx.DeleteBucket(bucketFiles) }); err != nil {
		t.Fatal(err)
	}
	if rec, err := tr.GetRecordByPath(ctx, "/m/a.md"); err != nil || rec != nil {
		t.Fatalf("GetRecordByPath = %+v, %v; want untracked", rec, err)
	}
}

// UpsertFile reports a tracker whose database is closed.
func TestUpsertFile_ClosedTracker(t *testing.T) {
	tr, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.UpsertFile(context.Background(), f); err == nil {
		t.Fatal("UpsertFile on a closed tracker succeeded")
	}
}

// removedWorkingDir leaves the process in a working directory that no longer
// exists, so filepath.Abs of a relative path fails. It skips where the
// platform will not let a directory be removed under the process, or still
// reports a working directory.
func removedWorkingDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PWD", "")
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove the working directory: %v", err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("this platform still reports a working directory after its removal")
	}
}

// Without a working directory relative paths cannot be made absolute; every
// lookup falls back to the cleaned relative path instead of failing.
func TestTracker_RelativePathsWithoutWorkingDirectory(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	if err := tr.UpsertRecord(ctx, "ha", filepath.Join("rel", "a.md"), 1, 1); err != nil {
		t.Fatal(err)
	}
	removedWorkingDir(t)

	a := filepath.Join("rel", "a.md")
	rec, err := tr.GetRecordByPath(ctx, a)
	if err != nil || rec == nil || rec.Hash != "ha" {
		t.Fatalf("GetRecordByPath = %+v, %v", rec, err)
	}
	paths, err := tr.ListPathsInDir(ctx, "rel")
	if err != nil || !reflect.DeepEqual(paths, []string{a}) {
		t.Fatalf("ListPathsInDir = %v, %v", paths, err)
	}

	b := filepath.Join("rel", "b.md")
	changed, deleted, err := tr.DetectChangedFiles(ctx, "rel", []string{b})
	if err != nil || !reflect.DeepEqual(changed, []string{b}) || !reflect.DeepEqual(deleted, []string{a}) {
		t.Fatalf("DetectChangedFiles = %v, %v, %v", changed, deleted, err)
	}

	if err := tr.RemovePath(ctx, a); err != nil {
		t.Fatal(err)
	}
	if rec, err := tr.GetRecordByPath(ctx, a); err != nil || rec != nil {
		t.Fatalf("path still tracked after RemovePath: %+v, %v", rec, err)
	}
}

// A closed error channel ends the watcher loop, like a closed event channel.
func TestWatcherLoop_ErrorChannelClosed(t *testing.T) {
	tr := newTestTracker(t)
	w, ev, errs := fakeWatcher(tr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Each select of a live loop sees the closed error channel and a waiting
	// sender ready and picks one at random, so a loop that honours the
	// closed channel stops taking events within a few sends (all twenty
	// being taken has probability 2^-20). A loop that ignored the closed
	// channel would take every one.
	close(errs)
	gone := filepath.Join(t.TempDir(), "gone.md")
	for i := 0; i < 20; i++ {
		select {
		case ev <- fsnotify.Event{Name: gone, Op: fsnotify.Chmod}:
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
	t.Fatal("watcher loop still running after its error channel closed")
}
