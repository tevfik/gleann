package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestComputeHash(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "hello.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	hash, err := ComputeHash(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 { // SHA-256 hex
		t.Errorf("expected 64-char hex hash, got %d chars", len(hash))
	}

	// Deterministic
	hash2, _ := ComputeHash(f)
	if hash != hash2 {
		t.Error("hash should be deterministic")
	}

	// Different content → different hash
	f2 := filepath.Join(tmpDir, "other.txt")
	os.WriteFile(f2, []byte("other content"), 0644)
	hash3, _ := ComputeHash(f2)
	if hash3 == hash {
		t.Error("different content should produce different hash")
	}
}

func TestComputeHashMissingFile(t *testing.T) {
	_, err := ComputeHash("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestDefaultDBPath(t *testing.T) {
	p := DefaultDBPath()
	if p == "" {
		t.Error("DefaultDBPath should not be empty")
	}
	if filepath.Base(p) != "vault.db" {
		t.Errorf("expected vault.db, got %s", filepath.Base(p))
	}
}

func TestTrackerUpsertRecord(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	// Upsert a record directly
	err = tracker.UpsertRecord(ctx, "abc123hash", "/some/path.go", 1700000000, 1024)
	if err != nil {
		t.Fatal(err)
	}

	// Retrieve by hash
	path, err := tracker.GetPathByHash(ctx, "abc123hash")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/some/path.go" {
		t.Errorf("unexpected path: %s", path)
	}

	// Retrieve by path
	hash, err := tracker.GetHashByPath(ctx, "/some/path.go")
	if err != nil {
		t.Fatal(err)
	}
	if hash != "abc123hash" {
		t.Errorf("unexpected hash: %s", hash)
	}
}

func TestTrackerRemoveByHash(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	// Insert and remove
	tracker.UpsertRecord(ctx, "deadbeef", "/tmp/file.go", 100, 200)
	err = tracker.RemoveByHash(ctx, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}

	// Should be gone
	_, err = tracker.GetPathByHash(ctx, "deadbeef")
	if err == nil {
		t.Error("expected error after removal")
	}
}

func TestTrackerGetPathNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	_, err = tracker.GetPathByHash(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent hash")
	}
}

func TestTrackerGetHashNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	_, err = tracker.GetHashByPath(context.Background(), "/no/such/path")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestTrackerUpsertFileExtended(t *testing.T) {
	tmpDir := t.TempDir()

	// Create DB
	dbPath := filepath.Join(tmpDir, "test.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	// Create test file
	testFile := filepath.Join(tmpDir, "sample.txt")
	os.WriteFile(testFile, []byte("sample content"), 0644)

	hash, err := tracker.UpsertFile(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}

	// Verify round-trip
	path, err := tracker.GetPathByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if path != testFile {
		t.Errorf("path mismatch: %s vs %s", path, testFile)
	}

	// Update content → hash changes
	os.WriteFile(testFile, []byte("updated content"), 0644)
	hash2, err := tracker.UpsertFile(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	if hash2 == hash {
		t.Error("hash should change when content changes")
	}
}

func TestTrackerUpsertFileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	_, err = tracker.UpsertFile(context.Background(), "/nonexistent/file.go")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestFileRecordJSON(t *testing.T) {
	r := FileRecord{
		Hash:         "abc",
		Path:         "/tmp/test.go",
		LastModified: 1700000000,
		Size:         1024,
	}
	if r.Hash != "abc" || r.Path != "/tmp/test.go" {
		t.Error("unexpected field values")
	}
}
