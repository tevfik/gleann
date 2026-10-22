package indexing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/internal/multimodal"
)

func TestVisionPDFEnabled(t *testing.T) {
	if visionPDFEnabled(nil) {
		t.Fatal("nil processor must not claim PDFs")
	}
	if visionPDFEnabled(&multimodal.Processor{}) {
		t.Fatal("a processor without a model cannot analyse PDFs")
	}
	if visionPDFEnabled(&multimodal.Processor{PluginOnly: true}) {
		t.Fatal("an audio-only processor cannot analyse PDFs")
	}
	if !visionPDFEnabled(&multimodal.Processor{Model: "some-vlm"}) {
		t.Fatal("a processor with a model reads PDFs through vision")
	}
}

// fakeDocPlugin registers a document-extraction plugin for .pdf under a
// temporary HOME and counts how many files it converts.
func fakeDocPlugin(t *testing.T) *int32 {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/convert":
			atomic.AddInt32(&calls, 1)
			json.NewEncoder(w).Encode(map[string]any{
				"markdown": "# Report\n\nThe plugin-extracted sentence about quetta prefixes.",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	reg := map[string]any{"plugins": []map[string]any{{
		"name": "fake-docs", "url": srv.URL, "priority": 10,
		"capabilities": []string{"document-extraction"}, "extensions": []string{".pdf"},
	}}}
	data, _ := json.Marshal(reg)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return &calls
}

func readText(t *testing.T, dir string, ro ReadOptions) string {
	t.Helper()
	items, _, err := ReadDocuments(context.Background(), dir, ro)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, it := range items {
		sb.WriteString(it.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}

// A multimodal processor that has no model (the default whenever an audio
// plugin is registered) must not keep a working document plugin from reading
// PDFs.
func TestReadDocuments_PDFUsesPluginWhenVisionHasNoModel(t *testing.T) {
	calls := fakeDocPlugin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, mm := range map[string]*multimodal.Processor{
		"no processor":      nil,
		"empty model":       {},
		"audio-only plugin": {PluginOnly: true},
	} {
		before := atomic.LoadInt32(calls)
		got := readText(t, dir, ReadOptions{Mode: ModeDocs, Multimodal: mm})
		if !strings.Contains(got, "plugin-extracted sentence") {
			t.Errorf("%s: PDF was not read by the plugin, got %q", name, got)
		}
		if atomic.LoadInt32(calls) != before+1 {
			t.Errorf("%s: plugin should have converted the PDF once", name)
		}
	}
}

func TestReadDocuments_NoPluginsFlagSkipsPlugin(t *testing.T) {
	calls := fakeDocPlugin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readText(t, dir, ReadOptions{Mode: ModeDocs, NoPlugins: true})
	if strings.Contains(got, "plugin-extracted sentence") || atomic.LoadInt32(calls) != 0 {
		t.Fatalf("--no-plugins must not call the plugin, got %q", got)
	}
}
