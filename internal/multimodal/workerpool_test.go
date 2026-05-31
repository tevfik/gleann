// Package multimodal — worker pool behavioural tests for Bug #2.
package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// stubOllama returns an HTTPS-less Ollama stub that replies with a small
// constant chat response after a configurable delay. The active concurrent
// request count is exposed so tests can assert that the worker pool is
// actually parallelising.
func stubOllama(t *testing.T, delay time.Duration) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var concurrent atomic.Int32
	var maxSeen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := concurrent.Add(1)
		// Track peak concurrency without a lock.
		for {
			cur := maxSeen.Load()
			if n <= cur || maxSeen.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(delay)
		concurrent.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"stub-description"}}`))
	}))
	return srv, &concurrent, &maxSeen
}

// TestProcessDirectory_WorkerPoolParallelism verifies Bug #2: 8 files
// processed with 4 workers must complete in noticeably less than the
// sequential wall time, and peak concurrent in-flight requests must be > 1.
func TestProcessDirectory_WorkerPoolParallelism(t *testing.T) {
	// Isolate cache so we measure real Ollama calls.
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "4")

	const delay = 50 * time.Millisecond
	srv, _, maxSeen := stubOllama(t, delay)
	defer srv.Close()

	dir := t.TempDir()
	const n = 8
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "img"+strconv.Itoa(i)+".png")
		if err := os.WriteFile(p, []byte("img-bytes-"+strconv.Itoa(i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	proc := NewProcessor(srv.URL, "stub-model")
	start := time.Now()
	items, err := proc.ProcessDirectory(dir, nil, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ProcessDirectory err: %v", err)
	}
	if len(items) != n {
		t.Fatalf("got %d items, want %d", len(items), n)
	}
	// 8 sequential calls = 400ms; with 4 workers we expect ~100ms + overhead.
	// We allow a generous bound (250ms) to avoid CI flakes.
	if elapsed > 250*time.Millisecond {
		t.Errorf("worker pool did not parallelise: elapsed=%v (want <250ms)", elapsed)
	}
	if maxSeen.Load() < 2 {
		t.Errorf("peak concurrency = %d, want >= 2 (worker pool inactive?)", maxSeen.Load())
	}
}

// TestMultimodalWorkerCount_EnvOverride covers the env-knob.
func TestMultimodalWorkerCount_EnvOverride(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "7")
	if n := multimodalWorkerCount(); n != 7 {
		t.Errorf("workers=%d, want 7", n)
	}
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "0")
	if n := multimodalWorkerCount(); n != 4 {
		t.Errorf("invalid value should fall back to default 4, got %d", n)
	}
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "9999")
	if n := multimodalWorkerCount(); n != 4 {
		t.Errorf("out-of-range value should fall back to default 4, got %d", n)
	}
}

// TestProcessDirectory_OrderingPreserved verifies that even with parallel
// workers the output slice ordering matches discovery order so that
// downstream graph/embedding pipelines remain deterministic.
func TestProcessDirectory_OrderingPreserved(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "4")
	srv, _, _ := stubOllama(t, 5*time.Millisecond)
	defer srv.Close()

	dir := t.TempDir()
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)
	}
	proc := NewProcessor(srv.URL, "stub")
	items, err := proc.ProcessDirectory(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items", len(items))
	}
	// filepath.Walk returns lexical order: a,b,c,d.
	want := []string{"a.png", "b.png", "c.png", "d.png"}
	for i, it := range items {
		if filepath.Base(it.Source) != want[i] {
			t.Errorf("idx %d: got %s, want %s", i, filepath.Base(it.Source), want[i])
		}
	}
}
