// Sandbox-style E2E test: spin up a real packshttp server in front of the
// ekiyo crops-tr pack (using a copy in a tempdir) and exercise the full
// HTTP surface end-to-end.
package packshttp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/packs"
	"github.com/tevfik/gleann/internal/packs/packshttp"
)

const cropsManifest = `id: crops-tr
version: 1.0.0
schema_version: 1
locale: tr
title: Türkiye Ekim Takvimi
description: e2e fixture
content_files: [crops.yaml]
search:
  fields: [common_name_tr, common_name_en]
app_hints:
  - app_id: ekiyo
    auto_load: true
`

const cropsContent = `- id: tomato_beefsteak
  common_name_tr: Domates
  common_name_en: Tomato
- id: pepper_capia
  common_name_tr: Kapya Biber
  common_name_en: Capia Pepper
`

func writePack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "crops-tr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(cropsManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crops.yaml"), []byte(cropsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	reg := packs.New(writePack(t))
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	packshttp.New(reg).Mount(mux)
	return httptest.NewServer(mux)
}

func mustGetJSON(t *testing.T, url string, headers map[string]string) (int, http.Header, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	return res.StatusCode, res.Header, out
}

func TestE2E_FullDiscoveryAndFetch(t *testing.T) {
	srv := startServer(t)
	defer srv.Close()

	// 1. Discover.
	code, _, body := mustGetJSON(t, srv.URL+"/api/packs?app=ekiyo", nil)
	if code != 200 {
		t.Fatalf("list code=%d body=%v", code, body)
	}
	if c, _ := body["count"].(float64); c != 1 {
		t.Errorf("count=%v want 1", body["count"])
	}

	// 2. Manifest with ETag round-trip.
	code, hdr, _ := mustGetJSON(t, srv.URL+"/api/packs/crops-tr", nil)
	if code != 200 {
		t.Fatalf("manifest code=%d", code)
	}
	etag := hdr.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	code2, _, _ := mustGetJSON(t, srv.URL+"/api/packs/crops-tr", map[string]string{"If-None-Match": etag})
	if code2 != http.StatusNotModified {
		t.Errorf("304 expected, got %d", code2)
	}

	// 3. Bulk data.
	code, _, body = mustGetJSON(t, srv.URL+"/api/packs/crops-tr/data", nil)
	if code != 200 {
		t.Fatalf("data code=%d", code)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Errorf("items=%d want 2", len(items))
	}

	// 4. Single item.
	code, _, body = mustGetJSON(t, srv.URL+"/api/packs/crops-tr/items/pepper_capia", nil)
	if code != 200 || body["common_name_tr"] != "Kapya Biber" {
		t.Errorf("item: code=%d body=%v", code, body)
	}

	// 5. Search.
	code, _, body = mustGetJSON(t, srv.URL+"/api/packs/crops-tr/search?q=domates", nil)
	if code != 200 {
		t.Fatalf("search code=%d", code)
	}
	hits, _ := body["items"].([]any)
	if len(hits) != 1 {
		t.Errorf("search hits=%d want 1", len(hits))
	}

	// 6. 404 on missing pack.
	code, _, _ = mustGetJSON(t, srv.URL+"/api/packs/missing", nil)
	if code != 404 {
		t.Errorf("missing pack: %d", code)
	}
}
