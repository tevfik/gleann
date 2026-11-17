package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.etcd.io/bbolt"
)

func newTestTracker(t *testing.T) *Tracker {
	t.Helper()
	tr, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

// A file that is not a bbolt database (an old SQLite vault, or garbage left
// by a crash) is replaced by a fresh database instead of failing every open.
func TestNewTracker_RecreatesCorruptDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vault.db")
	if err := os.WriteFile(dbPath, []byte("SQLite format 3\x00 definitely not bbolt"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := NewTracker(dbPath)
	if err != nil {
		t.Fatalf("NewTracker over a corrupt file: %v", err)
	}
	defer tr.Close()
	ctx := context.Background()
	if err := tr.UpsertRecord(ctx, "h1", "/x/a.txt", 1, 2); err != nil {
		t.Fatal(err)
	}
	if p, err := tr.GetPathByHash(ctx, "h1"); err != nil || p != "/x/a.txt" {
		t.Fatalf("GetPathByHash = %q, %v", p, err)
	}
}

func TestBatchUpsertRecords(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()

	if err := tr.BatchUpsertRecords(ctx, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}

	recs := []FileRecord{
		{Hash: "ha", Path: "/r/a.md", LastModified: 10, Size: 1},
		{Hash: "hb", Path: "/r/b.md", LastModified: 20, Size: 2},
	}
	if err := tr.BatchUpsertRecords(ctx, recs); err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		got, err := tr.GetRecordByPath(ctx, r.Path)
		if err != nil || got == nil || *got != r {
			t.Fatalf("GetRecordByPath(%s) = %+v, %v; want %+v", r.Path, got, err, r)
		}
	}

	// A later batch moves hash ha to a new path: the hash now resolves there.
	if err := tr.BatchUpsertRecords(ctx, []FileRecord{{Hash: "ha", Path: "/r/moved.md", LastModified: 30, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := tr.GetPathByHash(ctx, "ha"); p != "/r/moved.md" {
		t.Fatalf("hash ha resolves to %q, want /r/moved.md", p)
	}
	if h, _ := tr.GetHashByPath(ctx, "/r/moved.md"); h != "ha" {
		t.Fatalf("moved path hash = %q", h)
	}
}

// Touching a file (new mtime, same content) is not a change, and the stored
// mtime is refreshed so the next scan skips the hash again.
func TestDetectChangedFiles_TouchRefreshesRecord(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.UpsertFile(ctx, f); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(f, later, later); err != nil {
		t.Fatal(err)
	}

	changed, deleted, err := tr.DetectChangedFiles(ctx, dir, []string{f})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 || len(deleted) != 0 {
		t.Fatalf("touch: changed=%v deleted=%v, want none", changed, deleted)
	}
	rec, err := tr.GetRecordByPath(ctx, f)
	if err != nil || rec == nil {
		t.Fatalf("record after touch: %v, %v", rec, err)
	}
	if rec.LastModified != later.Unix() {
		t.Fatalf("LastModified = %d, want refreshed %d", rec.LastModified, later.Unix())
	}
}

// A listed file that vanished before the scan stat'ed it is skipped, neither
// changed nor deleted (it is still in the current list).
func TestDetectChangedFiles_VanishedTrackedFileIsSkipped(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	dir := t.TempDir()
	f := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.UpsertFile(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	changed, deleted, err := tr.DetectChangedFiles(ctx, dir, []string{f})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 || len(deleted) != 0 {
		t.Fatalf("changed=%v deleted=%v, want none", changed, deleted)
	}
}

// Relative paths resolve against the working directory both when stored as
// absolute and when queried relative, and a sibling directory sharing a name
// prefix is not "inside" the directory.
func TestTracker_RelativeAndPrefixPaths(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Chdir(root)

	abs := filepath.Join(root, "docs", "a.md")
	sibling := filepath.Join(root, "docs2", "b.md")
	for _, p := range []string{abs, sibling} {
		if err := tr.UpsertRecord(ctx, "h-"+filepath.Base(p), p, 1, 1); err != nil {
			t.Fatal(err)
		}
	}

	rec, err := tr.GetRecordByPath(ctx, filepath.Join("docs", "a.md"))
	if err != nil || rec == nil || rec.Path != abs {
		t.Fatalf("relative lookup = %+v, %v", rec, err)
	}
	if rec, err := tr.GetRecordByPath(ctx, "docs/missing.md"); err != nil || rec != nil {
		t.Fatalf("untracked lookup = %+v, %v; want nil, nil", rec, err)
	}

	paths, err := tr.ListPathsInDir(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != abs {
		t.Fatalf("ListPathsInDir(docs) = %v, want only %s", paths, abs)
	}

	// Removing by a relative path drops the absolute entry too.
	if err := tr.RemovePath(ctx, filepath.Join("docs", "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.GetHashByPath(ctx, abs); err == nil {
		t.Fatal("absolute path still tracked after relative RemovePath")
	}
	all, _ := tr.ListPathsInDir(ctx, root)
	sort.Strings(all)
	if len(all) != 1 || all[0] != sibling {
		t.Fatalf("remaining = %v, want only the sibling", all)
	}
}

// A record whose stored JSON is damaged reports an error rather than a zero
// record.
func TestTracker_DamagedRecordJSON(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	if err := tr.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket(bucketFiles).Put([]byte("hbad"), []byte("{not json")); err != nil {
			return err
		}
		return tx.Bucket(bucketPaths).Put([]byte("/bad/path"), []byte("hbad"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.GetPathByHash(ctx, "hbad"); err == nil {
		t.Error("GetPathByHash of damaged JSON returned no error")
	}
	if _, err := tr.GetRecordByPath(ctx, "/bad/path"); err == nil {
		t.Error("GetRecordByPath of damaged JSON returned no error")
	}
	// The path entry is removed by prefix even though its record cannot be
	// decoded; the undecodable record is left alone.
	n, err := tr.RemovePathsWithPrefix(ctx, "/bad/")
	if err != nil || n != 1 {
		t.Fatalf("RemovePathsWithPrefix = %d, %v; want 1", n, err)
	}
	if _, err := tr.RemovePathsWithPrefix(ctx, "   "); err == nil {
		t.Error("blank prefix accepted")
	}
}

// fakeWatcher builds a Watcher whose event and error channels the test drives
// directly, so the event loop is exercised without OS file notifications.
func fakeWatcher(tr *Tracker) (*Watcher, chan fsnotify.Event, chan error) {
	ev := make(chan fsnotify.Event)
	errs := make(chan error)
	return &Watcher{
		tracker: tr,
		watcher: &fsnotify.Watcher{Events: ev, Errors: errs},
		paths:   map[string]bool{},
	}, ev, errs
}

func waitEvent(t *testing.T, ch <-chan fsnotify.Event) fsnotify.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("no OnChange callback")
		return fsnotify.Event{}
	}
}

func TestWatcherLoop_EventHandling(t *testing.T) {
	tr := newTestTracker(t)
	w, ev, errs := fakeWatcher(tr)
	got := make(chan fsnotify.Event, 4)
	w.OnChange = func(e fsnotify.Event) { got <- e }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "note.md")
	if err := os.WriteFile(f, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write -> the file is hashed and tracked, callback fires.
	ev <- fsnotify.Event{Name: f, Op: fsnotify.Write}
	if e := waitEvent(t, got); e.Name != f {
		t.Fatalf("callback for %q", e.Name)
	}
	if h, err := tr.GetHashByPath(ctx, f); err != nil || h == "" {
		t.Fatalf("file not tracked after Write: %q, %v", h, err)
	}

	// A watcher error is logged and the loop keeps running.
	errs <- errors.New("overflow")

	// Chmod of a file that no longer exists: no upsert, no callback. Then a
	// Remove of it fires the callback (so sync can retract its passages)
	// while the hash stays tracked for a later rename.
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	ev <- fsnotify.Event{Name: f, Op: fsnotify.Chmod}
	ev <- fsnotify.Event{Name: f, Op: fsnotify.Remove}
	if e := waitEvent(t, got); e.Name != f || !e.Has(fsnotify.Remove) {
		t.Fatalf("callback %v, want Remove of %s", e, f)
	}
	if _, err := tr.GetHashByPath(ctx, f); err != nil {
		t.Fatalf("hash dropped on Remove: %v", err)
	}

	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		// A file that exists but cannot be read is logged, no callback.
		locked := filepath.Join(dir, "locked.md")
		if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
			t.Fatal(err)
		}
		ev <- fsnotify.Event{Name: locked, Op: fsnotify.Create}
		// A following Remove proves the loop survived and that the Create
		// did not fire a callback of its own.
		ev <- fsnotify.Event{Name: locked, Op: fsnotify.Remove}
		if e := waitEvent(t, got); !e.Has(fsnotify.Remove) {
			t.Fatalf("callback %v, want only the Remove", e)
		}
		if _, err := tr.GetHashByPath(ctx, locked); err == nil {
			t.Fatal("unreadable file was tracked")
		}
	}

	// Closing the channels ends the loop; sends would block forever if it
	// were still selecting on a different channel, so close is the last act.
	close(ev)
	close(errs)
}

// A directory created under a watched tree is watched too, so files created
// in it later reach the tracker.
func TestWatcher_NewSubdirectoryIsWatched(t *testing.T) {
	tr := newTestTracker(t)
	w, err := NewWatcher(tr)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	dir := t.TempDir()
	if err := w.AddDirectory(dir); err != nil {
		t.Fatal(err)
	}
	got := make(chan fsnotify.Event, 64)
	w.OnChange = func(e fsnotify.Event) { got <- e }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Writing files in sub until one is reported proves sub was added (the
	// first write can race the AddDirectory of the Create event).
	deadline := time.After(10 * time.Second)
	for i := 0; ; i++ {
		f := filepath.Join(sub, "f"+string(rune('a'+i%26))+".txt")
		if err := os.WriteFile(f, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-got:
			if filepath.Dir(e.Name) == sub {
				return
			}
		case <-deadline:
			t.Fatal("no event from the new subdirectory")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func TestWatcher_AddMissingDirectory(t *testing.T) {
	tr := newTestTracker(t)
	w, err := NewWatcher(tr)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.AddDirectory(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("AddDirectory of a missing directory returned no error")
	}
}

func TestComputeHash_DirectoryIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory fails earlier on windows")
	}
	if _, err := ComputeHash(t.TempDir()); err == nil {
		t.Fatal("ComputeHash of a directory returned no error")
	}
}

// Re-upserting a path with new content points the path at the new hash.
func TestUpsertRecord_PathChangesHash(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	if err := tr.UpsertRecord(ctx, "old", "/p/a.md", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.UpsertRecord(ctx, "new", "/p/a.md", 2, 2); err != nil {
		t.Fatal(err)
	}
	if h, _ := tr.GetHashByPath(ctx, "/p/a.md"); h != "new" {
		t.Fatalf("path hash = %q, want new", h)
	}
	rec, err := tr.GetRecordByPath(ctx, "/p/a.md")
	if err != nil || rec == nil || rec.Hash != "new" || rec.Size != 2 {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

// A path whose hash has no file record (an inconsistent database left by an
// interrupted older writer) reads as untracked, not as an error.
func TestGetRecordByPath_DanglingHash(t *testing.T) {
	tr := newTestTracker(t)
	if err := tr.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketPaths).Put([]byte("/d/x.md"), []byte("nohash"))
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := tr.GetRecordByPath(context.Background(), "/d/x.md")
	if err != nil || rec != nil {
		t.Fatalf("dangling hash: %+v, %v; want nil, nil", rec, err)
	}
}

// A database without the buckets (they are created on open, but a file
// written by another tool may lack them) behaves as an empty tracker.
func TestTracker_MissingBucketsReadAsEmpty(t *testing.T) {
	tr := newTestTracker(t)
	ctx := context.Background()
	if err := tr.UpsertRecord(ctx, "h", "/m/a.md", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.DeleteBucket(bucketPaths); err != nil {
			return err
		}
		return tx.DeleteBucket(bucketFiles)
	}); err != nil {
		t.Fatal(err)
	}
	if rec, err := tr.GetRecordByPath(ctx, "/m/a.md"); err != nil || rec != nil {
		t.Errorf("GetRecordByPath = %+v, %v", rec, err)
	}
	if ps, err := tr.ListPathsInDir(ctx, "/m"); err != nil || len(ps) != 0 {
		t.Errorf("ListPathsInDir = %v, %v", ps, err)
	}
	if err := tr.RemovePath(ctx, "/m/a.md"); err != nil {
		t.Errorf("RemovePath = %v", err)
	}
	if n, err := tr.RemovePathsWithPrefix(ctx, "/m/"); err != nil || n != 0 {
		t.Errorf("RemovePathsWithPrefix = %d, %v", n, err)
	}
}

// Every operation on a closed tracker reports an error instead of panicking
// or pretending the vault is empty.
func TestTracker_ClosedReturnsErrors(t *testing.T) {
	tr, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := tr.UpsertFile(ctx, f); err == nil {
		t.Error("UpsertFile on a closed tracker succeeded")
	}
	if err := tr.BatchUpsertRecords(ctx, []FileRecord{{Hash: "h", Path: f}}); err == nil {
		t.Error("BatchUpsertRecords on a closed tracker succeeded")
	}
	if _, err := tr.GetRecordByPath(ctx, f); err == nil {
		t.Error("GetRecordByPath on a closed tracker succeeded")
	}
	if _, err := tr.ListPathsInDir(ctx, dir); err == nil {
		t.Error("ListPathsInDir on a closed tracker succeeded")
	}
	if err := tr.RemovePath(ctx, f); err == nil {
		t.Error("RemovePath on a closed tracker succeeded")
	}
	if _, err := tr.RemovePathsWithPrefix(ctx, dir); err == nil {
		t.Error("RemovePathsWithPrefix on a closed tracker succeeded")
	}
	if _, _, err := tr.DetectChangedFiles(ctx, dir, []string{f}); err == nil {
		t.Error("DetectChangedFiles on a closed tracker succeeded")
	}
}

// UpsertFile of a file that does not exist fails and records nothing.
func TestTracker_UpsertMissingFile(t *testing.T) {
	tr, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	missing := filepath.Join(t.TempDir(), "gone.txt")
	if _, err := tr.UpsertFile(context.Background(), missing); err == nil {
		t.Fatal("UpsertFile of a missing file succeeded")
	}
	if rec, err := tr.GetRecordByPath(context.Background(), missing); err != nil || rec != nil {
		t.Errorf("a missing file was recorded: %+v, %v", rec, err)
	}
}
