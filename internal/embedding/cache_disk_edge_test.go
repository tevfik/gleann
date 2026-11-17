package embedding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveToDiskRefusesEmptyVector(t *testing.T) {
	dir := t.TempDir()
	c := NewCachedComputer(newMockEmbedder(4), CacheOptions{Dir: dir})
	err := c.saveToDisk("k", nil)
	if err == nil || !strings.Contains(err.Error(), "empty vector") {
		t.Fatalf("err = %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "k*")); len(m) != 0 {
		t.Errorf("files written for an empty vector: %v", m)
	}
}

func TestSaveToDiskFailsWhenDirIsGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	c := NewCachedComputer(newMockEmbedder(4), CacheOptions{Dir: dir})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := c.saveToDisk("k", []float32{1}); err == nil || !strings.Contains(err.Error(), "temp file") {
		t.Fatalf("err = %v", err)
	}
	// A failed write must not fail Compute: the vector is only recomputed next time.
	got, err := c.Compute(context.Background(), []string{"x"})
	if err != nil || len(got) != 1 || len(got[0]) != 4 {
		t.Fatalf("Compute = %v, %v", got, err)
	}
}

func TestSaveToDiskReplacesEntryAtomically(t *testing.T) {
	dir := t.TempDir()
	c := NewCachedComputer(newMockEmbedder(2), CacheOptions{Dir: dir})
	if err := c.saveToDisk("k", []float32{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.saveToDisk("k", []float32{3, 4}); err != nil {
		t.Fatal(err)
	}
	got, err := c.loadFromDisk("k")
	if err != nil || got[0] != 3 || got[1] != 4 {
		t.Fatalf("got %v, %v", got, err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(m) != 0 {
		t.Errorf("temp files left: %v", m)
	}
}

func TestLoadFromDiskRejectsBadEntries(t *testing.T) {
	dir := t.TempDir()
	c := NewCachedComputer(newMockEmbedder(4), CacheOptions{Dir: dir})
	_ = os.WriteFile(filepath.Join(dir, "empty.bin"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "odd.bin"), []byte{1, 2, 3}, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "short.bin"), make([]byte, 8), 0o644) // 2 floats, dim is 4
	c.noteDim(4)
	for name, want := range map[string]string{"empty": "empty cache entry", "odd": "corrupt", "short": "expected 16"} {
		if _, err := c.loadFromDisk(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want containing %q", name, err, want)
		}
	}
}

func TestRemoveStaleTempsOnlyRemovesOldOnes(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "a.tmp-1")
	fresh := filepath.Join(dir, "b.tmp-2")
	entry := filepath.Join(dir, "c.bin")
	for _, p := range []string{old, fresh, entry} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	// Opening the cache sweeps the directory.
	NewCachedComputer(newMockEmbedder(2), CacheOptions{Dir: dir})
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale temp file survived")
	}
	for _, p := range []string{fresh, entry} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must be kept: %v", filepath.Base(p), err)
		}
	}
}

type failingEmbedder struct{ mockEmbeddingComputer }

func (f *failingEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, context.DeadlineExceeded
}

func TestCachedComputeSingleFailureAndProgressForwarding(t *testing.T) {
	c := NewCachedComputer(&failingEmbedder{*newMockEmbedder(2)}, CacheOptions{MemoryOnly: true})
	if _, err := c.ComputeSingle(context.Background(), "x"); err == nil {
		t.Fatal("inner failure not propagated")
	}
	// An inner computer without progress support is left alone (must not panic).
	c.SetProgressCallback(func(int, int) {})

	// A real Computer receives the callback through the cache.
	inner := NewComputer(Options{Provider: ProviderMock, BatchSize: 1, Concurrency: 1})
	cc := NewCachedComputer(inner, CacheOptions{MemoryOnly: true})
	var calls int
	cc.SetProgressCallback(func(done, total int) { calls++ })
	if _, err := cc.Compute(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("progress calls = %d, want one per batch (2)", calls)
	}
}
