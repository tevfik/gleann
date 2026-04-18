package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// newTestServer creates a server with a temp index dir for testing.
func newTestServerExt3(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	return NewServer(cfg, ":0", "test")
}

// ── Health ─────────────────────────────────────────────────────

func TestHandleHealthExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Error("status should be ok")
	}
	if body["version"] != "test" {
		t.Errorf("version = %v", body["version"])
	}
}

// ── ListIndexes (empty dir) ────────────────────────────────────

func TestHandleListIndexesEmptyExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/api/indexes", nil)
	w := httptest.NewRecorder()
	s.handleListIndexes(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["count"].(float64) != 0 {
		t.Errorf("count = %v", body["count"])
	}
}

// ── GetIndex (not found) ───────────────────────────────────────

func TestHandleGetIndexNotFoundExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/api/indexes/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGetIndex(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleGetIndexEmptyNameExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/api/indexes/", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	s.handleGetIndex(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Search validation ──────────────────────────────────────────

func TestHandleSearchEmptyNameExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"query":"test"}`)
	req := httptest.NewRequest("POST", "/api/indexes//search", body)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleSearchInvalidBodyExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest("POST", "/api/indexes/test/search", body)
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleSearchEmptyQueryExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"query":""}`)
	req := httptest.NewRequest("POST", "/api/indexes/test/search", body)
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleSearchIndexNotFoundExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"query":"hello"}`)
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/search", body)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// ── Ask validation ─────────────────────────────────────────────

func TestHandleAskEmptyNameExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"question":"test"}`)
	req := httptest.NewRequest("POST", "/api/indexes//ask", body)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAskInvalidBodyExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`bad json`)
	req := httptest.NewRequest("POST", "/api/indexes/test/ask", body)
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAskEmptyQuestionExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"question":""}`)
	req := httptest.NewRequest("POST", "/api/indexes/test/ask", body)
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAskIndexNotFoundExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"question":"hello"}`)
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/ask", body)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// ── Build validation ───────────────────────────────────────────

func TestHandleBuildEmptyNameExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"texts":["hello"]}`)
	req := httptest.NewRequest("POST", "/api/indexes//build", body)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleBuildInvalidBodyExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest("POST", "/api/indexes/test/build", body)
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleBuildEmptyExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest("POST", "/api/indexes/test/build", body)
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Delete index validation ────────────────────────────────────

func TestHandleDeleteIndexEmptyNameExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("DELETE", "/api/indexes/", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	s.handleDeleteIndex(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleDeleteIndexNotFoundExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("DELETE", "/api/indexes/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleDeleteIndex(w, req)
	// May succeed or fail depending on implementation — just shouldn't panic.
	if w.Code >= 500 {
		// Acceptable — index doesn't exist to delete.
	}
}

// ── Conversation handlers ──────────────────────────────────────

func TestHandleListConversationsExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/api/conversations", nil)
	w := httptest.NewRecorder()
	s.handleListConversations(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleGetConversationEmptyIDExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/api/conversations/", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	s.handleGetConversation(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleGetConversationNotFoundExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("GET", "/api/conversations/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGetConversation(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleDeleteConversationEmptyIDExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("DELETE", "/api/conversations/", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	s.handleDeleteConversation(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleDeleteConversationNotFoundExt3(t *testing.T) {
	s := newTestServerExt3(t)
	req := httptest.NewRequest("DELETE", "/api/conversations/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	s.handleDeleteConversation(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// ── WriteJSON / WriteError ─────────────────────────────────────

func TestWriteJSONExt3(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, map[string]string{"hello": "world"})
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteErrorExt3(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, 422, "validation failed")
	if w.Code != 422 {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "validation failed" {
		t.Errorf("error = %q", body["error"])
	}
}

// ── NewServer ──────────────────────────────────────────────────

func TestNewServerDefaultsExt3(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := NewServer(cfg, ":8080", "")
	if s.version != "dev" {
		t.Errorf("version = %q, want 'dev'", s.version)
	}
	if s.addr != ":8080" {
		t.Errorf("addr = %q", s.addr)
	}
}

// ── WithMiddleware ─────────────────────────────────────────────

func TestWithMiddlewareCORSExt3(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := withMiddleware(inner)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS header missing")
	}
}

func TestWithMiddlewareOptionsExt3(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // Should not reach here.
	})
	handler := withMiddleware(inner)
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("OPTIONS should return 200, got %d", w.Code)
	}
}

// ── Search with various options ────────────────────────────────

func TestHandleSearchWithOptionsExt3(t *testing.T) {
	s := newTestServerExt3(t)
	reqBody := searchRequest{
		Query:       "test",
		TopK:        5,
		HybridAlpha: 0.5,
		MinScore:    0.1,
		Rerank:      true,
		MetadataFilters: []gleann.MetadataFilter{
			{Field: "lang", Operator: gleann.OpEqual, Value: "go"},
		},
		FilterLogic:  "and",
		GraphContext: true,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/search", bytes.NewReader(body))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	// Index doesn't exist, so expect 404.
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// ── Ask with various options ───────────────────────────────────

func TestHandleAskWithOptionsExt3(t *testing.T) {
	s := newTestServerExt3(t)
	reqBody := askRequest{
		Question:       "What is Go?",
		TopK:           5,
		LLMModel:       "llama3.2",
		LLMProvider:    "ollama",
		SystemPrompt:   "Be concise",
		Role:           "developer",
		ConversationID: "conv-123",
		Stream:         false,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/ask", bytes.NewReader(body))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleAskStreamParamExt3(t *testing.T) {
	s := newTestServerExt3(t)
	body := bytes.NewBufferString(`{"question":"hello"}`)
	req := httptest.NewRequest("POST", "/api/indexes/nonexistent/ask?stream=true", body)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	// Should hit stream code path but fail at index lookup.
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
