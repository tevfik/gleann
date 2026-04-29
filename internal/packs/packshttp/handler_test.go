package packshttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/packs"
)

func setupTestRegistry(t *testing.T) *packs.Registry {
	t.Helper()
	dir := t.TempDir()
	pdir := filepath.Join(dir, "crops-tr")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "pack.yaml"), []byte(
		`id: crops-tr
version: 1.0.0
schema_version: 1
locale: tr
title: Test
description: ""
content_files: [crops.yaml]
search:
  fields: [name_tr]
app_hints:
  - app_id: ekiyo
    auto_load: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "crops.yaml"), []byte(
		`- id: tomato
  name_tr: Domates
- id: pepper
  name_tr: Biber
`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := packs.New(dir)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	return r
}

func mustGet(t *testing.T, mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestList(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)

	rec := mustGet(t, mux, "/api/packs")
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "crops-tr") {
		t.Errorf("body missing pack id: %s", rec.Body.String())
	}

	// app filter hits.
	rec = mustGet(t, mux, "/api/packs?app=ekiyo")
	if rec.Code != 200 || !contains(rec.Body.String(), "crops-tr") {
		t.Errorf("ekiyo filter failed: %s", rec.Body.String())
	}
	// app filter misses.
	rec = mustGet(t, mux, "/api/packs?app=other")
	if rec.Code != 200 || contains(rec.Body.String(), "crops-tr") {
		t.Errorf("other filter should be empty: %s", rec.Body.String())
	}
}

func TestManifestAndETag(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)

	rec := mustGet(t, mux, "/api/packs/crops-tr")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	// 304 round-trip
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/packs/crops-tr", nil)
	req.Header.Set("If-None-Match", etag)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("want 304, got %d", rec.Code)
	}
}

func TestData(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)
	rec := mustGet(t, mux, "/api/packs/crops-tr/data")
	if rec.Code != 200 || !contains(rec.Body.String(), "Domates") {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestItem(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)
	rec := mustGet(t, mux, "/api/packs/crops-tr/items/tomato")
	if rec.Code != 200 || !contains(rec.Body.String(), "Domates") {
		t.Fatalf("body: %s", rec.Body.String())
	}
	rec = mustGet(t, mux, "/api/packs/crops-tr/items/missing")
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestSearch(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)
	rec := mustGet(t, mux, "/api/packs/crops-tr/search?q=biber")
	if rec.Code != 200 || !contains(rec.Body.String(), "Biber") {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestNotFound(t *testing.T) {
	mux := http.NewServeMux()
	New(setupTestRegistry(t)).Mount(mux)
	rec := mustGet(t, mux, "/api/packs/missing")
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
