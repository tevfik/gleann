package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenReadOnlyNeverWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x", "x.db")
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("OpenReadOnly of a missing file succeeded")
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.WriterDB().ExecContext(context.Background(), `INSERT INTO meta(key,value) VALUES('zz','1')`); err == nil {
		t.Fatal("write through a read-only store succeeded")
	}
	var n int
	if err := ro.ReaderDB().QueryRow(`SELECT count(*) FROM meta`).Scan(&n); err != nil {
		t.Fatalf("read through a read-only store: %v", err)
	}
}
