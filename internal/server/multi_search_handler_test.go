package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleMultiSearchBadRequest(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()

	s.handleMultiSearch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMultiSearchEmptyQuery(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	body, _ := json.Marshal(multiSearchRequest{Query: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleMultiSearch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMultiSearchNoIndexesDir(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	body, _ := json.Marshal(multiSearchRequest{Query: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleMultiSearch(w, req)

	// Empty dir → 0 results, still 200 OK.
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp multiSearchResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count != 0 {
		t.Errorf("expected 0 results, got %d", resp.Count)
	}
}

func createMockIndexMeta(t *testing.T, indexDir, name string, exposed bool, tags []string) {
	t.Helper()
	idxDir := filepath.Join(indexDir, name)
	if err := os.MkdirAll(idxDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	meta := gleann.IndexMeta{
		Name:       name,
		MCPExposed: &exposed,
		Tags:       tags,
	}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(idxDir, name+".meta.json"), data, 0644); err != nil {
		t.Fatalf("write meta failed: %v", err)
	}
}

func TestHandleMultiSearch_TargetIsolation(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	createMockIndexMeta(t, dir, "alpha", true, []string{"work"})
	createMockIndexMeta(t, dir, "beta", true, []string{"personal"})

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	// 1. Target alpha specified via Target field
	body, _ := json.Marshal(multiSearchRequest{
		Target: "alpha",
		Query:  "search query",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleMultiSearch(w, req)
	if w.Code == http.StatusForbidden {
		t.Fatalf("unexpected 403: %s", w.Body.String())
	}

	// 2. Header X-Gleann-Target: alpha trying to query beta -> Forbidden
	bodyBeta, _ := json.Marshal(multiSearchRequest{
		Target: "beta",
		Query:  "search query",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(bodyBeta))
	req2.Header.Set("X-Gleann-Target", "alpha")
	w2 := httptest.NewRecorder()
	s.handleMultiSearch(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when client target mismatch, got %d", w2.Code)
	}

	// 3. Header X-Gleann-Target: alpha with empty body -> targets alpha
	bodyEmpty, _ := json.Marshal(multiSearchRequest{
		Query: "search query",
	})
	req3 := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(bodyEmpty))
	req3.Header.Set("X-Gleann-Target", "alpha")
	w3 := httptest.NewRecorder()
	s.handleMultiSearch(w3, req3)
	if w3.Code == http.StatusForbidden {
		t.Errorf("unexpected forbidden for matching header target")
	}
}

func TestHandleMultiSearch_PrivateIndexIsolation(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	createMockIndexMeta(t, dir, "secret-vault", false, []string{"confidential"})

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	// 1. Explicitly targeting private index -> 403 Forbidden
	body, _ := json.Marshal(multiSearchRequest{
		Target: "secret-vault",
		Query:  "credentials",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleMultiSearch(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for private index target, got %d", w.Code)
	}

	// 2. Omitting target -> private index is filtered out completely, returns 200 with 0 results
	bodyAll, _ := json.Marshal(multiSearchRequest{
		Query: "credentials",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(bodyAll))
	w2 := httptest.NewRecorder()
	s.handleMultiSearch(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK with empty results, got %d", w2.Code)
	}
	var resp multiSearchResponse
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp.Count != 0 {
		t.Errorf("expected private index to be excluded, got %d results", resp.Count)
	}
}

func TestHandleMultiSearch_TagGovernance(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	createMockIndexMeta(t, dir, "work-repo", true, []string{"work", "golang"})
	createMockIndexMeta(t, dir, "personal-notes", true, []string{"personal"})

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	os.Setenv("GLEANN_TAGS", "work")
	defer os.Unsetenv("GLEANN_TAGS")

	// 1. Accessing personal-notes when GLEANN_TAGS=work -> 403 Forbidden
	body, _ := json.Marshal(multiSearchRequest{
		Target: "personal-notes",
		Query:  "vacation",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleMultiSearch(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for non-matching tag under GLEANN_TAGS, got %d", w.Code)
	}

	// 2. Querying with tag expansion @work -> expands work-repo
	bodyTag, _ := json.Marshal(multiSearchRequest{
		Target: "@work",
		Query:  "code",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(bodyTag))
	w2 := httptest.NewRecorder()
	s.handleMultiSearch(w2, req2)
	if w2.Code == http.StatusForbidden {
		t.Errorf("unexpected forbidden for matching @work tag: %s", w2.Body.String())
	}
}
