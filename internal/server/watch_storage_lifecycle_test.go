package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/pkg/gleann"
)

// writeMeta stores a hand-made <dir>/<name>/<name>.meta.json (plus an empty
// vector file of indexBytes when > 0) so handlers see an existing index.
func writeMeta(t *testing.T, dir string, meta gleann.IndexMeta, indexBytes int) {
	t.Helper()
	idir := filepath.Join(dir, meta.Name)
	if err := os.MkdirAll(idir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(idir, meta.Name+".meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if indexBytes > 0 {
		if err := os.WriteFile(filepath.Join(idir, meta.Name+".index"), make([]byte, indexBytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func waitTaskDone(t *testing.T, m *background.Manager, id string) *background.Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if tk := m.Get(id); tk != nil && (tk.Status == background.StatusCompleted || tk.Status == background.StatusFailed) {
			return tk
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return nil
}

// ── POST /api/indexes/{name}/storage ─────────────────────────────────────────

func TestSetStorage_ValidationAndConflicts(t *testing.T) {
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := NewServer(cfg, ":0", "test")
	t.Cleanup(s.bgManager.Stop)
	h := s.buildMux()

	// 10 passages x 8 dims x 4 bytes = 320 bytes of full vectors.
	writeMeta(t, dir, gleann.IndexMeta{Name: "full", Backend: "hnsw", Dimensions: 8, NumPassages: 10, VectorReady: true}, 320)
	writeMeta(t, dir, gleann.IndexMeta{Name: "disk", Backend: "diskann", Dimensions: 8, NumPassages: 10, VectorReady: true}, 320)

	post := func(name, body string) int {
		w := postJSON(h, "/api/indexes/"+name+"/storage", body)
		return w.Code
	}

	if c := post("full", `{"mode":"recompute"}`); c != http.StatusNotImplemented {
		t.Errorf("no switcher installed: status %d, want 501", c)
	}
	s.SetStorageSwitcher(func(gleann.Config, string, string) error { return nil })

	tests := []struct {
		name, index, body string
		want              int
	}{
		{"invalid index name", "bad%20name", `{"mode":"store"}`, http.StatusBadRequest},
		{"malformed body", "full", `{`, http.StatusBadRequest},
		{"unknown mode", "full", `{"mode":"swap"}`, http.StatusBadRequest},
		{"unknown index", "ghost", `{"mode":"store"}`, http.StatusNotFound},
		{"diskann cannot be switched", "disk", `{"mode":"recompute"}`, http.StatusConflict},
		{"already in the requested mode", "full", `{"mode":"store"}`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := post(tt.index, tt.body); got != tt.want {
				t.Fatalf("status %d, want %d", got, tt.want)
			}
		})
	}
	w := postJSON(h, "/api/indexes/full/storage", `{"mode":"store"}`)
	if !strings.Contains(w.Body.String(), `"unchanged":true`) {
		t.Errorf("no-op switch must say so: %s", w.Body.String())
	}
}

func TestSetStorage_ConvertsInBackground(t *testing.T) {
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := NewServer(cfg, ":0", "test")
	t.Cleanup(s.bgManager.Stop)
	writeMeta(t, dir, gleann.IndexMeta{Name: "ix", Backend: "hnsw", Dimensions: 8, NumPassages: 10, VectorReady: true}, 320)

	type call struct{ name, mode string }
	calls := make(chan call, 4)
	var fail error
	s.SetStorageSwitcher(func(c gleann.Config, name, target string) error {
		if c.IndexDir != dir {
			t.Errorf("switcher got IndexDir %q", c.IndexDir)
		}
		calls <- call{name, target}
		return fail
	})
	h := s.buildMux()

	run := func() *background.Task {
		w := postJSON(h, "/api/indexes/ix/storage", `{"mode":"recompute"}`)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			TaskID string `json:"task_id"`
			Mode   string `json:"mode"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Mode != "recompute" {
			t.Fatalf("body %s (%v)", w.Body.String(), err)
		}
		return waitTaskDone(t, s.bgManager, resp.TaskID)
	}

	if tk := run(); tk.Status != background.StatusCompleted {
		t.Fatalf("task = %+v", tk)
	}
	if c := <-calls; c != (call{"ix", "recompute"}) {
		t.Errorf("switcher called with %+v", c)
	}

	fail = errors.New("re-embedding failed")
	tk := run()
	if tk.Status != background.StatusFailed || !strings.Contains(tk.Error, "re-embedding failed") {
		t.Fatalf("a switcher error must fail the task: %+v", tk)
	}
}

// ── POST /api/indexes/{name}/watch ───────────────────────────────────────────

func newWatchServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := NewServer(cfg, ":0", "test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s, s.buildMux(), dir
}

func TestWatch_EnableAndDisablePersistsInMeta(t *testing.T) {
	s, h, dir := newWatchServer(t)
	src := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", src)
	writeMeta(t, dir, gleann.IndexMeta{Name: "docs"}, 0)

	w := postJSON(h, "/api/indexes/docs/watch", `{"enable":true,"dir":`+jsonStr(src)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	meta, err := gleann.GetIndexMeta(dir, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.AutoWatch || meta.SourceDir != src {
		t.Errorf("meta after enable = %+v", meta)
	}
	ai := s.getAutoIndexer()
	if ai == nil || len(ai.WatchedIndexes()) != 1 || ai.WatchedIndexes()[0] != "docs" {
		t.Fatalf("auto-indexer not watching docs")
	}

	// Re-enabling without dir reuses the recorded SourceDir (no allowed_roots check).
	t.Setenv("GLEANN_ALLOWED_ROOTS", t.TempDir())
	if w := postJSON(h, "/api/indexes/docs/watch", `{"enable":true}`); w.Code != http.StatusOK {
		t.Fatalf("re-enable from SourceDir: %d %s", w.Code, w.Body.String())
	}

	w = postJSON(h, "/api/indexes/docs/watch", `{"enable":false}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"watch":false`) {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if len(ai.WatchedIndexes()) != 0 {
		t.Error("index still watched after disable")
	}
	if meta, _ := gleann.GetIndexMeta(dir, "docs"); meta.AutoWatch {
		t.Error("AutoWatch not cleared")
	}
}

func TestWatch_Errors(t *testing.T) {
	_, h, dir := newWatchServer(t)
	writeMeta(t, dir, gleann.IndexMeta{Name: "nodir"}, 0)
	outside := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", t.TempDir())

	tests := []struct {
		name, path, body string
		want             int
	}{
		{"invalid name", "bad%20name", `{"enable":true}`, http.StatusBadRequest},
		{"malformed body", "nodir", `{`, http.StatusBadRequest},
		{"unknown index", "ghost", `{"enable":true,"dir":"x"}`, http.StatusNotFound},
		{"nothing to watch", "nodir", `{"enable":true}`, http.StatusBadRequest},
		{"dir outside allowed roots", "nodir", `{"enable":true,"dir":` + jsonStr(outside) + `}`, http.StatusForbidden},
		{"missing dir", "nodir", `{"enable":true,"dir":` + jsonStr(filepath.Join(t.TempDir(), "gone")) + `}`, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := postJSON(h, "/api/indexes/"+tt.path+"/watch", tt.body)
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
	// Disabling something that was never enabled is harmless.
	if w := postJSON(h, "/api/indexes/nodir/watch", `{"enable":false}`); w.Code != http.StatusOK {
		t.Errorf("disable without auto-indexer: %d", w.Code)
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

// ── Server.Start / startAutoIndexer ──────────────────────────────────────────

func TestStartAutoIndexer_WatchesEnvDirsAndPersistentIndexes(t *testing.T) {
	dir := t.TempDir()
	envDir, persistDir := t.TempDir(), t.TempDir()
	writeMeta(t, dir, gleann.IndexMeta{Name: "persist", AutoWatch: true, SourceDir: persistDir}, 0)
	writeMeta(t, dir, gleann.IndexMeta{Name: "unwatched", SourceDir: t.TempDir()}, 0)
	t.Setenv("GLEANN_AUTO_INDEX_DIRS", "envidx:"+envDir+", malformed ,:nodir,noDir:")

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := NewServer(cfg, ":0", "test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	s.startAutoIndexer()

	ai := s.getAutoIndexer()
	if ai == nil {
		t.Fatal("auto-indexer must be kept even when idle")
	}
	got := ai.WatchedIndexes()
	sort.Strings(got)
	if strings.Join(got, ",") != "envidx,persist" {
		t.Errorf("watched = %v, want [envidx persist]", got)
	}
}

func TestStart_FailsFastOnUnusableAddress(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := NewServer(cfg, "127.0.0.1:99999", "test") // port out of range
	err := s.Start()
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Start must report the listen error, got %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop after failed Start: %v", err)
	}
}
