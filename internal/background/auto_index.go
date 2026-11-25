package background

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tevfik/gleann/pkg/walker"
)

// AutoIndexConfig configures the background auto-indexer.
type AutoIndexConfig struct {
	IndexDir string            // Root directory for indexes
	Indexes  map[string]string // name → docs directory
	Debounce time.Duration     // Debounce interval (default: 5s)
	// ExcludeDirs are gleann data directories (config, memory, conversations,
	// caches, ...) in addition to IndexDir. Whatever of them lies inside a
	// watched directory is never watched and its events are dropped, so the
	// files gleann itself writes cannot trigger a sync of their own source.
	ExcludeDirs []string
	// OnChange performs the actual index sync for one batch of changed files
	// (the server wires it to indexing.SyncChanged). When nil, the task keeps
	// the legacy status-only behaviour: it reports progress without syncing.
	OnChange func(ctx context.Context, indexName string, files []string) error
	// IsBusy reports whether an OnChange error only means "the index is locked
	// right now" (another process or an in-process build holds it). The batch
	// is then kept and retried with exponential backoff inside the same task;
	// any other error fails the task. nil: no error is a busy error.
	IsBusy func(err error) bool
	// BusyBackoff is the first retry delay after a busy result (default 5s);
	// it doubles after every further busy result up to BusyBackoffMax
	// (default 5m) and starts over with the next batch.
	BusyBackoff    time.Duration
	BusyBackoffMax time.Duration
}

// AutoIndexer watches indexed directories and triggers re-index
// via the background task manager when files change.
//
// Attribution: a changed file belongs to EVERY watched index whose directory
// really contains it (compared on a path boundary after Clean and
// EvalSymlinks), so nested watched directories both see a file inside the
// inner one. That is what a CLI build or sync of each index would do: the
// outer index walks into the inner directory as well. Each index applies its
// own ignore rules (.gitignore, .gleannignore, the walker defaults) rooted at
// its own directory.
type AutoIndexer struct {
	manager *Manager
	config  AutoIndexConfig
	watcher *fsnotify.Watcher
	mu      sync.Mutex
	ctx     context.Context            // lifetime given to Start; handed to OnChange
	started bool                       // Start ran (guards duplicate loops)
	pending map[string]map[string]bool // index name → set of changed files
	entries map[string]*watchEntry     // index name → what is watched for it
	flights map[string]*flight         // index name → its one queued/running sync task
	wg      sync.WaitGroup             // started sync task bodies (Wait)
	stopCh  chan struct{}
	stopped bool
}

// watchEntry is the directory watched for one index.
type watchEntry struct {
	absDir  string          // as given, made absolute: the path syncs are told about
	realDir string          // absDir with symlinks resolved: the path events carry
	exclude []string        // canonical dirs strictly inside realDir that are ignored
	ignore  *ignoreSet      // .gitignore / .gleannignore / walker defaults of realDir
	dirs    map[string]bool // directories registered with fsnotify for this index
}

// flight is the single sync task of one index. While it exists no second task
// is created for the index: changes that arrive meanwhile stay pending and the
// flight (when busy-retrying) or the next tick after it ends picks them up.
type flight struct {
	ctx      context.Context
	cancel   context.CancelFunc
	quit     chan struct{} // closed on Unwatch: finish the current attempt, do not retry
	quitOnce sync.Once
	done     chan struct{} // closed when the task body ended
	started  bool          // the task body began (guarded by AutoIndexer.mu)
}

// ignoreSet evaluates the CLI's ignore rules (the same walker.Matcher the
// index build and sync use) for one watched root. It is safe for concurrent
// use; reset makes the next call reload the ignore files.
type ignoreSet struct {
	mu   sync.Mutex
	root string
	m    *walker.Matcher
}

// ignored reports whether the path rel (relative to the root) is ignored. A
// walk prunes an ignored directory, so every ancestor directory is tested too:
// "out/x.md" is ignored by a "out/" rule although the file itself matches no
// pattern.
func (s *ignoreSet) ignored(rel string, isDir bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = walker.NewMatcher(s.root, false, nil)
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if s.m.ShouldIgnore(strings.Join(parts[:i], "/"), true) {
			return true
		}
	}
	return s.m.ShouldIgnore(rel, isDir)
}

