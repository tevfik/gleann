package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestReviewBuildRequestBackendHonored(t *testing.T) {
	_, h, cfg := newIndexedServer(t)

	// Build a new index with explicit backend "diskann"
	body, _ := json.Marshal(map[string]any{
		"texts":   []string{"one", "two", "three"},
		"backend": "diskann",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/diskann-idx/build", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("build request failed: %d %s", w.Code, w.Body.String())
	}

	meta, err := gleann.GetIndexMeta(cfg.IndexDir, "diskann-idx")
	if err != nil {
		t.Fatalf("GetIndexMeta: %v", err)
	}
	if meta.Backend != "diskann" {
		t.Errorf("index built with request backend=diskann has meta.Backend=%q, want diskann", meta.Backend)
	}
}

func TestReviewBuildRequestUnknownBackend(t *testing.T) {
	_, h, _ := newIndexedServer(t)

	body, _ := json.Marshal(map[string]any{
		"texts":   []string{"one", "two"},
		"backend": "nonexistent-backend-xyz",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/invalid-backend-idx/build", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for unknown backend, got %d %s", w.Code, w.Body.String())
	}
}
