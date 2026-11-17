package gleann

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectStoreFormatThreeWay(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if got := DetectStoreFormat(base); got != StoreNone {
		t.Fatalf("missing index = %v, want StoreNone", got)
	}
	if got := StoreFormatOf(base); got != 1 {
		t.Fatalf("StoreFormatOf(missing) = %d, want the documented 1", got)
	}

	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	if got := DetectStoreFormat(base); got != StoreV2 {
		t.Fatalf("format-2 index = %v, want StoreV2", got)
	}

	// Every trace of a v1.9 index without a format-2 database is the old
	// format: meta.json alone, a bbolt store, a lexical sidecar, a vector
	// file, or only a Kùzu <name>_graph directory.
	for _, tc := range []struct {
		name  string
		write func(base string) error
	}{
		{"meta", func(b string) error { return os.WriteFile(b+".meta.json", []byte(`{"name":"x"}`), 0o644) }},
		{"passages", func(b string) error { return os.WriteFile(b+".passages.db", []byte("bolt"), 0o644) }},
		{"lexdb", func(b string) error { return os.WriteFile(b+".lex.db", []byte("lex"), 0o644) }},
		{"vectors", func(b string) error { return os.WriteFile(b+".index", []byte("vec"), 0o644) }},
	} {
		old := filepath.Join(dir, tc.name, tc.name)
		if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := tc.write(old); err != nil {
			t.Fatal(err)
		}
		if got := DetectStoreFormat(old); got != StoreOld {
			t.Errorf("%s: DetectStoreFormat = %v, want StoreOld", tc.name, got)
		}
		if err := CheckStoreFormat(dir, tc.name); !errors.Is(err, ErrOldStoreFormat) {
			t.Errorf("%s: CheckStoreFormat = %v, want ErrOldStoreFormat", tc.name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "graphonly_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectStoreFormat(filepath.Join(dir, "graphonly", "graphonly")); got != StoreOld {
		t.Errorf("only <name>_graph: DetectStoreFormat = %v, want StoreOld", got)
	}
}

// A missing index is not an old-format index: nothing is created for it, and
// the callers get a typed error.
func TestMissingIndexIsNotTreatedAsOldFormat(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()

	if err := b.UpdateIndex(ctx, "ghost", nil, []string{"x"}); err != nil {
		t.Fatalf("removing from a missing index = %v, want nil", err)
	}
	err := b.AddToIndex(ctx, "ghost", f2Items())
	if !errors.Is(err, ErrIndexNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("AddToIndex on a missing index = %v, want ErrIndexNotFound", err)
	}

	base := filepath.Join(dir, "ghost", "ghost")
	for _, ro := range []bool{true, false} {
		if _, err := OpenPassageStore(base, ro); !errors.Is(err, ErrIndexNotFound) {
			t.Fatalf("OpenPassageStore(readOnly=%v) on a missing index = %v, want ErrIndexNotFound", ro, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("a missing index left %s behind (%v)", filepath.Join(dir, "ghost"), err)
	}
}
