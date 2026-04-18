package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func testServer() *Server {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = "/tmp/gleann-test-nonexistent"
	return NewServer(cfg, ":0", "test")
}

func TestNewServer(t *testing.T) {
	s := testServer()
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.version != "test" {
		t.Errorf("version = %q, want test", s.version)
	}
	if s.config.IndexDir == "" {
		t.Error("IndexDir should not be empty")
	}
}

func TestNewServerDefaultVersion(t *testing.T) {
	cfg := gleann.DefaultConfig()
	s := NewServer(cfg, ":0", "")
	if s.version != "dev" {
		t.Errorf("version = %q, want dev", s.version)
	}
}

func TestHandleHealthExt(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Error("status should be ok")
	}
	if resp["version"] != "test" {
		t.Errorf("version = %v", resp["version"])
	}
}

func TestHandleListIndexes(t *testing.T) {
	s := testServer()
	s.config.IndexDir = t.TempDir()

	req := httptest.NewRequest("GET", "/api/indexes", nil)
	w := httptest.NewRecorder()
	s.handleListIndexes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	count, ok := resp["count"].(float64)
	if !ok || count != 0 {
		t.Errorf("count = %v", resp["count"])
	}
}

func TestHandleGetIndexMissing(t *testing.T) {
	s := testServer()
	s.config.IndexDir = t.TempDir()

	req := httptest.NewRequest("GET", "/api/indexes/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGetIndex(w, req)

	if w.Code == http.StatusOK {
		t.Error("should not return 200 for missing index")
	}
}

func TestHandleGetIndexEmpty(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/indexes/", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	s.handleGetIndex(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleMetricsExt(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	s.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleListConversations(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/conversations", nil)
	w := httptest.NewRecorder()
	s.handleListConversations(w, req)

	// Should return OK even if no conversations.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleDeleteIndexMissing(t *testing.T) {
	s := testServer()
	s.config.IndexDir = t.TempDir()
	req := httptest.NewRequest("DELETE", "/api/indexes/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleDeleteIndex(w, req)

	// Should succeed (RemoveAll on non-existent is OK).
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleListWebhooks(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/webhooks", nil)
	w := httptest.NewRecorder()
	s.handleListWebhooks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleOpenAPISpecExt(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/openapi.json", nil)
	w := httptest.NewRecorder()
	s.handleOpenAPISpec(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHandleSwaggerUIExt(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/docs", nil)
	w := httptest.NewRecorder()
	s.handleSwaggerUI(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/html" && ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHandleListModels(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	s.handleListModels(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleBlockStatsExt(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/blocks/stats", nil)
	w := httptest.NewRecorder()
	s.handleBlockStats(w, req)

	// Might return error if no store is initialized, but should not panic.
	if w.Code == 0 {
		t.Error("should have a response code")
	}
}

func TestHandleListBlocks(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/blocks", nil)
	w := httptest.NewRecorder()
	s.handleListBlocks(w, req)

	if w.Code == 0 {
		t.Error("should have a response code")
	}
}