func (s *ignoreSet) reset() {
	s.mu.Lock()
	s.m = nil
	s.mu.Unlock()
}

// canonicalDir returns an absolute, cleaned path with symlinks resolved.
func canonicalDir(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// canonicalLeaf is canonicalDir for an event path: only the parent directory
// is resolved, never the last element. A symlinked file inside a watched
// directory stays inside it (a build reads it through its link name), and a
// file that no longer exists still resolves.
func canonicalLeaf(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(r, filepath.Base(abs))
	}
	return abs
}

// within reports whether p is root or lies inside it, comparing on a path
// boundary ("/r/docs2" is not inside "/r/docs"), and returns p relative to
// root ("." for root itself).
func within(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// NewAutoIndexer creates an auto-indexer that submits re-index tasks
// to the background manager when watched files change.
func NewAutoIndexer(manager *Manager, config AutoIndexConfig) (*AutoIndexer, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}
	if config.Debounce <= 0 {
		config.Debounce = 5 * time.Second
	}
	if config.BusyBackoff <= 0 {
		config.BusyBackoff = 5 * time.Second
	}
	if config.BusyBackoffMax <= 0 {
		config.BusyBackoffMax = 5 * time.Minute
	}
	if config.BusyBackoffMax < config.BusyBackoff {
		config.BusyBackoffMax = config.BusyBackoff
	}
	if config.Indexes == nil {
		config.Indexes = make(map[string]string)
	}
	a := &AutoIndexer{
		manager: manager,
		config:  config,
		watcher: w,
		pending: make(map[string]map[string]bool),
		entries: make(map[string]*watchEntry),
		flights: make(map[string]*flight),
		stopCh:  make(chan struct{}),
	}
	for name, dir := range config.Indexes {
		a.entries[name] = a.newEntry(dir)
	}
	return a, nil
}

// newEntry resolves dir and the data directories that must be ignored.
func (a *AutoIndexer) newEntry(dir string) *watchEntry {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	abs = filepath.Clean(abs)
	real := canonicalDir(abs)
	e := &watchEntry{
		absDir:  abs,
		realDir: real,
		ignore:  &ignoreSet{root: real},
		dirs:    make(map[string]bool),
	}
	excl := append([]string{a.config.IndexDir}, a.config.ExcludeDirs...)
	for _, x := range excl {
		if x == "" {
			continue
		}
		cx := canonicalDir(x)
		// Only a directory strictly inside the watched one is carved out: a
		// watched directory that itself lives under a data directory is the
		// user's explicit choice.
		if rel, ok := within(real, cx); ok && rel != "." {
			e.exclude = append(e.exclude, cx)
		}
	}
	return e
}

// excluded reports whether canon is one of the entry's data directories or
// inside one.
func (e *watchEntry) excluded(canon string) bool {
	for _, x := range e.exclude {
		if _, ok := within(x, canon); ok {
			return true
		}
	}
	return false
}

// Watch adds a directory to the auto-index watcher for the given index name.
func (a *AutoIndexer) Watch(indexName, docsDir string) error {
	e := a.newEntry(docsDir)
	a.mu.Lock()
	if a.entries == nil {
		a.entries = make(map[string]*watchEntry)
	}
	if a.config.Indexes == nil {
		a.config.Indexes = make(map[string]string)
	}
	if old := a.entries[indexName]; old != nil {
		// Re-watching (possibly another directory): release the old watches.
		delete(a.entries, indexName)
		a.removeUnusedWatchesLocked(old)
	}
	a.config.Indexes[indexName] = docsDir
	a.entries[indexName] = e
	a.mu.Unlock()

	return a.addDirRecursive(e, e.realDir, false)
}

// Unwatch removes a directory from the auto-index watcher for the given index
// name. It drops the batch still waiting for the next tick, stops retrying a
// busy batch and removes the directory watches no other index uses, so no
// sync starts after it returns. A sync attempt that is already running is not
// interrupted (use UnwatchAndWait to cancel it).
func (a *AutoIndexer) Unwatch(indexName string) {
	a.mu.Lock()
	a.unwatchLocked(indexName)
	a.mu.Unlock()
}

