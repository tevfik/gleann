package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// An index in the old store format gets no code graph in any build (RFC 004
// B2): POST /api/graph/{name}/index answers 409 with the migration hint and
// creates nothing.
func TestHandleGraphIndex_OldFormatConflict(t *testing.T) {
	s := newTestServerWithGraph(nil)
	s.config.IndexDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(s.config.IndexDir, "test-index"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.config.IndexDir, "test-index", "test-index.passages.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)
	req := httptest.NewRequest("POST", "/api/graph/test-index/index", bytes.NewBufferString(`{"docs_dir":`+strconv.Quote(docs)+`}`))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "gleann index migrate") {
		t.Errorf("the answer must name the conversion: %s", w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.config.IndexDir, "test-index_graph")); !os.IsNotExist(err) {
		t.Errorf("no test-index_graph directory may be created: %v", err)
	}
}

// An old <name>_graph Kùzu directory is not a code graph: the pool reports
// errNoCodeGraph and the migration hint for an index in the old format.
func TestGraphDBPool_Get_OldKuzuDirectoryIsNoGraph(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old", "old.passages.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "old_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	pool := newGraphDBPool(dir)
	_, err := pool.get("old")
	if !errors.Is(err, errNoCodeGraph) {
		t.Fatalf("get = %v, want errNoCodeGraph", err)
	}
	if !strings.Contains(err.Error(), "gleann index migrate old") {
		t.Errorf("migration hint missing: %v", err)
	}
	if _, err := pool.get("nonexistent"); !errors.Is(err, errNoCodeGraph) {
		t.Errorf("get(nonexistent) = %v, want errNoCodeGraph", err)
	}
}

func TestRunGraphIndex_MissingAndOldIndex(t *testing.T) {
	err := runGraphIndex("test", t.TempDir(), t.TempDir())
	if !errors.Is(err, errNoCodeGraph) {
		t.Errorf("runGraphIndex on a missing index = %v, want errNoCodeGraph", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old", "old.meta.json"), []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = runGraphIndex("old", t.TempDir(), dir)
	if !errors.Is(err, gleann.ErrOldStoreFormat) || !strings.Contains(err.Error(), "gleann index migrate old") {
		t.Errorf("runGraphIndex on an old-format index = %v, want the migration refusal", err)
	}
}

// ── memory pool lifecycle ─────────────────────────────────────────────────

func TestMemoryStub_StopMemoryPool(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	// Should not panic.
	s.stopMemoryPool(context.Background())
}

func TestMemoryStub_CloseAll(t *testing.T) {
	p := newMemoryPool("/tmp")
	p.closeAll() // should not panic
}

// Search and the other searcher-backed endpoints answer 409 with the
// migration hint for an index in the old store format, not 404.
func TestAcquireSearcher_OldFormatConflict(t *testing.T) {
	s := newTestServerWithGraph(nil)
	s.config.IndexDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(s.config.IndexDir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.config.IndexDir, "old", "old.passages.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.acquireSearcher(context.Background(), "old")
	if !errors.Is(err, gleann.ErrOldStoreFormat) {
		t.Fatalf("acquireSearcher = %v, want ErrOldStoreFormat", err)
	}
	w := httptest.NewRecorder()
	writeSearcherError(w, httptest.NewRequest(http.MethodGet, "/", nil), "old", err)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "gleann index migrate old") {
		t.Errorf("got %d %s, want 409 with the migration hint", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	writeSearcherError(w, httptest.NewRequest(http.MethodGet, "/", nil), "x", errors.New("boom"))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("other errors: got %d, want 500", w.Code)
	}
}
