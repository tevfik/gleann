// Package multimodal — Bug #6 PDF marker plugin behavioural tests.
package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// stubMarkerPlugin imitates gleann-plugin-marker: /health 200, /convert
// returns a fixed markdown body so the test can assert the call shape.
// It also exposes a counter so a test can verify the VLM is NOT called
// when MarkerOnly is set.
func stubMarkerPlugin(t *testing.T, markdown string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/convert":
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"markdown":` + jsonString(markdown) + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &calls
}

func jsonString(s string) string {
	// Naive escape: enough for ascii fixtures used in tests.
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// TestTryMarkerExtraction_RespectsEnvURL verifies the marker URL is
// resolved from GLEANN_MARKER_PLUGIN_URL and the response is parsed.
func TestTryMarkerExtraction_RespectsEnvURL(t *testing.T) {
	srv, _ := stubMarkerPlugin(t, "# Hello from marker")
	defer srv.Close()
	t.Setenv("GLEANN_MARKER_PLUGIN_URL", srv.URL)

	dir := t.TempDir()
	pdf := filepath.Join(dir, "doc.pdf")
	_ = os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644)

	pages := tryMarkerExtraction(pdf)
	if len(pages) == 0 || !strings.Contains(pages[1], "Hello from marker") {
		t.Fatalf("expected marker text on page 1, got %v", pages)
	}
}

// TestTryMarkerExtraction_NoServerBugSix returns nil silently.
func TestTryMarkerExtraction_NoServerBugSix(t *testing.T) {
	t.Setenv("GLEANN_MARKER_PLUGIN_URL", "http://127.0.0.1:1") // black hole
	dir := t.TempDir()
	pdf := filepath.Join(dir, "doc.pdf")
	_ = os.WriteFile(pdf, []byte("%PDF-1.4"), 0o644)
	if pages := tryMarkerExtraction(pdf); pages != nil {
		t.Errorf("want nil with no server, got %v", pages)
	}
}
