package embedding

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeAged(t testing.TB, p string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		ts := time.Now().Add(-age)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
}

// The sweep for crash leftovers runs at most once an hour per directory: a
// second open right after the first does not scan the directory again.
func TestStaleTempSweepRunsOncePerHour(t *testing.T) {
	dir := t.TempDir()
	NewCachedComputer(newMockEmbedder(2), CacheOptions{Dir: dir}) // first open: sweeps, stamps

	late := filepath.Join(dir, "late.tmp-1")
	writeAged(t, late, 2*time.Hour)
	NewCachedComputer(newMockEmbedder(2), CacheOptions{Dir: dir})
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("second open inside the hour must not rescan the directory: %v", err)
	}

	// An old stamp lets the next open sweep again; a fresh temp file
	// (a write in progress in another process) is still kept.
	fresh := filepath.Join(dir, "fresh.tmp-2")
	writeAged(t, fresh, 0)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, sweepStampName), past, past); err != nil {
		t.Fatal(err)
	}
	NewCachedComputer(newMockEmbedder(2), CacheOptions{Dir: dir})
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Error("stale temp file survived the sweep after the stamp expired")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh temp file must be kept: %v", err)
	}
}

// BenchmarkNewCachedComputerLargeDir measures opening the cache on a
// directory of 200K entries (the cost every CLI build, sync and search paid).
func BenchmarkNewCachedComputerLargeDir(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 200000; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.bin", i)), []byte("x"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	emb := newMockEmbedder(2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewCachedComputer(emb, CacheOptions{Dir: dir})
	}
}

// BenchmarkStaleSweepLargeDir is the cost of the sweep itself, once an hour.
func BenchmarkStaleSweepLargeDir(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 200000; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.bin", i)), []byte("x"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sweepStaleTemps(dir)
	}
}

// A stamp dated in the future (clock skew, a restored backup) must not stop
// the sweep until the clock catches up.
func TestStaleTempSweepIgnoresFutureStamp(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, sweepStampName)
	writeAged(t, stamp, 0)
	future := time.Now().AddDate(1, 0, 0)
	if err := os.Chtimes(stamp, future, future); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "old.tmp-1")
	writeAged(t, stale, 2*time.Hour)
	NewCachedComputer(newMockEmbedder(2), CacheOptions{Dir: dir})
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale temp file survived: a future-dated stamp blocked the sweep")
	}
	fi, err := os.Stat(stamp)
	if err != nil || fi.ModTime().After(time.Now().Add(time.Minute)) {
		t.Errorf("stamp was not renewed to the present: %v %v", fi, err)
	}
}
