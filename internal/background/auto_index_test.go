package background

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errTestBusy = errors.New("test: index busy")

func TestAutoIndexer_WatchAndFlush(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	tmpDir, err := os.MkdirTemp("", "auto_index_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()

	// Watch the temp directory as "test-index".
	if err := ai.Watch("test-index", tmpDir); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai.Start(ctx)

	// Create a file in the watched directory.
	testFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + processing with polling (handles FSEvents delay on macOS)
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		for _, task := range manager.List("") {
			if task.Type == TaskTypeAutoIndex {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Error("expected AutoIndex task to be submitted after file change")
	}
}

func TestAutoIndexer_WatchedIndexes(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()

	if len(ai.WatchedIndexes()) != 0 {
		t.Error("expected no watched indexes initially")
	}

	tmpDir, err := os.MkdirTemp("", "auto_index_test2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	if err := ai.Watch("my-index", tmpDir); err != nil {
		t.Fatal(err)
	}
	if len(ai.WatchedIndexes()) != 1 {
		t.Errorf("expected 1 watched index, got %d", len(ai.WatchedIndexes()))
	}
}

func TestAutoIndexer_StopIdempotent(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{})
	if err != nil {
		t.Fatal(err)
	}

	// Multiple stops should not panic.
	ai.Stop()
	ai.Stop()
}

// waitAutoIndexTasks waits until at least n AutoIndex tasks reached a terminal
// state and returns them (oldest first by creation).
func waitAutoIndexTasks(t *testing.T, m *Manager, n int) []Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var done []Task
		for _, task := range m.List("") {
			if task.Type == TaskTypeAutoIndex && (task.Status == StatusCompleted || task.Status == StatusFailed) {
				done = append(done, task)
			}
		}
		if len(done) >= n {
			return done
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d AutoIndex tasks finished", n)
	return nil
}

// flushPending must hand the exact changed file paths (sorted) to OnChange,
// with a context derived from the one Start was given — not just a count.
func TestAutoIndexer_OnChangeReceivesExactFiles(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()
	dir := t.TempDir()

	var mu sync.Mutex
	var gotName string
	var gotFiles []string
	var ctxLive bool
	called := make(chan struct{}, 1)
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce: time.Hour, // flushed manually below
		OnChange: func(ctx context.Context, indexName string, files []string) error {
			mu.Lock()
			gotName, gotFiles, ctxLive = indexName, files, ctx.Err() == nil
			mu.Unlock()
			called <- struct{}{}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()
	if err := ai.Watch("idx", dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai.Start(ctx)

	fB := filepath.Join(dir, "b.txt")
	fA := filepath.Join(dir, "a.txt")
	for _, f := range []string{fB, fA} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		ai.recordChange(f)
	}
	ai.flushPending()

	select {
	case <-called:
	case <-time.After(10 * time.Second):
		t.Fatal("OnChange was never called")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotName != "idx" {
		t.Errorf("OnChange index = %q, want idx", gotName)
	}
	if len(gotFiles) != 2 || gotFiles[0] != fA || gotFiles[1] != fB {
		t.Errorf("OnChange files = %v, want sorted [%s %s]", gotFiles, fA, fB)
	}
	if !ctxLive {
		t.Error("OnChange must receive a live context (a child of the one passed to Start)")
	}
	tasks := waitAutoIndexTasks(t, manager, 1)
	if tasks[0].Status != StatusCompleted {
		t.Errorf("task = %+v, want completed", tasks[0])
	}
}

// A lock-busy OnChange error keeps the files and retries them inside the same
// task (after the backoff), instead of failing the task or creating a new one.
func TestAutoIndexer_OnChangeBusyRetriesInSameTask(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()
	dir := t.TempDir()

	var calls atomic.Int32
	var mu sync.Mutex
	var batches [][]string
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce:    time.Hour,
		BusyBackoff: 10 * time.Millisecond,
		IsBusy:      func(err error) bool { return errors.Is(err, errTestBusy) },
		OnChange: func(ctx context.Context, indexName string, files []string) error {
			mu.Lock()
			batches = append(batches, append([]string(nil), files...))
			mu.Unlock()
			if calls.Add(1) == 1 {
				return fmt.Errorf("sync %q: %w", indexName, errTestBusy)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()
	if err := ai.Watch("idx", dir); err != nil {
		t.Fatal(err)
	}
	ai.Start(context.Background())

	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ai.recordChange(f)
	ai.flushPending()

	tasks := waitAutoIndexTasks(t, manager, 1)
	if len(tasks) != 1 || tasks[0].Status != StatusCompleted || !strings.Contains(tasks[0].Message, "synced") {
		t.Fatalf("busy batch must be retried to a synced state in one task: %+v", tasks)
	}
	if n := len(manager.List("")); n != 1 {
		t.Errorf("a busy retry created extra tasks: %d tasks, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 2 {
		t.Fatalf("OnChange calls = %d, want 2", len(batches))
	}
	for i, b := range batches {
		if len(b) != 1 || b[0] != f {
			t.Errorf("batch %d = %v, want [%s]", i, b, f)
		}
	}
}

// A real (non-lock) OnChange error must fail the task with that error.
func TestAutoIndexer_OnChangeErrorPropagates(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()
	dir := t.TempDir()

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce: time.Hour,
		OnChange: func(ctx context.Context, indexName string, files []string) error {
			return errors.New("disk on fire")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()
	if err := ai.Watch("idx", dir); err != nil {
		t.Fatal(err)
	}
	ai.Start(context.Background())

	// Record a change without writing a real file: an fsnotify event would
	// otherwise refill pending on its own and mask the re-queue behaviour.
	ai.recordChange(filepath.Join(dir, "a.txt"))
	ai.flushPending()

	tasks := waitAutoIndexTasks(t, manager, 1)
	if tasks[0].Status != StatusFailed || !strings.Contains(tasks[0].Error, "disk on fire") {
		t.Fatalf("task = %+v, want failed with the OnChange error", tasks[0])
	}
	// A failed (non-busy) batch is not re-queued.
	ai.mu.Lock()
	n := len(ai.pending["idx"])
	ai.mu.Unlock()
	if n != 0 {
		t.Errorf("a real error must not re-queue files (pending=%d)", n)
	}
}

// After the Start context is cancelled (server shutdown) a flush must not
// start new sync work.
func TestAutoIndexer_CancelledContextSkipsSync(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()
	dir := t.TempDir()

	var calls atomic.Int32
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce: time.Hour,
		OnChange: func(ctx context.Context, indexName string, files []string) error {
			calls.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()
	if err := ai.Watch("idx", dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ai.Start(ctx)
	cancel()

	ai.recordChange(filepath.Join(dir, "a.txt"))
	ai.flushPending()
	waitAutoIndexTasks(t, manager, 1)
	if calls.Load() != 0 {
		t.Errorf("OnChange ran %d times after cancellation, want 0", calls.Load())
	}
}
