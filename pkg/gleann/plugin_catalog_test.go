package gleann

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchPluginCatalog(t *testing.T) {
	// Test fallback mechanism by giving an invalid URL
	originalURL := RegistryURL
	RegistryURL = "http://localhost:1" // guaranteed connection refused
	defer func() { RegistryURL = originalURL }()

	catalog := FetchPluginCatalog()
	if len(catalog) == 0 {
		t.Fatal("expected fallback catalog, got empty")
	}
	if catalog[0].Name != "gleann-plugin-docs" {
		t.Errorf("expected first fallback plugin to be gleann-plugin-docs, got %s", catalog[0].Name)
	}

	// Test successful fetch from mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name": "mock-plugin", "description": "A test plugin"}]`))
	}))
	defer ts.Close()

	RegistryURL = ts.URL
	catalog = FetchPluginCatalog()
	if len(catalog) != 1 {
		t.Fatalf("expected 1 plugin from mock server, got %d", len(catalog))
	}
	if catalog[0].Name != "mock-plugin" {
		t.Errorf("expected mock-plugin, got %s", catalog[0].Name)
	}
}
