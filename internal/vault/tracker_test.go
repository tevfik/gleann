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

func TestDetectChangedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "vault.db")
	tracker, err := NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	ctx := context.Background()

	f1 := filepath.Join(tmpDir, "file1.txt")
	f2 := filepath.Join(tmpDir, "file2.txt")
	_ = os.WriteFile(f1, []byte("content 1"), 0644)
	_ = os.WriteFile(f2, []byte("content 2"), 0644)

	// Phase 1: Not tracked yet -> both should be detected as changed
	changed, deleted, err := tracker.DetectChangedFiles(ctx, tmpDir, []string{f1, f2})
	if err != nil {
		t.Fatalf("DetectChangedFiles: %v", err)
	}
	if len(changed) != 2 || len(deleted) != 0 {
		t.Fatalf("Expected 2 changed, 0 deleted, got changed=%v, deleted=%v", changed, deleted)
	}

	// Upsert both
	_, _ = tracker.UpsertFile(ctx, f1)
	_, _ = tracker.UpsertFile(ctx, f2)

	// Phase 2: Tracked & unchanged -> 0 changed, 0 deleted
	changed, deleted, err = tracker.DetectChangedFiles(ctx, tmpDir, []string{f1, f2})
	if err != nil {
		t.Fatalf("DetectChangedFiles: %v", err)
	}
	if len(changed) != 0 || len(deleted) != 0 {
		t.Fatalf("Expected 0 changed, 0 deleted, got changed=%v, deleted=%v", changed, deleted)
	}

	// Phase 3: Modify f1 content
	_ = os.WriteFile(f1, []byte("content 1 - modified"), 0644)

	changed, deleted, err = tracker.DetectChangedFiles(ctx, tmpDir, []string{f1, f2})
	if err != nil {
		t.Fatalf("DetectChangedFiles: %v", err)
	}
	if len(changed) != 1 || changed[0] != f1 {
		t.Fatalf("Expected [f1] changed, got %v", changed)
	}
	if len(deleted) != 0 {
		t.Fatalf("Expected 0 deleted, got %v", deleted)
	}

	// Re-track f1
	_, _ = tracker.UpsertFile(ctx, f1)

	// Phase 4: Delete f2, add f3
	_ = os.Remove(f2)
	f3 := filepath.Join(tmpDir, "file3.txt")
	_ = os.WriteFile(f3, []byte("content 3"), 0644)

	changed, deleted, err = tracker.DetectChangedFiles(ctx, tmpDir, []string{f1, f3})
	if err != nil {
		t.Fatalf("DetectChangedFiles: %v", err)
	}
	if len(changed) != 1 || changed[0] != f3 {
		t.Fatalf("Expected [f3] changed, got %v", changed)
	}
	if len(deleted) != 1 || deleted[0] != f2 {
		t.Fatalf("Expected [f2] deleted, got %v", deleted)
	}

	// Clean up deleted path
	_ = tracker.RemovePath(ctx, f2)
	changed, deleted, err = tracker.DetectChangedFiles(ctx, tmpDir, []string{f1, f3})
	if err != nil {
		t.Fatalf("DetectChangedFiles: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("Expected 0 deleted after cleanup, got %v", deleted)
	}
}

