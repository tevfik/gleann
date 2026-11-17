package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A searcher opens the index while a rebuild holds the write lock for a
// long time (ReplaceAll runs as one transaction). Open of an existing,
// fully migrated database must not wait for that lock.
func TestOpenDuringLongWriteTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx.db")
	w, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	applyTexts(t, w, "a.go", []string{"alpha"})

	tx, err := w.writer.BeginTx(context.Background(), nil) // BEGIN IMMEDIATE
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('busy', '1')`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	r, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open while a write transaction is open: %v", err)
	}
	defer r.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Open waited %v for the write lock", d)
	}
	if n, err := r.Count(context.Background()); err != nil || n != 1 {
		t.Fatalf("read during the write transaction = %d, %v", n, err)
	}
}
