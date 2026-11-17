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

	v1 := filepath.Join(dir, "old", "old")
	if err := os.MkdirAll(filepath.Dir(v1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v1+".meta.json", []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectStoreFormat(v1); got != StoreV1 {
		t.Fatalf("index with meta.json and no store_format = %v, want StoreV1", got)
	}
}

// A missing index is not a format-1 index: nothing is created for it, and the
// callers get a typed error.
func TestMissingIndexIsNotTreatedAsFormat1(t *testing.T) {
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
	// The writable open must not have created the index directory.
	if _, err := os.Stat(filepath.Join(dir, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("a missing index left %s behind (%v)", filepath.Join(dir, "ghost"), err)
	}
}
