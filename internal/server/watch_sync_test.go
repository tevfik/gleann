package server

// End-to-end tests of the server-side auto-sync watch: enabling watch on a
// directory must actually sync changed files into the index (not just report
// progress), make them searchable, retry while the index lock is busy, and
// leave no held lock behind after Stop.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// isolateWatchEnv pins HOME, XDG_* and the GLEANN_* directory overrides to
// the test's own temp dirs so nothing touches the real user state.
func isolateWatchEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	for _, k := range []string{
		"GLEANN_CONFIG_DIR", "GLEANN_MEMORY_DIR", "GLEANN_MULTIMODAL_CACHE_DIR",
		"GLEANN_CONVERSATIONS_DIR", "GLEANN_INDEX_DIR", "GLEANN_PACKS_DIR",
		"GLEANN_AUTO_INDEX_DIRS",
	} {
		t.Setenv(k, "")
	}
}

// watchTestServer builds a lexical-only index "watched" over docs (no Ollama
// needed) and returns a server with watch enabled on that directory.
func watchTestServer(t *testing.T) (*Server, http.Handler, gleann.Config, string) {
	t.Helper()
	return watchTestServerHook(t, nil)
}

// watchTestServerHook is watchTestServer with the watcher's sync callback
// replaced by hook (before the watch is enabled). hook may be nil.
func watchTestServerHook(t *testing.T, hook func(s *Server, ctx context.Context, name string, files []string) error) (*Server, http.Handler, gleann.Config, string) {
	t.Helper()
	isolateWatchEnv(t)
	indexDir := t.TempDir()
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "first.md"), []byte("# First\n\nThe initial document about pandas.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = indexDir
	if _, err := indexing.Build(context.Background(), indexing.BuildOptions{
		Options:     indexing.Options{Config: cfg, NoPlugins: true, NoTracker: true, Stdout: io.Discard, Stderr: io.Discard},
		Name:        "watched",
		DocsDir:     docs,
		InstantOnly: true,
	}); err != nil {
		t.Fatalf("building the watch test index: %v", err)
	}

	s := NewServer(cfg, ":0", "test")
	if hook != nil {
		s.autoSyncHook = func(ctx context.Context, name string, files []string) error {
			return hook(s, ctx, name, files)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	h := s.buildMux()
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)
	if w := postJSON(h, "/api/indexes/watched/watch", `{"enable":true,"dir":`+jsonStr(docs)+`}`); w.Code != http.StatusOK {
		t.Fatalf("enable watch: %d %s", w.Code, w.Body.String())
	}
	return s, h, cfg, docs
}

func watchSearch(t *testing.T, h http.Handler, query string) []gleann.SearchResult {
	t.Helper()
	w := postJSON(h, "/api/indexes/watched/search", `{"query":`+jsonStr(query)+`,"top_k":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("search %q: %d %s", query, w.Code, w.Body.String())
	}
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("search response: %v", err)
	}
	return resp.Results
}

// A file written into the watched directory must be synced into the index by
// the watcher itself and become searchable via POST /api/indexes/{name}/search.
func TestWatch_AutoSyncMakesNewFileSearchable(t *testing.T) {
	_, h, _, docs := watchTestServer(t)

	// Precondition: the initial document is searchable.
	if len(watchSearch(t, h, "pandas")) == 0 {
		t.Fatal("precondition: the initial document is not searchable")
	}

	if err := os.WriteFile(filepath.Join(docs, "new.md"), []byte("# New\n\nA wild quokkaflux appears in this document.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range watchSearch(t, h, "quokkaflux") {
			if strings.Contains(r.Text, "quokkaflux") {
				return // synced and searchable
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the watcher never synced the new file into the index (search finds no quokkaflux)")
}

// While another process holds <name>.lock the watch batch is re-queued with a
// "busy, will retry" progress note; once the lock is free the next tick syncs.
func TestWatch_BusyLockRetriesUntilSynced(t *testing.T) {
	s, h, cfg, docs := watchTestServer(t)

	lockPath := filepath.Join(cfg.IndexDir, "watched.lock")
	l, err := store.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("taking the index lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, "busy.md"), []byte("# Busy\n\nThe busybeetle waits for the lock.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	busy := false
	for time.Now().Before(deadline) && !busy {
		for _, task := range s.bgManager.List("") {
			if task.Type == background.TaskTypeAutoIndex && strings.Contains(task.Message, "busy, will retry") {
				busy = true
				break
			}
		}
		if !busy {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !busy {
		l.Release()
		t.Fatal("no auto-index task reported busy, will retry while the lock was held")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range watchSearch(t, h, "busybeetle") {
			if strings.Contains(r.Text, "busybeetle") {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the re-queued batch was never synced after the lock was released")
}

// lockedSyncHook imitates a real sync: it takes <name>.lock like SyncChanged,
// signals entered, and keeps the lock until released. Cancellation of ctx is
// noted on cancelled but, like a real sync unwinding, it takes until release
// before the hook returns and frees the lock.
type lockedSyncHook struct {
	entered, cancelled, release chan struct{}
	calls                       atomic.Int32
	once                        sync.Once
}

func newLockedSyncHook() *lockedSyncHook {
	return &lockedSyncHook{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
}

func (h *lockedSyncHook) fn(s *Server, ctx context.Context, name string, _ []string) error {
	h.calls.Add(1)
	unlock, err := indexing.LockIndex(ctx, s.cfg().IndexDir, name)
	if err != nil {
		return err
	}
	defer unlock()
	h.once.Do(func() { close(h.entered) })
	go func() {
		<-ctx.Done()
		close(h.cancelled)
	}()
	<-h.release
	return ctx.Err()
}

func waitChan(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func triggerWatchSync(t *testing.T, docs, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(docs, name), []byte("# T\n\nA changed document.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Stop must cancel a sync that is running and WAIT for it: it returns only
// after the sync released the index lock, and the lock is then free.
func TestWatch_StopWaitsForRunningSync(t *testing.T) {
	h := newLockedSyncHook()
	s, _, cfg, docs := watchTestServerHook(t, h.fn)
	triggerWatchSync(t, docs, "stop.md")
	waitChan(t, h.entered, 30*time.Second, "the watch sync to start")

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		stopped <- s.Stop(ctx)
	}()
	waitChan(t, h.cancelled, 5*time.Second, "Stop to cancel the sync context")
	select {
	case <-stopped:
		t.Fatal("Stop returned while a watch sync was still running")
	case <-time.After(300 * time.Millisecond):
	}
	close(h.release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop never returned after the sync ended")
	}
	l, err := store.AcquireLock(filepath.Join(cfg.IndexDir, "watched.lock"))
	if err != nil {
		t.Fatalf("the server left the index lock held after Stop: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}
}

func deleteIndex(h http.Handler, name string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/api/indexes/"+name, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// Deleting an index while its watch sync is running cancels that sync and
// waits for it before removing anything; afterwards nothing recreates the index.
func TestWatch_DeleteWhileSyncing(t *testing.T) {
	h := newLockedSyncHook()
	s, mux, cfg, docs := watchTestServerHook(t, h.fn)
	triggerWatchSync(t, docs, "del.md")
	waitChan(t, h.entered, 30*time.Second, "the watch sync to start")

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- deleteIndex(mux, "watched") }()
	waitChan(t, h.cancelled, 5*time.Second, "DELETE to cancel the running sync")
	select {
	case <-done:
		t.Fatal("DELETE returned while the sync still held the index")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "watched")); err != nil {
		t.Fatalf("index removed under a running sync: %v", err)
	}
	close(h.release)
	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("DELETE never returned")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body.String())
	}
	if ai := s.getAutoIndexer(); ai != nil && ai.WatchedDir("watched") != "" {
		t.Error("the deleted index is still watched")
	}

	// Edits after the delete must not sync (the hook would count them) or
	// bring the index back.
	before := h.calls.Load()
	triggerWatchSync(t, docs, "after.md")
	time.Sleep(7 * time.Second) // more than one default debounce tick
	if n := h.calls.Load(); n != before {
		t.Errorf("a sync ran after the index was deleted (%d -> %d calls)", before, n)
	}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "watched")); !os.IsNotExist(err) {
		t.Errorf("the deleted index exists again: %v", err)
	}
}

// A delete takes <name>.lock: while another build or sync holds it the
// delete is refused (409) and nothing is removed.
func TestWatch_DeleteRefusedWhileLockHeld(t *testing.T) {
	s, mux, cfg, docs := watchTestServer(t)
	l, err := store.AcquireLock(filepath.Join(cfg.IndexDir, "watched.lock"))
	if err != nil {
		t.Fatal(err)
	}
	w := deleteIndex(mux, "watched")
	_ = l.Release()
	if w.Code != http.StatusConflict {
		t.Fatalf("DELETE with the lock held = %d %s, want 409", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "watched")); err != nil {
		t.Errorf("a refused delete removed the index: %v", err)
	}
	// A delete that did not happen leaves the watch as it was.
	if got := s.getAutoIndexer().WatchedDir("watched"); got != docs {
		t.Errorf("watch after a refused delete = %q, want %q", got, docs)
	}
	if w := deleteIndex(mux, "watched"); w.Code != http.StatusOK {
		t.Errorf("DELETE after the lock was released = %d %s", w.Code, w.Body.String())
	}
}

// An index removed behind the server's back (another process) is unwatched
// by the next failed sync instead of failing a task per edit forever.
func TestWatch_IndexRemovedExternallyDropsWatch(t *testing.T) {
	s, _, cfg, docs := watchTestServer(t)
	if err := gleann.RemoveIndex(cfg.IndexDir, "watched"); err != nil {
		t.Fatal(err)
	}
	triggerWatchSync(t, docs, "gone.md")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if s.getAutoIndexer().WatchedDir("watched") == "" {
			for _, task := range s.bgManager.List("") {
				if task.Type == background.TaskTypeAutoIndex && task.Status == background.StatusFailed {
					t.Fatalf("task failed instead of dropping the watch: %+v", task)
				}
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the watch of a vanished index was never dropped")
}
