package packshttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/packs"
)

func TestDataNotFoundAndNotModified(t *testing.T) {
	mux := http.NewServeMux()
	reg := setupTestRegistry(t)
	New(reg).Mount(mux)

	if rec := mustGet(t, mux, "/api/packs/nope/data"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown pack data: %d", rec.Code)
	}
	p, err := reg.Get("crops-tr")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/packs/crops-tr/data", nil)
	req.Header.Set("If-None-Match", p.ETag)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("matching ETag: %d %q", rec.Code, rec.Body.String())
	}
}

func TestSearchLimitAndNotFound(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)

	tests := []struct {
		target string
		count  int
	}{
		{"/api/packs/crops-tr/search?n=1", 1},
		{"/api/packs/crops-tr/search?n=0", 2},    // out of range: default
		{"/api/packs/crops-tr/search?n=9999", 2}, // out of range: default
		{"/api/packs/crops-tr/search?n=abc", 2},  // not a number: default
	}
	for _, tc := range tests {
		rec := mustGet(t, mux, tc.target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.target, rec.Code)
		}
		var body struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Count != tc.count {
			t.Errorf("%s: count %d, want %d", tc.target, body.Count, tc.count)
		}
	}
	if rec := mustGet(t, mux, "/api/packs/nope/search?q=x"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown pack search: %d", rec.Code)
	}
}

func TestReloadEndpoint(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "pack.yaml"), []byte("id: demo\nversion: \"1\"\ncontent_files: [items.yaml]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "items.yaml"), []byte("- id: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(packs.New(dir)).Mount(mux)

	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/packs/reload", nil))
		return rec
	}
	rec := post()
	if rec.Code != http.StatusOK {
		t.Fatalf("reload: %d %s", rec.Code, rec.Body.String())
	}
	var ok struct {
		Reloaded bool `json:"reloaded"`
		Count    int  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil || !ok.Reloaded || ok.Count != 1 {
		t.Fatalf("reload body %s (%v)", rec.Body.String(), err)
	}

	// A pack directory without pack.yaml makes reload answer 500 with the
	// reason.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec = post()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reload with a broken pack: %d %s", rec.Code, rec.Body.String())
	}
	var bad map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &bad); err != nil || bad["error"] == "" {
		t.Fatalf("reload error body %s", rec.Body.String())
	}
}

func TestRespondNotFoundOtherError(t *testing.T) {
	rec := httptest.NewRecorder()
	if !respondNotFound(rec, errors.New("boom")) {
		t.Fatal("an error must be handled")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "boom" {
		t.Fatalf("body %s", rec.Body.String())
	}
}
