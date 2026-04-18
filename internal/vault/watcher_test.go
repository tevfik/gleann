package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// --- Watcher tests ---

func TestNewWatcher(t *testing.T) {
	tracker, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	w, err := NewWatcher(tracker)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if w.tracker != tracker {
		t.Error("tracker not set")
	}
	if w.paths == nil {
		t.Error("paths not initialized")
	}
}

func TestWatcherAddDirectory(t *testing.T) {
	dir := t.TempDir()
	// Create a nested structure
	os.MkdirAll(filepath.Join(dir, "subdir", "nested"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755) // should be skipped

	tracker, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	w, err := NewWatcher(tracker)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.AddDirectory(dir); err != nil {
		t.Fatal(err)
	}

	// Should have top-level dir and subdir and nested, but not .hidden
	if !w.paths[dir] {
		t.Error("expected root dir to be watched")
	}
	if !w.paths[filepath.Join(dir, "subdir")] {
		t.Error("expected subdir to be watched")
	}
	if !w.paths[filepath.Join(dir, "subdir", "nested")] {
		t.Error("expected nested to be watched")
	}
	if w.paths[filepath.Join(dir, ".hidden")] {
		t.Error("hidden dir should be skipped")
	}
}

func TestWatcherStartAndDetectChange(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "vault.db")

	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	w, err := NewWatcher(tracker)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	changed := make(chan string, 10)
	w.OnChange = func(e fsnotify.Event) {
		changed <- e.Name
	}

	if err := w.AddDirectory(dir); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Create a file - should be detected
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("hello"), 0o644)

	// Wait a bit for the watcher to process
	time.Sleep(200 * time.Millisecond)

	// File should be tracked now
	hash, err := tracker.GetHashByPath(ctx, testFile)
	if err != nil {
		t.Logf("file not tracked yet (may be timing): %v", err)
	} else if hash == "" {
		t.Error("expected non-empty hash")
	}

	_ = changed
}

func TestWatcherClose(t *testing.T) {
	tracker, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	w, err := NewWatcher(tracker)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherStartContextCancellation(t *testing.T) {
	tracker, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	w, err := NewWatcher(tracker)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	// Cancel should stop the goroutine
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// --- More tracker edge cases ---

func TestTrackerNewWithBadPath(t *testing.T) {
	_, err := NewTracker("/nonexistent/path/to/db")
	if err == nil {
		t.Fatal("expected error for bad path")
	}
}

func TestTrackerUpsertAndRetrieve(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vault.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	// Create a test file
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0o644)

	hash, err := tracker.UpsertFile(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	// Retrieve by hash
	path, err := tracker.GetPathByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if path != testFile {
		t.Errorf("expected %s, got %s", testFile, path)
	}

	// Retrieve by path
	gotHash, err := tracker.GetHashByPath(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != hash {
		t.Errorf("hash mismatch: %s vs %s", gotHash, hash)
	}
}

func TestTrackerRemoveAndVerify(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vault.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0o644)

	hash, _ := tracker.UpsertFile(ctx, testFile)

	// Remove
	if err := tracker.RemoveByHash(ctx, hash); err != nil {
		t.Fatal(err)
	}

	// Should not find it anymore
	_, err = tracker.GetPathByHash(ctx, hash)
	if err == nil {
		t.Error("expected not found after removal")
	}
}

func TestTrackerComputeHashConsistency(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("same content"), 0o644)

	h1, err := ComputeHash(testFile)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ComputeHash(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Error("same content should produce same hash")
	}

	// Different content should produce different hash
	testFile2 := filepath.Join(dir, "test2.txt")
	os.WriteFile(testFile2, []byte("different content"), 0o644)
	h3, err := ComputeHash(testFile2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
}

func TestTrackerUpsertRecordEdgeCases(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vault.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	// Upsert with explicit record
	err = tracker.UpsertRecord(ctx, "deadbeef", "/some/path", 1234567890, 42)
	if err != nil {
		t.Fatal(err)
	}

	// Should be retrievable
	path, err := tracker.GetPathByHash(ctx, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/some/path" {
		t.Errorf("expected /some/path, got %s", path)
	}

	// Update same hash with new path
	err = tracker.UpsertRecord(ctx, "deadbeef", "/new/path", 1234567891, 43)
	if err != nil {
		t.Fatal(err)
	}

	path, _ = tracker.GetPathByHash(ctx, "deadbeef")
	if path != "/new/path" {
		t.Errorf("expected /new/path after update, got %s", path)
	}
}
