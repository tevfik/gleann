package background

// Regression tests for the server-side watch review findings: path-boundary
// attribution, unwatch and stop semantics, ignore rules, the self-sync loop
// and the busy backoff.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A change in a sibling directory whose name merely starts with the watched
// directory's name must not be attributed to that index.
func TestRecordChange_PathBoundary(t *testing.T) {
	root := t.TempDir()
	docs, docs2, priv := filepath.Join(root, "docs"), filepath.Join(root, "docs2"), filepath.Join(root, "docs-private")
	mkdirs(t, docs, docs2, priv)
	ai := newTestIndexer(t, time.Hour)
	for name, dir := range map[string]string{"docs": docs, "docs2": docs2} {
		if err := ai.Watch(name, dir); err != nil {
			t.Fatal(err)
		}
	}

	ai.recordChange(filepath.Join(docs2, "secret.md"))
	ai.recordChange(filepath.Join(priv, "x.md"))
	ai.recordChange(filepath.Join(docs, "ok.md"))

	if got := pendingFor(ai, "docs"); !slices.Equal(got, []string{filepath.Join(docs, "ok.md")}) {
		t.Errorf("index docs got %v, want only its own file", got)
	}
	if got := pendingFor(ai, "docs2"); !slices.Equal(got, []string{filepath.Join(docs2, "secret.md")}) {
		t.Errorf("index docs2 got %v", got)
	}
}

// A symlinked watch root and symlinked parents compare by their real path,
// but the recorded path stays under the directory as it was given.
func TestRecordChange_SymlinkedRoot(t *testing.T) {
	root := t.TempDir()
	real, other := filepath.Join(root, "real"), filepath.Join(root, "real2")
	mkdirs(t, real, other)
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ai := newTestIndexer(t, time.Hour)
	if err := ai.Watch("l", link); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ai.watcher.WatchList(), canonicalDir(real)) {
		t.Fatalf("a symlinked root must be walked through its real path: %v", ai.watcher.WatchList())
	}

	ai.recordChange(filepath.Join(real, "x.md")) // event paths carry the real path
	ai.recordChange(filepath.Join(link, "y.md")) // and the link path also resolves
	ai.recordChange(filepath.Join(other, "z.md"))

	got := pendingFor(ai, "l")
	slices.Sort(got)
	want := []string{filepath.Join(link, "x.md"), filepath.Join(link, "y.md")}
	if !slices.Equal(got, want) {
		t.Errorf("pending = %v, want %v", got, want)
	}
}

// Nested watched directories: a file belongs to every index whose directory
// contains it (as a CLI build of each would see it), each index applying its
// own ignore rules.
func TestRecordChange_NestedDirsBelongToEveryContainingIndex(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "proj")
	inner := filepath.Join(outer, "sub")
	mkdirs(t, inner)
	writeFile(t, filepath.Join(outer, ".gitignore"), "secret.md\n")
	ai := newTestIndexer(t, time.Hour)
	if err := ai.Watch("outer", outer); err != nil {
		t.Fatal(err)
	}
	if err := ai.Watch("inner", inner); err != nil {
		t.Fatal(err)
	}

	inFile := filepath.Join(inner, "a.md")
	outFile := filepath.Join(outer, "b.md")
	secret := filepath.Join(inner, "secret.md")
	ai.recordChange(inFile)
	ai.recordChange(outFile)
	ai.recordChange(secret)

	gotOuter, gotInner := pendingFor(ai, "outer"), pendingFor(ai, "inner")
	slices.Sort(gotOuter)
	slices.Sort(gotInner)
	if want := []string{outFile, inFile}; !slices.Equal(gotOuter, want) {
		t.Errorf("outer = %v, want %v", gotOuter, want)
	}
	// secret.md is ignored by outer's .gitignore only; inner has no such rule.
	if want := []string{inFile, secret}; !slices.Equal(gotInner, want) {
		t.Errorf("inner = %v, want %v", gotInner, want)
	}
}