// UnwatchAndWait is Unwatch that also cancels a sync of the index already in
// progress and waits (bounded by ctx) until it has returned. Deleting an index
// calls it before taking the index lock.
func (a *AutoIndexer) UnwatchAndWait(ctx context.Context, indexName string) error {
	a.mu.Lock()
	f := a.unwatchLocked(indexName)
	if f != nil {
		f.cancel()
	}
	a.mu.Unlock()
	if f == nil {
		return nil
	}
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the auto-sync of %q to stop: %w", indexName, ctx.Err())
	}
}

// unwatchLocked removes every trace of indexName and returns its running
// flight (nil when none has started).
func (a *AutoIndexer) unwatchLocked(indexName string) *flight {
	delete(a.config.Indexes, indexName)
	delete(a.pending, indexName)
	if e := a.entries[indexName]; e != nil {
		delete(a.entries, indexName)
		a.removeUnusedWatchesLocked(e)
	}
	f := a.flights[indexName]
	if f == nil {
		return nil
	}
	f.quitOnce.Do(func() { close(f.quit) })
	if !f.started {
		// Still queued: its body will see the cancelled context and do
		// nothing; a later re-enable must not wait for it.
		f.cancel()
		delete(a.flights, indexName)
		return nil
	}
	return f
}

// removeUnusedWatchesLocked unregisters the directories of e that no other
// watched index still needs.
func (a *AutoIndexer) removeUnusedWatchesLocked(e *watchEntry) {
	for d := range e.dirs {
		used := false
		for _, other := range a.entries {
			if other.dirs[d] {
				used = true
				break
			}
		}
		if !used && a.watcher != nil {
			_ = a.watcher.Remove(d)
		}
	}
}

// addDirRecursive registers root and its subdirectories with fsnotify for
// entry e, skipping what the CLI build would not read (.gitignore,
// .gleannignore, walker defaults) and the gleann data directories. With
// record set (a directory that appeared while watching) the files already
// inside are recorded as changed, since they produce no event of their own.
func (a *AutoIndexer) addDirRecursive(e *watchEntry, root string, record bool) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		rel, ok := within(e.realDir, path)
		if !ok {
			return nil
		}
		if !info.IsDir() {
			if record {
				a.recordChange(path)
			}
			return nil
		}
		if rel != "." && (e.excluded(path) || e.ignore.ignored(rel, true)) {
			return filepath.SkipDir
		}
		if err := a.watcher.Add(path); err != nil {
			return err
		}
		a.mu.Lock()
		if e.dirs != nil {
			e.dirs[path] = true
		}
		a.mu.Unlock()
		return nil
	})
}

// Start begins watching for file changes and submitting auto-index tasks.
// ctx bounds the sync work handed to OnChange (the server passes its lifetime
// context, so Stop cancels a running sync). Calling Start twice is a no-op.
func (a *AutoIndexer) Start(ctx context.Context) {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.ctx = ctx
	a.mu.Unlock()
	go a.eventLoop(ctx)
	go a.debounceLoop(ctx)
}

// eventLoop reads fsnotify events and accumulates changed files.
func (a *AutoIndexer) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case event, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				// Dynamically add new directories.
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						a.addNewDir(event.Name)
						continue
					}
				}
				a.recordChange(event.Name)
			}
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("auto-index watcher error: %v", err)
		}
	}
}

// addNewDir watches a directory that appeared (and records the files already
// in it) for every index whose directory contains it.
func (a *AutoIndexer) addNewDir(path string) {
	canon := canonicalLeaf(path)
	a.mu.Lock()
	var targets []*watchEntry
	for _, e := range a.entries {
		if rel, ok := within(e.realDir, canon); ok && rel != "." && !e.excluded(canon) && !e.ignore.ignored(rel, true) {
			targets = append(targets, e)
		}
	}
	a.mu.Unlock()
	for _, e := range targets {
		if err := a.addDirRecursive(e, canon, true); err != nil {
			log.Printf("auto-index: watching %s: %v", canon, err)
		}
	}
}

