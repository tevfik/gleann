package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func writeBytes(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// `gleann info` lists the format-2 database and its WAL files.
func TestIndexFileSizesListsFormat2Database(t *testing.T) {
	dir := t.TempDir()
	writeBytes(t, filepath.Join(dir, "x.db"), 100)
	writeBytes(t, filepath.Join(dir, "x.db-wal"), 20)
	writeBytes(t, filepath.Join(dir, "x.db-shm"), 3)
	writeBytes(t, filepath.Join(dir, "x.meta.json"), 7)
	got := map[string]int64{}
	for _, f := range indexFileSizes(dir, "x") {
		got[f.ext] = f.size
	}
	for ext, want := range map[string]int64{".db": 100, ".db-wal": 20, ".db-shm": 3, ".meta.json": 7} {
		if got[ext] != want {
			t.Errorf("%s = %d, want %d (all: %v)", ext, got[ext], want, got)
		}
	}
}

// The storage-switch backup keeps the format-2 database with its WAL and a
// failed switch puts all of it back, dropping the log of the failed attempt.
func TestStorageBackupRestoresFormat2Database(t *testing.T) {
	dir := t.TempDir()
	bak := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	write("x.db", "db-old")
	write("x.db-wal", "wal-old")
	write("x.index", "idx-old")

	saved := backupIndexFiles(dir, "x", bak)
	if len(saved) != 3 {
		t.Fatalf("saved %v, want .index .db .db-wal", saved)
	}
	// The failed switch rewrote everything and left a -shm behind.
	write("x.db", "db-new")
	write("x.db-wal", "wal-new")
	write("x.db-shm", "shm-new")
	write("x.index", "idx-new")

	restoreIndexFiles(dir, "x", bak, saved)
	for name, want := range map[string]string{"x.db": "db-old", "x.db-wal": "wal-old", "x.index": "idx-old", "x.db-shm": "<missing>"} {
		if got := read(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// No WAL at backup time: the one the failed attempt created is removed.
	dir2, bak2 := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "y.db"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved = backupIndexFiles(dir2, "y", bak2)
	writeBytes(t, filepath.Join(dir2, "y.db-wal"), 5)
	restoreIndexFiles(dir2, "y", bak2, saved)
	if _, err := os.Stat(filepath.Join(dir2, "y.db-wal")); !os.IsNotExist(err) {
		t.Errorf("stale -wal of the failed attempt was kept (%v)", err)
	}
}

func TestInspectVectorStorageCountsFormat2Database(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(dir, "x")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBytes(t, filepath.Join(idx, "x.db"), 1000)
	writeBytes(t, filepath.Join(idx, "x.db-wal"), 24)
	vs := gleann.InspectVectorStorage(dir, gleann.IndexMeta{Name: "x", Backend: "hnsw"})
	if vs.PassageBytes != 1024 {
		t.Fatalf("PassageBytes = %d, want 1024", vs.PassageBytes)
	}
}
