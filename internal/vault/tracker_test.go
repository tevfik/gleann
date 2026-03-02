package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTracker(t *testing.T) {
	// Setup temp dir and files
	tmpDir, err := os.MkdirTemp("", "vault_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "vault.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	// 1. Create a dummy file
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("hello leannvault")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Upsert it
	hash, err := tracker.UpsertFile(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Upserted hash: %s", hash)

	// 3. Find path by hash
	path, err := tracker.GetPathByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if path != testFile {
		t.Fatalf("Expected path %s, got %s", testFile, path)
	}

	// 4. Find hash by path
	gotHash, err := tracker.GetHashByPath(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != hash {
		t.Fatalf("Expected hash %s, got %s", hash, gotHash)
	}

	// 5. Simulate rename (move)
	newFile := filepath.Join(tmpDir, "test_moved.txt")
	if err := os.Rename(testFile, newFile); err != nil {
		t.Fatal(err)
	}

	// Note: In real life Watcher triggers an Upsert on Create event.
	// We simulate the re-scan.
	newHash, err := tracker.UpsertFile(ctx, newFile)
	if err != nil {
		t.Fatal(err)
	}
	if newHash != hash {
		t.Fatalf("Hash should not change upon move. Expected %s, got %s", hash, newHash)
	}

	// Check path by hash points to the new place
	path, err = tracker.GetPathByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if path != newFile {
		t.Fatalf("Expected moved path %s, got %s", newFile, path)
	}

	// 6. Delete
	if err := tracker.RemoveByHash(ctx, hash); err != nil {
		t.Fatal(err)
	}

	_, err = tracker.GetPathByHash(ctx, hash)
	if err == nil {
		t.Fatal("Expected error finding removed hash")
	}
}
