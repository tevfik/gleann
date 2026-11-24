package indexing

import (
	"encoding/json"
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
	err := withIndexRollback(idx, "demo", newPrinter(nil, nil), func() error {
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
	// The rebuild writes its first meta.json with the old user fields (what
	// BuildPassages does through SetPreviousMeta); the restore keeps them.
	err = withIndexRollbackMeta(idx, "demo", newPrinter(nil, nil), func(prev *gleann.IndexMeta) error {
		_ = os.MkdirAll(dir, 0o755)
		fresh := gleann.IndexMeta{Name: "demo", VectorReady: true}
		gleann.MergeUserMeta(&fresh, prev, prev, prev)
		data, _ := json.Marshal(fresh)
		return os.WriteFile(filepath.Join(dir, "demo.meta.json"), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _ = gleann.GetIndexMeta(idx, "demo")
	if m.SourceDir != "/src" || len(m.Tags) != 1 {
		t.Fatalf("metadata lost across rebuild: %+v", m)
	}
}
