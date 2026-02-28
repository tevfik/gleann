package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleHealth(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}
	if resp["engine"] != "gleann-go" {
		t.Errorf("expected engine gleann-go, got %v", resp["engine"])
	}
}

func TestHandleListIndexesEmpty(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/indexes", nil)
	w := httptest.NewRecorder()

	s.handleListIndexes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	count := resp["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 indexes, got %v", count)
	}
}

func TestHandleGetIndexNotFound(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/indexes/{name}", s.handleGetIndex)

	req := httptest.NewRequest(http.MethodGet, "/api/indexes/nonexistent", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleSearchBadRequest(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)

	// Invalid JSON body.
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/search", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSearchEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)

	body, _ := json.Marshal(searchRequest{Query: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/search", bytes.NewReader(body))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDeleteIndexNotFound(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/indexes/{name}", s.handleDeleteIndex)

	req := httptest.NewRequest(http.MethodDelete, "/api/indexes/nonexistent", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// Should succeed even if nothing to delete (os.RemoveAll is idempotent).
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleBuildBadRequest(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/build", s.handleBuild)

	// Invalid JSON body.
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/build", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleBuildNoTexts(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/build", s.handleBuild)

	body, _ := json.Marshal(buildRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/build", bytes.NewReader(body))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["key"] != "value" {
		t.Errorf("expected value, got %s", resp["key"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusNotFound, "not found")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "not found" {
		t.Errorf("expected 'not found', got %s", resp["error"])
	}
}

func TestCORSMiddleware(t *testing.T) {
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header")
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // Should not be reached.
	}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", w.Code)
	}
}