// The CLI's ignore rules (.gitignore, .gleannignore, walker defaults) decide
// what is watched and recorded; changing .gitignore reloads them.
func TestWatch_HonoursGitignoreAndWalkerDefaults(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, filepath.Join(root, "out"), filepath.Join(root, "target"), filepath.Join(root, "node_modules"), filepath.Join(root, "docs"), filepath.Join(root, "pycache_like"))
	writeFile(t, filepath.Join(root, ".gitignore"), "out/\n*.log\n")
	ai := newTestIndexer(t, time.Hour)
	if err := ai.Watch("idx", root); err != nil {
		t.Fatal(err)
	}

	list := ai.watcher.WatchList()
	for _, d := range []string{"out", "target", "node_modules"} {
		if slices.Contains(list, filepath.Join(canonicalDir(root), d)) {
			t.Errorf("ignored directory %s is watched", d)
		}
	}
	if !slices.Contains(list, filepath.Join(canonicalDir(root), "docs")) {
		t.Errorf("docs must be watched: %v", list)
	}

	ai.recordChange(filepath.Join(root, "out", "x.md"))
	ai.recordChange(filepath.Join(root, "a.log"))
	ai.recordChange(filepath.Join(root, "target", "y.md"))
	ai.recordChange(filepath.Join(root, ".hidden"))
	ai.recordChange(filepath.Join(root, "docs", "a.md"))
	if got := pendingFor(ai, "idx"); !slices.Equal(got, []string{filepath.Join(root, "docs", "a.md")}) {
		t.Fatalf("pending = %v, want only docs/a.md", got)
	}

	// A new rule takes effect once .gitignore changes.
	writeFile(t, filepath.Join(root, ".gitignore"), "out/\n*.log\ndocs/private.md\n")
	ai.recordChange(filepath.Join(root, ".gitignore"))
	ai.recordChange(filepath.Join(root, "docs", "private.md"))
	if got := pendingFor(ai, "idx"); !slices.Equal(got, []string{filepath.Join(root, "docs", "a.md")}) {
		t.Errorf("docs/private.md must be ignored after .gitignore changed: %v", got)
	}
}

