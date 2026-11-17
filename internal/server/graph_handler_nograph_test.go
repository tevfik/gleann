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

// A format-1 (or unknown) index has no code graph in any build (RFC 004 A3):
// POST /api/graph/{name}/index answers 409 and creates nothing.
func TestHandleGraphIndex_Format1Conflict(t *testing.T) {
	s := newTestServerWithGraph(nil)
	s.config.IndexDir = t.TempDir()
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
// errNoCodeGraph and the hint for a format-1 index.
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
		t.Errorf("format-1 hint missing: %v", err)
	}
	if _, err := pool.get("nonexistent"); !errors.Is(err, errNoCodeGraph) {
		t.Errorf("get(nonexistent) = %v, want errNoCodeGraph", err)
	}
}

func TestRunGraphIndex_Format1(t *testing.T) {
	err := runGraphIndex("test", t.TempDir(), t.TempDir())
	if !errors.Is(err, errNoCodeGraph) {
		t.Errorf("runGraphIndex on a format-1 index = %v, want errNoCodeGraph", err)
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
