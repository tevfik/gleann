package embedding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A memory-only cache creates and writes no file, still serves repeated texts
// from memory, and ClearCache only empties L1.
func TestCachedComputer_MemoryOnlyWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	inner := newMockEmbedder(4)
	c := NewCachedComputer(inner, CacheOptions{Dir: dir, MemoryOnly: true})

	texts := []string{"alpha", "beta"}
	if _, err := c.Compute(context.Background(), texts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cache dir created (err=%v)", err)
	}
	if _, err := c.Compute(context.Background(), texts); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Errorf("second call was not served from memory: %d inner calls", inner.calls)
	}
	if err := c.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Compute(context.Background(), texts); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Errorf("ClearCache did not empty L1: %d inner calls", inner.calls)
	}
}

// A memory-only cache does not read entries a disk cache left in the directory,
// and ClearCache does not remove that directory.
func TestCachedComputer_MemoryOnlyIgnoresDiskEntries(t *testing.T) {
	dir := t.TempDir()
	inner := newMockEmbedder(4)
	disk := NewCachedComputer(inner, CacheOptions{Dir: dir})
	if _, err := disk.Compute(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	before := inner.calls
	mem := NewCachedComputer(inner, CacheOptions{Dir: dir, MemoryOnly: true})
	if _, err := mem.Compute(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if inner.calls != before+1 {
		t.Errorf("memory-only cache read the disk entry")
	}
	if err := mem.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("ClearCache of a memory-only cache removed the directory: %v", err)
	}
}