// Unwatch drops the batch still waiting for the next tick: no sync runs after
// the watch is disabled, and the directory watches go away.
func TestUnwatch_DropsPendingBatch(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()
	dir := t.TempDir()
	var calls atomic.Int32
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce: time.Hour,
		OnChange: func(context.Context, string, []string) error { calls.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()
	if err := ai.Watch("idx", dir); err != nil {
		t.Fatal(err)
	}
	ai.Start(context.Background())
	ai.recordChange(filepath.Join(dir, "a.md"))

	ai.Unwatch("idx")
	ai.flushPending()

	time.Sleep(300 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("OnChange ran %d time(s) after Unwatch, want 0", n)
	}
	if n := len(manager.List("")); n != 0 {
		t.Errorf("%d task(s) submitted after Unwatch, want 0", n)
	}
	if got := pendingFor(ai, "idx"); len(got) != 0 {
		t.Errorf("pending survived Unwatch: %v", got)
	}
	if slices.Contains(ai.watcher.WatchList(), canonicalDir(dir)) {
		t.Error("the directory watch survived Unwatch")
	}
}

// blockingHook is an OnChange that signals when it started, notes the
// cancellation of its context and then keeps running until released.
type blockingHook struct {
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
	returned  chan struct{}
	calls     atomic.Int32
	once      sync.Once
	retOnce   sync.Once
}

func newBlockingHook() *blockingHook {
	return &blockingHook{
		entered:   make(chan struct{}),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
		returned:  make(chan struct{}),
	}
}

func (h *blockingHook) fn(ctx context.Context, _ string, _ []string) error {
	h.calls.Add(1)
	h.once.Do(func() { close(h.entered) })
	go func() {
		<-ctx.Done()
		select {
		case <-h.cancelled:
		default:
			close(h.cancelled)
		}
	}()
	select {
	case <-h.release:
	case <-ctx.Done():
		<-h.release // cancelled, but a real sync takes a moment to unwind
	}
	h.retOnce.Do(func() { close(h.returned) })
	return ctx.Err()
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func startedIndexer(t *testing.T, dir string, h *blockingHook) (*AutoIndexer, *Manager) {
	t.Helper()
	manager := NewManager(2)
	t.Cleanup(manager.Stop)
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{Debounce: time.Hour, OnChange: h.fn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ai.Stop)
	if err := ai.Watch("idx", dir); err != nil {
		t.Fatal(err)
	}
	ai.Start(context.Background())
	return ai, manager
}

// Stop cancels the running sync and Wait does not return until it has ended.
func TestStop_CancelsAndWaitsForRunningSync(t *testing.T) {
	dir := t.TempDir()
	h := newBlockingHook()
	ai, _ := startedIndexer(t, dir, h)
	ai.recordChange(filepath.Join(dir, "a.md"))
	ai.flushPending()
	waitFor(t, h.entered, "the sync to start")

	ai.Stop()
	waitFor(t, h.cancelled, "Stop to cancel the sync context")

	wctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	err := ai.Wait(wctx)
	cancel()
	if err == nil {
		t.Fatal("Wait returned while the sync was still running")
	}
	close(h.release)
	wctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ai.Wait(wctx); err != nil {
		t.Fatalf("Wait after the sync returned: %v", err)
	}
	select {
	case <-h.returned:
	default:
		t.Fatal("Wait returned before the sync had returned")
	}
}

// UnwatchAndWait (index delete) cancels a sync that is running and waits for it.
func TestUnwatchAndWait_CancelsRunningSync(t *testing.T) {
	dir := t.TempDir()
	h := newBlockingHook()
	ai, _ := startedIndexer(t, dir, h)
	ai.recordChange(filepath.Join(dir, "a.md"))
	ai.flushPending()
	waitFor(t, h.entered, "the sync to start")

	done := make(chan error, 1)
	go func() { done <- ai.UnwatchAndWait(context.Background(), "idx") }()
	waitFor(t, h.cancelled, "the sync context to be cancelled")
	select {
	case <-done:
		t.Fatal("UnwatchAndWait returned while the sync was still running")
	case <-time.After(200 * time.Millisecond):
	}
	close(h.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UnwatchAndWait never returned")
	}
	if len(ai.WatchedIndexes()) != 0 {
		t.Error("index still watched")
	}
}

// One task per index: while it is queued or running, further changes stay
// pending and do not create a second task; the next flush after it ended
// handles them.
func TestFlushPending_OneTaskPerIndex(t *testing.T) {
	dir := t.TempDir()
	h := newBlockingHook()
	ai, manager := startedIndexer(t, dir, h)
	first, second := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")
	ai.recordChange(first)
	ai.flushPending()
	waitFor(t, h.entered, "the sync to start")

	ai.recordChange(second)
	ai.flushPending()
	if n := len(manager.List("")); n != 1 {
		t.Fatalf("%d tasks while one sync runs, want 1", n)
	}
	close(h.release)
	waitFor(t, h.returned, "the sync to return")
	waitAutoIndexTasks(t, manager, 1)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.calls.Load() < 2 {
		ai.flushPending()
		time.Sleep(20 * time.Millisecond)
	}
	if h.calls.Load() != 2 {
		t.Fatalf("OnChange calls = %d, want 2 (the second batch after the first ended)", h.calls.Load())
	}
}

// The index directory (and other gleann data directories) inside a watched
// source tree never triggers a sync: a sync writes there, and that must not
// start the next sync.
func TestIndexDirInsideSource_DoesNotResync(t *testing.T) {
	src := t.TempDir()
	indexDir := filepath.Join(src, "indexes")
	dataDir := filepath.Join(src, "gleanndata")
	mkdirs(t, indexDir, dataDir)

	manager := NewManager(1)
	defer manager.Stop()
	var calls atomic.Int32
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		IndexDir:    indexDir,
		ExcludeDirs: []string{dataDir},
		Debounce:    30 * time.Millisecond,
		OnChange: func(ctx context.Context, name string, files []string) error {
			calls.Add(1)
			// What a sync writes: the db, the lock and a temp file renamed
			// over meta.json, in a fresh subdirectory, plus a data-dir file.
			writeFile(t, filepath.Join(indexDir, name, name+".db"), "x")
			writeFile(t, filepath.Join(indexDir, name+".lock"), "")
			writeFile(t, filepath.Join(indexDir, name, "meta.json"), "{}")
			writeFile(t, filepath.Join(dataDir, "state.json"), "{}")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()
	if err := ai.Watch("idx", src); err != nil {
		t.Fatal(err)
	}
	ai.Start(context.Background())

	writeFile(t, filepath.Join(src, "a.md"), "# a")
	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() < 1 {
		t.Fatal("the real source change never synced")
	}
	time.Sleep(700 * time.Millisecond) // ~20 debounce ticks
	if n := calls.Load(); n != 1 {
		t.Fatalf("OnChange ran %d times; the sync's own writes retriggered it", n)
	}
}

// A busy index is retried inside one task with exponential backoff (doubling,
// capped) and nothing is lost.
func TestBusyBackoff_OneTaskExponential(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()
	dir := t.TempDir()

	const base, maxB = 20 * time.Millisecond, 80 * time.Millisecond
	var mu sync.Mutex
	var stamps []time.Time
	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce:       time.Hour,
		BusyBackoff:    base,
		BusyBackoffMax: maxB,
		IsBusy:         func(err error) bool { return errors.Is(err, errTestBusy) },
		OnChange: func(ctx context.Context, name string, files []string) error {
			mu.Lock()
			defer mu.Unlock()
			stamps = append(stamps, time.Now())
			if len(stamps) <= 5 {
				return errTestBusy
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
	ai.recordChange(filepath.Join(dir, "a.md"))
	ai.flushPending()

	tasks := waitAutoIndexTasks(t, manager, 1)
	if len(tasks) != 1 || tasks[0].Status != StatusCompleted || !strings.Contains(tasks[0].Message, "synced") {
		t.Fatalf("tasks = %+v, want one synced task", tasks)
	}
	if n := len(manager.List("")); n != 1 {
		t.Errorf("%d tasks, want one per batch", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(stamps) != 6 {
		t.Fatalf("OnChange calls = %d, want 6", len(stamps))
	}
	// Gaps follow base, 2*base, then the cap; sleeps never end early.
	for i, want := range []time.Duration{base, 2 * base, maxB, maxB, maxB} {
		if gap := stamps[i+1].Sub(stamps[i]); gap < want {
			t.Errorf("retry %d came after %v, want at least %v", i+1, gap, want)
		}
	}
}

func TestNextBackoffAndDefaults(t *testing.T) {
	d := 5 * time.Second
	var seq []time.Duration
	for i := 0; i < 8; i++ {
		seq = append(seq, d)
		d = nextBackoff(d, 5*time.Minute)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	if !slices.Equal(seq, want) {
		t.Errorf("backoff sequence = %v, want %v", seq, want)
	}
	ai := newTestIndexer(t, time.Hour)
	if ai.config.BusyBackoff != 5*time.Second || ai.config.BusyBackoffMax != 5*time.Minute {
		t.Errorf("defaults = %v / %v, want 5s / 5m", ai.config.BusyBackoff, ai.config.BusyBackoffMax)
	}
}

// A directory that appears while watching is watched and the files already in
// it (which produce no event of their own) are recorded.
func TestAddNewDir_RecordsExistingFiles(t *testing.T) {
	root := t.TempDir()
	ai := newTestIndexer(t, time.Hour)
	if err := ai.Watch("idx", root); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "moved-in")
	writeFile(t, filepath.Join(sub, "deep", "a.md"), "x")
	ai.addNewDir(sub)
	if got := pendingFor(ai, "idx"); !slices.Equal(got, []string{filepath.Join(sub, "deep", "a.md")}) {
		t.Errorf("pending = %v", got)
	}
	if !slices.Contains(ai.watcher.WatchList(), filepath.Join(canonicalDir(root), "moved-in", "deep")) {
		t.Errorf("nested new directory not watched: %v", ai.watcher.WatchList())
	}
}
