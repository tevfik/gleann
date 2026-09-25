package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)


func TestHandleHealth(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test-1.2.3",
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
	if resp["version"] != "test-1.2.3" {
		t.Errorf("expected version test-1.2.3, got %v", resp["version"])
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

func TestHandleUpdateIndexBadRequest(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/update", s.handleUpdateIndex)

	body, _ := json.Marshal(updateIndexRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/update", bytes.NewReader(body))
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

func TestHandlePatchIndex(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	idxDir := filepath.Join(dir, "myindex")
	os.MkdirAll(idxDir, 0755)
	initialMeta := gleann.IndexMeta{
		Name: "myindex",
	}
	data, _ := json.Marshal(initialMeta)
	os.WriteFile(filepath.Join(idxDir, "myindex.meta.json"), data, 0644)

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/indexes/{name}", s.handlePatchIndex)

	patchBody := `{"tags":["work","go"],"description":"Test index","mcp_exposed":false}`
	req := httptest.NewRequest(http.MethodPatch, "/api/indexes/myindex", strings.NewReader(patchBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	meta, err := gleann.GetIndexMeta(dir, "myindex")
	if err != nil {
		t.Fatalf("GetIndexMeta failed: %v", err)
	}
	if meta.Description != "Test index" {
		t.Errorf("description mismatch: %s", meta.Description)
	}
	if meta.IsMCPExposed() {
		t.Errorf("expected mcp_exposed to be false")
	}
	if !meta.HasTag("work") || !meta.HasTag("go") {
		t.Errorf("tags mismatch: %v", meta.Tags)
	}
}

func TestHandleSearch_TargetIsolation(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	idxDirA := filepath.Join(dir, "alpha")
	os.MkdirAll(idxDirA, 0755)
	metaA := gleann.IndexMeta{Name: "alpha", MCPExposed: &[]bool{true}[0]}
	dataA, _ := json.Marshal(metaA)
	os.WriteFile(filepath.Join(idxDirA, "alpha.meta.json"), dataA, 0644)

	idxDirB := filepath.Join(dir, "beta")
	os.MkdirAll(idxDirB, 0755)
	metaB := gleann.IndexMeta{Name: "beta", MCPExposed: &[]bool{true}[0]}
	dataB, _ := json.Marshal(metaB)
	os.WriteFile(filepath.Join(idxDirB, "beta.meta.json"), dataB, 0644)

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)

	// 1. Client restricted to alpha attempts to query beta -> 403 Forbidden
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/beta/search", strings.NewReader(`{"query":"test"}`))
	req.Header.Set("X-Gleann-Target", "alpha")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", w.Code)
	}

	// 2. Client restricted to alpha queries alpha -> permitted (proceeds past target check)
	req2 := httptest.NewRequest(http.MethodPost, "/api/indexes/alpha/search", strings.NewReader(`{"query":"test"}`))
	req2.Header.Set("X-Gleann-Target", "alpha")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code == http.StatusForbidden {
		t.Errorf("unexpected 403 Forbidden for permitted target: %s", w2.Body.String())
	}
}

func TestHandleAsk_TargetIsolation(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/ask", s.handleAsk)

	req := httptest.NewRequest(http.MethodPost, "/api/indexes/secret/ask", strings.NewReader(`{"question":"how to hack"}`))
	req.Header.Set("X-Gleann-Target", "allowed-repo")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", w.Code)
	}
}

func TestHandleListIndexes_TargetIsolation(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	for _, item := range []struct {
		name    string
		exposed bool
		tags    []string
	}{
		{"repo-a", true, []string{"work"}},
		{"repo-b", true, []string{"personal"}},
		{"repo-c", false, []string{"secret"}},
	} {
		p := filepath.Join(dir, item.name)
		os.MkdirAll(p, 0755)
		meta := gleann.IndexMeta{
			Name:       item.name,
			MCPExposed: &item.exposed,
			Tags:       item.tags,
		}
		d, _ := json.Marshal(meta)
		os.WriteFile(filepath.Join(p, item.name+".meta.json"), d, 0644)
	}

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	// 1. Target isolation via query param ?target=repo-a
	req1 := httptest.NewRequest(http.MethodGet, "/api/indexes?target=repo-a", nil)
	w1 := httptest.NewRecorder()
	s.handleListIndexes(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}
	var resp1 map[string]any
	json.NewDecoder(w1.Body).Decode(&resp1)
	if resp1["count"].(float64) != 1 {
		t.Errorf("expected 1 index, got %v", resp1["count"])
	}

	// 2. Tag isolation via ?tag=work
	req2 := httptest.NewRequest(http.MethodGet, "/api/indexes?tag=work", nil)
	w2 := httptest.NewRecorder()
	s.handleListIndexes(w2, req2)
	var resp2 map[string]any
	json.NewDecoder(w2.Body).Decode(&resp2)
	if resp2["count"].(float64) != 1 {
		t.Errorf("expected 1 index with tag work, got %v", resp2["count"])
	}

	// 3. Exposed only via ?exposed_only=true
	req3 := httptest.NewRequest(http.MethodGet, "/api/indexes?exposed_only=true", nil)
	w3 := httptest.NewRecorder()
	s.handleListIndexes(w3, req3)
	var resp3 map[string]any
	json.NewDecoder(w3.Body).Decode(&resp3)
	if resp3["count"].(float64) != 2 {
		t.Errorf("expected 2 exposed indexes, got %v", resp3["count"])
	}
}

func TestHandleSearch_TagGovernance(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	idxDir := filepath.Join(dir, "personal-vault")
	os.MkdirAll(idxDir, 0755)
	meta := gleann.IndexMeta{
		Name:       "personal-vault",
		MCPExposed: &[]bool{true}[0],
		Tags:       []string{"personal"},
	}
	d, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(idxDir, "personal-vault.meta.json"), d, 0644)

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	os.Setenv("GLEANN_TAGS", "work,enterprise")
	defer os.Unsetenv("GLEANN_TAGS")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)

	req := httptest.NewRequest(http.MethodPost, "/api/indexes/personal-vault/search", strings.NewReader(`{"query":"my documents"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden under GLEANN_TAGS isolation, got %d", w.Code)
	}
}

