package background

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func newTestIndexer(t *testing.T, debounce time.Duration) *AutoIndexer {
	t.Helper()
	m := NewManager(1)
	t.Cleanup(m.Stop)
	ai, err := NewAutoIndexer(m, AutoIndexConfig{Debounce: debounce})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ai.Stop)
	return ai
}

func TestWatchSkipsHiddenAndBuildDirs(t *testing.T) {
	root := t.TempDir() // created first so it is removed after the watcher closes
	ai := newTestIndexer(t, time.Hour)
	for _, d := range []string{"src/pkg", ".git/objects", "node_modules/x", "vendor", "dist", "build"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ai.Watch("idx", root); err != nil {
		t.Fatal(err)
	}
	got := ai.watcher.WatchList()
	slices.Sort(got)
	// The watcher registers canonical paths (symlinks and 8.3 names resolved),
	// so expectations are canonical too (/private/var on macOS, long names on
	// Windows).
	croot := canonicalDir(root)
	want := []string{croot, filepath.Join(croot, "src"), filepath.Join(croot, "src", "pkg")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("watched %q, want %q", got, want)
	}
}

func TestWatchMissingDirIsNotAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	ai := newTestIndexer(t, time.Hour)
	if err := ai.Watch("idx", missing); err != nil {
		t.Fatalf("Watch on a missing dir: %v", err)
	}
	if len(ai.watcher.WatchList()) != 0 {
		t.Fatal("nothing must be watched")
	}
}

func TestUnwatchForgetsIndex(t *testing.T) {
	dir := t.TempDir()
	ai := newTestIndexer(t, time.Hour)
	if err := ai.Watch("a", dir); err != nil {
		t.Fatal(err)
	}
	if err := ai.Watch("b", dir); err != nil {
		t.Fatal(err)
	}
	ai.Unwatch("a")
	if got := ai.WatchedIndexes(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("WatchedIndexes after Unwatch = %v", got)
	}
	// Changes are no longer attributed to the unwatched index.
	ai.recordChange(filepath.Join(dir, "f.txt"))
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if _, ok := ai.pending["a"]; ok {
		t.Fatal("change recorded for an unwatched index")
	}
	if len(ai.pending["b"]) != 1 {
		t.Fatalf("pending = %v", ai.pending)
	}
}

func pendingFor(ai *AutoIndexer, name string) []string {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	var out []string
	for p := range ai.pending[name] {
		out = append(out, p)
	}
	return out
}

func TestEventLoopWatchesNewDirectoriesAndLogsErrors(t *testing.T) {
	root := t.TempDir()
	ai := newTestIndexer(t, time.Hour) // no flush during the test
	if err := ai.Watch("idx", root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai.Start(ctx)

	// A watcher error is logged and the loop keeps running.
	ai.watcher.Errors <- errors.New("injected watcher error")

	sub := filepath.Join(root, "newdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	csub := filepath.Join(canonicalDir(root), "newdir")
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(ai.watcher.WatchList(), csub) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Contains(ai.watcher.WatchList(), csub) {
		t.Fatalf("new directory %s was not added to the watch list %v", csub, ai.watcher.WatchList())
	}
	// Files in the new directory are now picked up.
	file := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for !slices.Contains(pendingFor(ai, "idx"), file) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Contains(pendingFor(ai, "idx"), file) {
		t.Fatalf("change in the new directory not recorded: %v", pendingFor(ai, "idx"))
	}
}

// runLoop runs loop in a goroutine and fails the test if it does not return.
func runLoop(t *testing.T, loop func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		loop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not return")
	}
}

func TestLoopsReturnOnStopAndClosedChannels(t *testing.T) {
	t.Run("stop channel", func(t *testing.T) {
		ai := &AutoIndexer{
			watcher: &fsnotify.Watcher{Events: make(chan fsnotify.Event), Errors: make(chan error)},
			config:  AutoIndexConfig{Debounce: time.Hour},
			stopCh:  make(chan struct{}),
		}
		close(ai.stopCh)
		runLoop(t, func() { ai.eventLoop(context.Background()) })
		runLoop(t, func() { ai.debounceLoop(context.Background()) })
	})
	t.Run("events closed", func(t *testing.T) {
		events := make(chan fsnotify.Event)
		close(events)
		ai := &AutoIndexer{
			watcher: &fsnotify.Watcher{Events: events, Errors: make(chan error)},
			stopCh:  make(chan struct{}),
		}
		runLoop(t, func() { ai.eventLoop(context.Background()) })
	})
	t.Run("errors closed", func(t *testing.T) {
		errs := make(chan error)
		close(errs)
		ai := &AutoIndexer{
			watcher: &fsnotify.Watcher{Events: make(chan fsnotify.Event), Errors: errs},
			stopCh:  make(chan struct{}),
		}
		runLoop(t, func() { ai.eventLoop(context.Background()) })
	})
}

func TestNewManagerDefaultsWorkers(t *testing.T) {
	m := NewManager(0)
	defer m.Stop()
	if m.workers != 2 {
		t.Fatalf("workers = %d, want 2", m.workers)
	}
}

// When the queue is full, Submit runs the task on its own goroutine instead
// of blocking the caller.
func TestSubmitRunsInlineWhenQueueIsFull(t *testing.T) {
	m := NewManager(1)
	defer m.Stop()

	release := make(chan struct{})
	started := make(chan struct{})
	m.Submit(TaskTypeCustom, func(func(float64, string)) error {
		close(started)
		<-release
		return nil
	})
	<-started // the only worker is now busy

	var ran sync.WaitGroup
	const n = 101 // queue capacity is 100
	ran.Add(1)
	var ids []string
	for i := 0; i < n-1; i++ {
		ids = append(ids, m.Submit(TaskTypeCustom, func(func(float64, string)) error { return nil }))
	}
	overflow := m.Submit(TaskTypeCustom, func(func(float64, string)) error {
		ran.Done()
		return nil
	})
	// The overflow task must run although the worker is still blocked.
	waitDone := make(chan struct{})
	go func() { ran.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("overflow task did not run while the queue was full")
	}
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for _, id := range append(ids, overflow) {
		for time.Now().Before(deadline) {
			if task := m.Get(id); task != nil && task.Status == StatusCompleted {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if task := m.Get(id); task == nil || task.Status != StatusCompleted {
			t.Fatalf("task %s not completed: %+v", id, task)
		}
	}
}
