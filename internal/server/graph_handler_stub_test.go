//go:build !treesitter

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleGraphIndex_StubReturnsError(t *testing.T) {
	s := newTestServerWithGraph(nil)
	docs := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)
	req := httptest.NewRequest("POST", "/api/graph/test-index/index", bytes.NewBufferString(`{"docs_dir":`+strconv.Quote(docs)+`}`))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestGraphDBPool_Get_StubReturnsError(t *testing.T) {
	pool := newGraphDBPool("/tmp")
	_, err := pool.get("nonexistent")
	if err == nil {
		t.Error("expected error from stub openGraphDB")
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

// ── graph stub functions ─────────────────────────────────────────────────

func TestGraphStub_OpenGraphDB(t *testing.T) {
	_, err := openGraphDB("/tmp/test")
	if err == nil {
		t.Error("expected error from stub openGraphDB")
	}
}

func TestGraphStub_RunGraphIndex(t *testing.T) {
	err := runGraphIndex("test", "/tmp/docs", "/tmp/index", "github.com/test")
	if err == nil {
		t.Error("expected error from stub runGraphIndex")
	}
}