// recordChange maps a changed file path back to its index names: every
// watched index whose directory contains it (see AutoIndexer), unless that
// index ignores it. The recorded path is expressed under the directory as it
// was given to Watch, which is the root the sync resolves paths against.
func (a *AutoIndexer) recordChange(path string) {
	canon := canonicalLeaf(path)
	isDir := false
	if info, err := os.Lstat(canon); err == nil {
		isDir = info.IsDir()
	}
	base := filepath.Base(canon)

	a.mu.Lock()
	defer a.mu.Unlock()

	for name, e := range a.entries {
		rel, ok := within(e.realDir, canon)
		if !ok || rel == "." {
			continue
		}
		if base == ".gitignore" || base == ".gleannignore" {
			// The rules changed: reload them. The file itself is not content.
			e.ignore.reset()
			continue
		}
		if e.excluded(canon) || e.ignore.ignored(rel, isDir) {
			continue
		}
		if a.pending == nil {
			a.pending = make(map[string]map[string]bool)
		}
		if a.pending[name] == nil {
			a.pending[name] = make(map[string]bool)
		}
		a.pending[name][filepath.Join(e.absDir, rel)] = true
	}
}

// debounceLoop periodically checks for pending changes and submits tasks.
func (a *AutoIndexer) debounceLoop(ctx context.Context) {
	ticker := time.NewTicker(a.config.Debounce)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.flushPending()
		}
	}
}

// flushPending submits one auto-index task for every index that has pending
// changes and no task of its own yet. An index with a queued or running task
// keeps its pending set: that task (or the next tick after it ended) takes it.
func (a *AutoIndexer) flushPending() {
	a.mu.Lock()
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	type launch struct {
		name string
		f    *flight
	}
	var launches []launch
	for name, files := range a.pending {
		if len(files) == 0 || a.flights[name] != nil {
			continue
		}
		fctx, cancel := context.WithCancel(base)
		f := &flight{ctx: fctx, cancel: cancel, quit: make(chan struct{}), done: make(chan struct{})}
		if a.flights == nil {
			a.flights = make(map[string]*flight)
		}
		a.flights[name] = f
		launches = append(launches, launch{name, f})
	}
	a.mu.Unlock()

	for _, l := range launches {
		name, f := l.name, l.f
		a.manager.Submit(TaskTypeAutoIndex, func(progress func(float64, string)) error {
			return a.runFlight(name, f, progress)
		})
	}
}

// begin marks the flight started; false means it was cancelled while queued
// (Unwatch, delete, Stop), in which case nothing may run.
func (a *AutoIndexer) begin(name string, f *flight) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if f.ctx.Err() != nil || a.stopped {
		if a.flights[name] == f {
			delete(a.flights, name)
		}
		f.cancel()
		close(f.done)
		return false
	}
	f.started = true
	a.wg.Add(1)
	return true
}

func (a *AutoIndexer) end(name string, f *flight) {
	a.mu.Lock()
	if a.flights[name] == f {
		delete(a.flights, name)
	}
	a.mu.Unlock()
	f.cancel()
	close(f.done)
	a.wg.Done()
}

// takePending removes and returns the sorted pending paths of an index.
func (a *AutoIndexer) takePending(name string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	files := a.pending[name]
	delete(a.pending, name)
	paths := make([]string, 0, len(files))
	for f := range files {
		paths = append(paths, f)
	}
	sort.Strings(paths)
	return paths
}

func (a *AutoIndexer) isBusy(err error) bool {
	return a.config.IsBusy != nil && a.config.IsBusy(err)
}

// nextBackoff doubles cur, capped at max.
func nextBackoff(cur, max time.Duration) time.Duration {
	if cur *= 2; cur > max || cur <= 0 {
		return max
	}
	return cur
}

