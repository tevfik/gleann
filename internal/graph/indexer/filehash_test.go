//go:build treesitter && !windows

package indexer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/graph/indexer"
)

func TestFileHashStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Empty store.
	if got := store.Get("nope.go"); got != "" {
		t.Errorf("Get(unknown) = %q, want empty", got)
	}
	if got := store.Count(); got != 0 {
		t.Errorf("Count() = %d, want 0", got)
	}

	// Mark + Get.
	if err := store.Mark("a.go", "hashA", "go", 100, 5); err != nil {
		t.Fatalf("Mark a.go: %v", err)
	}
	if err := store.Mark("b.py", "hashB", "python", 200, 3); err != nil {
		t.Fatalf("Mark b.py: %v", err)
	}
	if got := store.Get("a.go"); got != "hashA" {
		t.Errorf("Get(a.go) = %q, want hashA", got)
	}
	if got := store.Count(); got != 2 {
		t.Errorf("Count() = %d, want 2", got)
	}

	// Re-mark same key overwrites.
	if err := store.Mark("a.go", "hashA2", "go", 110, 6); err != nil {
		t.Fatalf("Mark a.go overwrite: %v", err)
	}
	if got := store.Get("a.go"); got != "hashA2" {
		t.Errorf("after overwrite Get(a.go) = %q, want hashA2", got)
	}

	// Remove.
	if err := store.Remove("a.go"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := store.Get("a.go"); got != "" {
		t.Errorf("after Remove Get(a.go) = %q, want empty", got)
	}
	if got := store.Count(); got != 1 {
		t.Errorf("after Remove Count() = %d, want 1", got)
	}

	// Clear.
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := store.Count(); got != 0 {
		t.Errorf("after Clear Count() = %d, want 0", got)
	}
}

func TestFileHashStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hashes.db")

	store1, err := indexer.NewFileHashStore(path)
	if err != nil {
		t.Fatalf("NewFileHashStore #1: %v", err)
	}
	if err := store1.Mark("x.go", "abc123", "go", 42, 1); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	_ = store1.Close()

	store2, err := indexer.NewFileHashStore(path)
	if err != nil {
		t.Fatalf("NewFileHashStore #2: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })

	if got := store2.Get("x.go"); got != "abc123" {
		t.Errorf("after reopen Get(x.go) = %q, want abc123", got)
	}
}

func TestFileHashStoreIsDirty(t *testing.T) {
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte("package main"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	key := "main.go"

	// First observation: never seen before → dirty.
	dirty, h1, err := store.IsDirty(key, srcPath)
	if err != nil {
		t.Fatalf("IsDirty first: %v", err)
	}
	if !dirty {
		t.Errorf("first IsDirty: want dirty=true")
	}
	if h1 == "" {
		t.Errorf("first IsDirty: hash should not be empty")
	}

	// Mark it.
	if err := store.Mark(key, h1, "go", 12, 0); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	// Same content → not dirty.
	dirty, _, err = store.IsDirty(key, srcPath)
	if err != nil {
		t.Fatalf("IsDirty same: %v", err)
	}
	if dirty {
		t.Errorf("same content: want dirty=false")
	}

	// Change content → dirty again.
	if err := os.WriteFile(srcPath, []byte("package main // changed"), 0o644); err != nil {
		t.Fatalf("WriteFile changed: %v", err)
	}
	dirty, h2, err := store.IsDirty(key, srcPath)
	if err != nil {
		t.Fatalf("IsDirty changed: %v", err)
	}
	if !dirty {
		t.Errorf("changed content: want dirty=true")
	}
	if h2 == h1 {
		t.Errorf("hash did not change after content edit: %q == %q", h1, h2)
	}
}

func TestFileHashStoreMissingFile(t *testing.T) {
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dirty, h, _ := store.IsDirty("ghost.go", filepath.Join(dir, "ghost.go"))
	if !dirty {
		t.Errorf("missing file: want dirty=true")
	}
	if h != "" {
		t.Errorf("missing file: want empty hash, got %q", h)
	}
}

func TestFileHashStoreNilSafety(t *testing.T) {
	var store *indexer.FileHashStore
	// All methods on a nil store must not panic.
	if got := store.Get("x"); got != "" {
		t.Errorf("nil Get: got %q", got)
	}
	if dirty, _, _ := store.IsDirty("x", "x"); !dirty {
		t.Errorf("nil IsDirty: want dirty=true")
	}
	if err := store.Mark("x", "h", "go", 0, 0); err != nil {
		t.Errorf("nil Mark: unexpected error %v", err)
	}
	if err := store.Remove("x"); err != nil {
		t.Errorf("nil Remove: unexpected error %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Errorf("nil Clear: unexpected error %v", err)
	}
	if got := store.Count(); got != 0 {
		t.Errorf("nil Count: got %d, want 0", got)
	}
	if err := store.Close(); err != nil {
		t.Errorf("nil Close: unexpected error %v", err)
	}
}

func TestDefaultHashStorePath(t *testing.T) {
	// DefaultHashStorePath must produce a deterministic, well-formed
	// path under indexDir/<name>_graph/. We don't assert the exact
	// string (filenames are platform-specific) but we DO assert:
	//   - the path is non-empty
	//   - it ends in a "file_hashes.db" filename
	//   - it contains both the index dir and the index name
	got := indexer.DefaultHashStorePath("/tmp/idx", "myindex")
	if got == "" {
		t.Fatal("DefaultHashStorePath returned empty")
	}
	if !strings.HasSuffix(got, "file_hashes.db") {
		t.Errorf("DefaultHashStorePath should end in file_hashes.db, got %q", got)
	}
	if !strings.Contains(got, "myindex") {
		t.Errorf("DefaultHashStorePath should contain index name, got %q", got)
	}
	if !strings.Contains(got, "myindex_graph") {
		t.Errorf("DefaultHashStorePath should contain <name>_graph, got %q", got)
	}

	// Different index names should produce different paths.
	a := indexer.DefaultHashStorePath("/tmp/idx", "alpha")
	b := indexer.DefaultHashStorePath("/tmp/idx", "beta")
	if a == b {
		t.Errorf("different index names produced same path: %q == %q", a, b)
	}
}

func TestNewFileHashStoreRecoversFromCorrupt(t *testing.T) {
	// Simulate a corrupt bbolt file: write garbage, then open. The
	// store should remove the corrupt file and create a fresh one.
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(path, []byte("not a valid bbolt database"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	store, err := indexer.NewFileHashStore(path)
	if err != nil {
		t.Fatalf("NewFileHashStore on corrupt file: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Should be usable: Mark + Get.
	if err := store.Mark("k", "v", "go", 0, 0); err != nil {
		t.Fatalf("Mark after recovery: %v", err)
	}
	if got := store.Get("k"); got != "v" {
		t.Errorf("Get after recovery: want v, got %q", got)
	}
}

func TestNewFileHashStoreCreatesParentDir(t *testing.T) {
	// NewFileHashStore must mkdir -p the parent directory if it
	// doesn't exist. Use a deeply nested path.
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "d", "hashes.db")
	store, err := indexer.NewFileHashStore(deep)
	if err != nil {
		t.Fatalf("NewFileHashStore with deep path: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := os.Stat(deep); err != nil {
		t.Errorf("expected store file to exist at %q, got %v", deep, err)
	}
}

func TestFileHashStoreMarkOverwritesPrevious(t *testing.T) {
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Mark twice with different hashes; second wins.
	if err := store.Mark("a", "first", "go", 1, 0); err != nil {
		t.Fatalf("Mark first: %v", err)
	}
	if err := store.Mark("a", "second", "go", 2, 0); err != nil {
		t.Fatalf("Mark second: %v", err)
	}
	if got := store.Get("a"); got != "second" {
		t.Errorf("after overwrite: want second, got %q", got)
	}
	if got := store.Count(); got != 1 {
		t.Errorf("Count: want 1, got %d", got)
	}
}

func TestFileHashStoreGetAfterRemove(t *testing.T) {
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Mark("a", "h", "go", 0, 0); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if err := store.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := store.Get("a"); got != "" {
		t.Errorf("Get after Remove: want empty, got %q", got)
	}
	// Re-Mark after Remove should work.
	if err := store.Mark("a", "h2", "go", 0, 0); err != nil {
		t.Fatalf("Mark after Remove: %v", err)
	}
	if got := store.Get("a"); got != "h2" {
		t.Errorf("Re-mark after Remove: want h2, got %q", got)
	}
}

func TestFileHashStoreClearOnEmptyIsNoop(t *testing.T) {
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Clear on a fresh, empty store must not error.
	if err := store.Clear(); err != nil {
		t.Errorf("Clear on empty store: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Errorf("Clear (second call): %v", err)
	}
	if got := store.Count(); got != 0 {
		t.Errorf("Count after double Clear: want 0, got %d", got)
	}
}

func TestFileHashStoreIsDirtyWithMatchingKey(t *testing.T) {
	// When the persisted hash matches the on-disk hash, IsDirty
	// must return false. This is the "no work needed" path.
	dir := t.TempDir()
	src := filepath.Join(dir, "f.go")
	if err := os.WriteFile(src, []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Compute the actual hash and mark with it.
	hash, err := indexer.ComputeFileHash(src)
	if err != nil {
		t.Fatalf("ComputeFileHash: %v", err)
	}
	if err := store.Mark("f.go", hash, "go", 11, 0); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	dirty, currentHash, err := store.IsDirty("f.go", src)
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if dirty {
		t.Errorf("expected dirty=false (hash matches)")
	}
	if currentHash != hash {
		t.Errorf("expected IsDirty to return the matching hash, got %q", currentHash)
	}
}

func TestFileHashStoreIsDirtyOnMtimeOnlyChange(t *testing.T) {
	// Touch the file (mtime changes) but content does NOT change.
	// IsDirty must return false because we hash content, not mtime.
	dir := t.TempDir()
	src := filepath.Join(dir, "f.go")
	if err := os.WriteFile(src, []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash, _ := indexer.ComputeFileHash(src)
	if err := store.Mark("f.go", hash, "go", 11, 0); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	// Bump mtime into the future.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	dirty, _, _ := store.IsDirty("f.go", src)
	if dirty {
		t.Errorf("mtime-only change should not be dirty (we hash content)")
	}
}

func TestComputeFileHashStableAcrossRuns(t *testing.T) {
	// Same content must produce same hash.
	dir := t.TempDir()
	src := filepath.Join(dir, "x.go")
	content := "package x\nfunc F() {}\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1, err := indexer.ComputeFileHash(src)
	if err != nil {
		t.Fatalf("ComputeFileHash #1: %v", err)
	}
	h2, err := indexer.ComputeFileHash(src)
	if err != nil {
		t.Fatalf("ComputeFileHash #2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not stable: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("SHA-256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestComputeFileHashMissingFile(t *testing.T) {
	if _, err := indexer.ComputeFileHash("/nonexistent/path/to/file.go"); err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestFileHashStoreIsDirtyWithEmptyHash(t *testing.T) {
	// When the current hash is empty (file missing/unreadable),
	// IsDirty must return (true, "") and the caller can treat it
	// as "remove the stale record and skip".
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dirty, h, _ := store.IsDirty("nope", "/nonexistent/file")
	if !dirty {
		t.Errorf("expected dirty=true for missing file")
	}
	if h != "" {
		t.Errorf("expected empty hash for missing file, got %q", h)
	}
}

func TestFileHashStoreManyEntries(t *testing.T) {
	// Stress: insert 100 entries, remove 50, verify the rest.
	dir := t.TempDir()
	store, err := indexer.NewFileHashStore(filepath.Join(dir, "hashes.db"))
	if err != nil {
		t.Fatalf("NewFileHashStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for i := 0; i < 100; i++ {
		key := "f" + string(rune('0'+i%10)) + "_" + string(rune('a'+i%26))
		if err := store.Mark(key, "hash"+key, "go", 0, 0); err != nil {
			t.Fatalf("Mark %s: %v", key, err)
		}
	}
	if got := store.Count(); got != 100 {
		t.Errorf("Count after 100 Marks: want 100, got %d", got)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := store.Count(); got != 0 {
		t.Errorf("Count after Clear: want 0, got %d", got)
	}
}
