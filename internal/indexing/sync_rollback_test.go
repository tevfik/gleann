package indexing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// C-01: a failing rebuild must restore the previous index, metadata included.
func TestWithIndexRollback(t *testing.T) {
	idx := t.TempDir()
	dir := filepath.Join(idx, "demo")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "demo.passages.db"), []byte("OLD"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "demo.meta.json"), []byte(`{"name":"demo","source_dir":"/src","tags":["t"],"vector_ready":true}`), 0o644)

	// Failure: old index restored byte for byte.
	err := withIndexRollback(idx, "demo", func() error {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "demo.meta.json"), []byte(`{"name":"demo","lexical_only":true}`), 0o644)
		return errors.New("embedder down")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	m, e := gleann.GetIndexMeta(idx, "demo")
	if e != nil || !m.VectorReady || m.SourceDir != "/src" || m.LexicalOnly {
		t.Fatalf("index not restored: %+v %v", m, e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "demo.passages.db")); string(b) != "OLD" {
		t.Fatalf("passages changed: %s", b)
	}
	if _, e := os.Stat(dir + ".sync-bak"); e == nil {
		t.Fatal("backup dir left behind")
	}

	// Success: user-owned metadata carried over to the new index.
	err = withIndexRollback(idx, "demo", func() error {
		_ = os.MkdirAll(dir, 0o755)
		return os.WriteFile(filepath.Join(dir, "demo.meta.json"), []byte(`{"name":"demo","vector_ready":true}`), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _ = gleann.GetIndexMeta(idx, "demo")
	if m.SourceDir != "/src" || len(m.Tags) != 1 {
		t.Fatalf("metadata lost across rebuild: %+v", m)
	}
}
