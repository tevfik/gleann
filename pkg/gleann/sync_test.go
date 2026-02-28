package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileSynchronizerLoadSaveState(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileSynchronizer(tmpDir)

	// Load non-existent state should return empty.
	state, err := fs.LoadState("test-index")
	if err != nil {
		t.Fatalf("LoadState error: %v", err)
	}
	if len(state.Files) != 0 {
		t.Errorf("expected empty files, got %d", len(state.Files))
	}
	if state.IndexName != "test-index" {
		t.Errorf("expected index name 'test-index', got %q", state.IndexName)
	}

	// Save and reload.
	state.Files["main.go"] = &FileState{
		Path:     "main.go",
		Hash:     "abc123",
		Size:     100,
		Passages: []int64{0, 1, 2},
	}
	state.NextID = 3
	state.TotalFiles = 1

	if err := fs.SaveState(state); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}

	loaded, err := fs.LoadState("test-index")
	if err != nil {
		t.Fatalf("LoadState after save error: %v", err)
	}
	if len(loaded.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(loaded.Files))
	}
	if loaded.NextID != 3 {
		t.Errorf("expected NextID 3, got %d", loaded.NextID)
	}
	if loaded.Files["main.go"].Hash != "abc123" {
		t.Errorf("expected hash abc123, got %s", loaded.Files["main.go"].Hash)
	}
}

func TestFileSynchronizerDetectChanges(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	os.MkdirAll(srcDir, 0o755)

	// Create test files.
	os.WriteFile(filepath.Join(srcDir, "a.go"), []byte("package a"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "b.go"), []byte("package b"), 0o644)

	fs := NewFileSynchronizer(filepath.Join(tmpDir, "state"))
	state, _ := fs.LoadState("test")

	// First scan — all files are new.
	result, err := fs.DetectChanges(state, srcDir, []string{".go"})
	if err != nil {
		t.Fatalf("DetectChanges error: %v", err)
	}
	if len(result.Added) != 2 {
		t.Errorf("expected 2 added files, got %d", len(result.Added))
	}
	if !result.HasChanges() {
		t.Error("expected HasChanges() to be true")
	}

	// Record state.
	for _, f := range result.Added {
		fs.UpdateFileState(state, srcDir, f, 1)
	}

	// No changes on second scan.
	result2, _ := fs.DetectChanges(state, srcDir, []string{".go"})
	if result2.HasChanges() {
		t.Errorf("expected no changes, got added=%d modified=%d deleted=%d",
			len(result2.Added), len(result2.Modified), len(result2.Deleted))
	}

	// Delete a file.
	os.Remove(filepath.Join(srcDir, "b.go"))
	result3, _ := fs.DetectChanges(state, srcDir, []string{".go"})
	if len(result3.Deleted) != 1 {
		t.Errorf("expected 1 deleted file, got %d", len(result3.Deleted))
	}
}

func TestFileSynchronizerRemoveFile(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileSynchronizer(tmpDir)
	state, _ := fs.LoadState("test")

	state.Files["main.go"] = &FileState{
		Path:     "main.go",
		Passages: []int64{5, 6, 7},
	}
	state.TotalFiles = 1

	ids := fs.RemoveFile(state, "main.go")
	if len(ids) != 3 {
		t.Errorf("expected 3 passage IDs, got %d", len(ids))
	}
	if state.TotalFiles != 0 {
		t.Errorf("expected TotalFiles 0, got %d", state.TotalFiles)
	}

	// Remove non-existent.
	ids2 := fs.RemoveFile(state, "nonexistent.go")
	if ids2 != nil {
		t.Errorf("expected nil for non-existent file, got %v", ids2)
	}
}

func TestMatchExtensions(t *testing.T) {
	tests := []struct {
		path       string
		extensions []string
		want       bool
	}{
		{"main.go", []string{".go"}, true},
		{"main.go", []string{"go"}, true}, // Without dot.
		{"main.py", []string{".go"}, false},
		{"main.go", nil, true},    // Empty = match all.
		{"main.go", []string{}, true},
	}

	for _, tt := range tests {
		got := matchExtensions(tt.path, tt.extensions)
		if got != tt.want {
			t.Errorf("matchExtensions(%q, %v) = %v, want %v", tt.path, tt.extensions, got, tt.want)
		}
	}
}

func TestSyncResultMethods(t *testing.T) {
	r := &SyncResult{
		Added:    []string{"a.go"},
		Modified: []string{"b.go"},
		Deleted:  []string{"c.go"},
	}

	if !r.HasChanges() {
		t.Error("expected HasChanges() true")
	}
	if r.TotalChanged() != 3 {
		t.Errorf("expected TotalChanged() 3, got %d", r.TotalChanged())
	}

	empty := &SyncResult{}
	if empty.HasChanges() {
		t.Error("expected empty HasChanges() false")
	}
}