// runFlight is the body of an index's single auto-index task. It hands the
// exact changed paths (sorted) to OnChange. A busy result keeps the batch,
// waits with exponential backoff (BusyBackoff doubling up to BusyBackoffMax)
// and tries again inside the same task, updating its message; the delay
// starts over with the next batch. Cancellation (Unwatch, delete, Stop) ends
// the task without failing it.
func (a *AutoIndexer) runFlight(name string, f *flight, progress func(float64, string)) error {
	if !a.begin(name, f) {
		progress(1.0, fmt.Sprintf("Auto-index %q skipped (stopped)", name))
		return nil
	}
	defer a.end(name, f)

	base, maxBackoff := a.config.BusyBackoff, a.config.BusyBackoffMax
	if base <= 0 {
		base = 5 * time.Second
	}
	if maxBackoff < base {
		maxBackoff = 5 * time.Minute
		if maxBackoff < base {
			maxBackoff = base
		}
	}
	backoff := base
	for {
		files := a.takePending(name)
		if len(files) == 0 {
			progress(1.0, fmt.Sprintf("Auto-index %q: nothing to sync", name))
			return nil
		}
		progress(0.1, fmt.Sprintf("Auto-reindex %q: %d file(s) changed", name, len(files)))
		if a.config.OnChange == nil {
			// Legacy status-only behaviour: no sync callback is wired,
			// so the task only reports that changes were seen.
			progress(0.5, fmt.Sprintf("Queued rebuild for %q", name))
			progress(1.0, fmt.Sprintf("Auto-index triggered for %q (%d files)", name, len(files)))
			return nil
		}
		if f.ctx.Err() != nil {
			progress(1.0, fmt.Sprintf("Auto-index %q skipped (shutting down)", name))
			return nil
		}
		progress(0.5, fmt.Sprintf("Syncing %q (%d file(s))", name, len(files)))
		err := a.config.OnChange(f.ctx, name, files)
		switch {
		case err == nil:
			progress(1.0, fmt.Sprintf("Auto-index synced %q (%d file(s))", name, len(files)))
			return nil
		case f.ctx.Err() != nil:
			progress(1.0, fmt.Sprintf("Auto-index %q cancelled", name))
			return nil
		case a.isBusy(err):
			if !a.requeue(name, files) {
				progress(1.0, fmt.Sprintf("Auto-index %q dropped (no longer watched)", name))
				return nil
			}
			progress(0.5, fmt.Sprintf("Auto-index %q busy, will retry in %s (%d file(s))", name, backoff, len(files)))
			if !a.waitRetry(f, backoff) {
				progress(1.0, fmt.Sprintf("Auto-index %q busy, retry abandoned (stopped)", name))
				return nil
			}
			backoff = nextBackoff(backoff, maxBackoff)
		default:
			return fmt.Errorf("auto-sync index %q: %w", name, err)
		}
	}
}

// waitRetry sleeps d; false means the flight was told to stop meanwhile.
func (a *AutoIndexer) waitRetry(f *flight, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-f.ctx.Done():
	case <-f.quit:
	case <-a.stopCh:
	}
	return false
}

// requeue puts a busy batch back into pending, merging with changes that
// arrived since it was taken so nothing is lost or duplicated. It reports
// false (and drops the batch) when the index is no longer watched.
func (a *AutoIndexer) requeue(indexName string, files []string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.entries[indexName]; !ok {
		return false
	}
	if a.pending[indexName] == nil {
		a.pending[indexName] = make(map[string]bool)
	}
	for _, f := range files {
		a.pending[indexName][f] = true
	}
	return true
}

// WatchedDir returns the directory watched for indexName ("" when unwatched).
func (a *AutoIndexer) WatchedDir(indexName string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config.Indexes[indexName]
}

// WatchedIndexes returns the list of index names being watched.
func (a *AutoIndexer) WatchedIndexes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, 0, len(a.config.Indexes))
	for name := range a.config.Indexes {
		names = append(names, name)
	}
	return names
}

// Stop shuts down the auto-indexer: no new task starts, the directory watches
// close and every queued or running sync is cancelled. It does not wait for a
// running sync to return; Wait does.
func (a *AutoIndexer) Stop() {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	for _, f := range a.flights {
		f.cancel()
	}
	a.mu.Unlock()
	close(a.stopCh)
	a.watcher.Close()
}

// Wait blocks until every sync task that started has returned, or ctx ends.
// Call it after Stop (which cancels them) and before closing what a sync
// writes to, so shutdown never races a sync that is still writing.
func (a *AutoIndexer) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for auto-index syncs: %w", ctx.Err())
	}
}
