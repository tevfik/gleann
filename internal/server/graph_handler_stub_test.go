//go:build !treesitter

package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleGraphIndex_StubReturnsError(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph/test-index/index", bytes.NewBufferString(`{"docs_dir":"/tmp/test"}`))
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

// ── memory stub handlers ─────────────────────────────────────────────────

func TestMemoryStub_HandleMemoryInject(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("POST", "/api/memory/test/inject", nil)
	w := httptest.NewRecorder()
	s.handleMemoryInject(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_HandleMemoryDeleteNode(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("DELETE", "/api/memory/test/nodes/abc", nil)
	w := httptest.NewRecorder()
	s.handleMemoryDeleteNode(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_HandleMemoryDeleteEdge(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("DELETE", "/api/memory/test/edges", nil)
	w := httptest.NewRecorder()
	s.handleMemoryDeleteEdge(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_HandleMemoryTraverse(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("POST", "/api/memory/test/traverse", nil)
	w := httptest.NewRecorder()
	s.handleMemoryTraverse(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_StopMemoryPool(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	// Should not panic.
	s.stopMemoryPool(nil)
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
