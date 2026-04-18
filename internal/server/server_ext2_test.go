package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── handleSearch validation ────────────────────────────────────

func TestHandleSearchMissingName(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes//search", strings.NewReader(`{"query":"test"}`))
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleSearchBadJSON(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/test/search", strings.NewReader("not json"))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleSearchEmptyQuery2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/test/search", strings.NewReader(`{"query":""}`))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleSearchIndexNotFound(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/search", strings.NewReader(`{"query":"hello"}`))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleAsk validation ───────────────────────────────────────

func TestHandleAskMissingName(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes//ask", strings.NewReader(`{"question":"test"}`))
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleAskBadJSON(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/test/ask", strings.NewReader("bad"))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleAskEmptyQuestion(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/test/ask", strings.NewReader(`{"question":""}`))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleAskIndexNotFound(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/ask", strings.NewReader(`{"question":"hello?"}`))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleGetIndex validation ──────────────────────────────────

func TestHandleGetIndexMissingName2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/indexes/", nil)
	w := httptest.NewRecorder()
	s.handleGetIndex(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleBuild validation ─────────────────────────────────────

func TestHandleBuildMissingName(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes//build", strings.NewReader(`{"texts":["hello"]}`))
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleBuildBadJSON2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/test/build", strings.NewReader("bad"))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleBuildNoTexts2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/indexes/test/build", strings.NewReader(`{}`))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleDeleteIndex validation ───────────────────────────────

func TestHandleDeleteIndexMissing2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("DELETE", "/api/indexes/", nil)
	w := httptest.NewRecorder()
	s.handleDeleteIndex(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleListConversations ────────────────────────────────────

func TestHandleListConversations2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/conversations", nil)
	w := httptest.NewRecorder()
	s.handleListConversations(w, req)
	// Should return 200 even when no conversations.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleGetConversation ──────────────────────────────────────

func TestHandleGetConversationMissingID(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/conversations/", nil)
	w := httptest.NewRecorder()
	s.handleGetConversation(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleGetConversationNotFound(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/api/conversations/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGetConversation(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleDeleteConversation ───────────────────────────────────

func TestHandleDeleteConversationMissingID(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("DELETE", "/api/conversations/", nil)
	w := httptest.NewRecorder()
	s.handleDeleteConversation(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleMultiSearch ──────────────────────────────────────────

func TestHandleMultiSearchBadJSON(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/search", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	s.handleMultiSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestHandleMultiSearchEmptyQuery2(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("POST", "/api/search", strings.NewReader(`{"query":""}`))
	w := httptest.NewRecorder()
	s.handleMultiSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

// ── handleHealth ───────────────────────────────────────────────

func TestHandleHealthResponse(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v", resp["status"])
	}
	if resp["version"] != "test" {
		t.Errorf("version = %v", resp["version"])
	}
}

// ── handleListIndexes ──────────────────────────────────────────

func TestHandleListIndexesEmpty2(t *testing.T) {
	s := testServer()
	s.config.IndexDir = t.TempDir() // empty dir
	req := httptest.NewRequest("GET", "/api/indexes", nil)
	w := httptest.NewRecorder()
	s.handleListIndexes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("count = %v", resp["count"])
	}
}

// ── writeJSON / writeError ─────────────────────────────────────

func TestWriteJSONContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"foo": "bar"})
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestWriteErrorBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "test error")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "test error" {
		t.Errorf("error = %q", resp["error"])
	}
}

// ── NewServer defaults ─────────────────────────────────────────

func TestNewServerDefaultVersion2(t *testing.T) {
	s := NewServer(testServer().config, ":0", "")
	if s.version != "dev" {
		t.Errorf("version = %q, want dev", s.version)
	}
}
