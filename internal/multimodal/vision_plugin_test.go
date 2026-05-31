// Package multimodal — Bug #4/#7/#11 + Layer B/C vision plugin tests.
package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubVisionPlugin imitates the v2 schema: /health 200, /convert returns
// CLIP+OCR+entities fixtures so the wiring can be checked end-to-end.
func stubVisionPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(200)
		case "/convert":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
              "schema_version": 2,
              "markdown": "stub-md",
              "ocr_text": "INVOICE 2026",
              "clip_embedding": [0.1, 0.2, 0.3],
              "entities": [
                {"type":"Object","label":"laptop","confidence":0.94,"bbox":[0,0,1,1]}
              ],
              "exif": {"DateTimeOriginal":"2026:05:31 12:00:00"},
              "width": 32, "height": 24,
              "capabilities": ["clip","ocr","entities"]
            }`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestCallVisionPlugin_ParsesSchemaV2 covers happy-path JSON decoding.
func TestCallVisionPlugin_ParsesSchemaV2(t *testing.T) {
	srv := stubVisionPlugin(t)
	defer srv.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "x.png")
	_ = os.WriteFile(p, []byte("img"), 0o644)

	vr, err := CallVisionPlugin(srv.URL, p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if vr.SchemaVersion != 2 {
		t.Errorf("schema_version=%d, want 2", vr.SchemaVersion)
	}
	if len(vr.CLIPEmbedding) != 3 || vr.CLIPEmbedding[0] != 0.1 {
		t.Errorf("CLIP embedding malformed: %v", vr.CLIPEmbedding)
	}
	if len(vr.Entities) != 1 || vr.Entities[0].Label != "laptop" {
		t.Errorf("entities malformed: %v", vr.Entities)
	}
	if vr.OCRText != "INVOICE 2026" {
		t.Errorf("ocr_text=%q", vr.OCRText)
	}
}

// TestVisionPluginHealthy_OK + missing endpoint paths.
func TestVisionPluginHealthy(t *testing.T) {
	srv := stubVisionPlugin(t)
	defer srv.Close()
	if !VisionPluginHealthy(srv.URL) {
		t.Error("expected healthy")
	}
	if VisionPluginHealthy("") {
		t.Error("empty url should be unhealthy")
	}
	if VisionPluginHealthy("http://127.0.0.1:1") {
		t.Error("unreachable url should be unhealthy")
	}
}

// TestProcessDirectory_EnrichesWithVisionPlugin verifies that with the
// vision plugin URL configured, ProcessDirectory's worker pool populates
// the new IndexableItem fields (CLIP, OCR, entities) and appends the
// OCR text to the Text field for BM25.
func TestProcessDirectory_EnrichesWithVisionPlugin(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "2")

	ollama, _, _ := stubOllama(t, 5*time.Millisecond)
	defer ollama.Close()
	vision := stubVisionPlugin(t)
	defer vision.Close()
	t.Setenv("GLEANN_VISION_PLUGIN_URL", vision.URL)

	dir := t.TempDir()
	makePNG(t, filepath.Join(dir, "a.png"), 32, 24)
	makePNG(t, filepath.Join(dir, "b.png"), 32, 24)

	proc := NewProcessor(ollama.URL, "stub-model")
	items, err := proc.ProcessDirectory(dir, nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	for _, it := range items {
		if len(it.CLIPEmbedding) != 3 {
			t.Errorf("%s: missing CLIP embedding", it.Source)
		}
		if it.OCRText == "" {
			t.Errorf("%s: missing OCR text", it.Source)
		}
		if !strings.Contains(it.Text, "INVOICE 2026") {
			t.Errorf("%s: text missing OCR append: %q", it.Source, it.Text)
		}
		if len(it.Entities) != 1 {
			t.Errorf("%s: missing entities", it.Source)
		}
		if it.Metadata == nil || it.Metadata.Width != 32 {
			t.Errorf("%s: metadata not populated", it.Source)
		}
	}
}

// TestVisionPluginURL_Off lets users keep the plugin installed but opt
// out via env without removing the registry entry.
func TestVisionPluginURL_Off(t *testing.T) {
	t.Setenv("GLEANN_VISION_PLUGIN_URL", "off")
	if VisionPluginURL() != "" {
		t.Error("'off' should disable the plugin")
	}
	t.Setenv("GLEANN_VISION_PLUGIN_URL", "http://x")
	if VisionPluginURL() != "http://x" {
		t.Error("env should propagate")
	}
}
