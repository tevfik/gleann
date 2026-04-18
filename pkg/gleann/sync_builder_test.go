package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

// --- GetPassageIDs ---

func TestGetPassageIDs_Found(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		Files: map[string]*FileState{
			"foo.txt": {Path: "foo.txt", Passages: []int64{1, 2, 3}},
			"bar.txt": {Path: "bar.txt", Passages: []int64{4, 5}},
		},
	}

	ids := fs.GetPassageIDs(state, "foo.txt")
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}
}

func TestGetPassageIDs_NotFound(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{Files: map[string]*FileState{}}

	ids := fs.GetPassageIDs(state, "missing.txt")
	if ids != nil {
		t.Errorf("expected nil, got %v", ids)
	}
}

// --- DetectChanges comprehensive ---

func TestDetectChanges_NewFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.go"), []byte("go code"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{Files: map[string]*FileState{}}

	result, err := fs.DetectChanges(state, dir, []string{".txt"})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Added) != 2 {
		t.Errorf("expected 2 added, got %d: %v", len(result.Added), result.Added)
	}
	if len(result.Modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(result.Modified))
	}
	if len(result.Deleted) != 0 {
		t.Errorf("expected 0 deleted, got %d", len(result.Deleted))
	}
}

func TestDetectChanges_DeletedFiles(t *testing.T) {
	dir := t.TempDir()

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		Files: map[string]*FileState{
			"old.txt": {Path: "old.txt", Hash: "abc123"},
		},
	}

	result, err := fs.DetectChanges(state, dir, []string{".txt"})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Deleted) != 1 {
		t.Errorf("expected 1 deleted, got %d", len(result.Deleted))
	}
	if result.Deleted[0] != "old.txt" {
		t.Errorf("expected old.txt, got %s", result.Deleted[0])
	}
}

func TestDetectChanges_ModifiedFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new content"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		Files: map[string]*FileState{
			"a.txt": {Path: "a.txt", Hash: "wronghash", Size: 5},
		},
	}

	result, err := fs.DetectChanges(state, dir, []string{".txt"})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Modified) != 1 {
		t.Errorf("expected 1 modified, got %d", len(result.Modified))
	}
}

// --- LoadState + SaveState ---

func TestLoadState_NewState(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())

	state, err := fs.LoadState("testidx")
	if err != nil {
		t.Fatal(err)
	}
	if state.IndexName != "testidx" {
		t.Errorf("expected testidx, got %s", state.IndexName)
	}
	if state.Files == nil {
		t.Error("expected initialized Files map")
	}
}

func TestSaveAndLoadState(t *testing.T) {
	stateDir := t.TempDir()
	fs := NewFileSynchronizer(stateDir)

	state := &SyncState{
		IndexName: "myindex",
		Files: map[string]*FileState{
			"a.txt": {Path: "a.txt", Hash: "abc", Size: 10, Passages: []int64{0, 1}},
		},
		NextID:     2,
		TotalFiles: 1,
	}

	if err := fs.SaveState(state); err != nil {
		t.Fatal(err)
	}

	loaded, err := fs.LoadState("myindex")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NextID != 2 {
		t.Errorf("expected NextID 2, got %d", loaded.NextID)
	}
	if len(loaded.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(loaded.Files))
	}
}

// --- UpdateFileState ---

func TestUpdateFileState(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("content here"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		IndexName: "test",
		Files:     map[string]*FileState{},
		NextID:    0,
	}

	if err := fs.UpdateFileState(state, dir, "test.txt", 3); err != nil {
		t.Fatal(err)
	}

	entry, ok := state.Files["test.txt"]
	if !ok {
		t.Fatal("expected file entry")
	}
	if entry.Hash == "" {
		t.Error("expected hash")
	}
	if len(entry.Passages) != 3 {
		t.Errorf("expected 3 passages, got %d", len(entry.Passages))
	}
	if state.NextID != 3 {
		t.Errorf("expected NextID 3, got %d", state.NextID)
	}
}
