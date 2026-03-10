package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
