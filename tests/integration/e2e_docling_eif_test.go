package integration

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// These tests drive the real gleann-plugin-docs (Go/C, in-process EIF) through
// gleann's PluginManager, using the registered plugin from ~/.gleann. They skip
// when the plugin or its model is not installed. Run them with an isolated HOME
// that exposes plugins.json and the models directory.

func doclingPlugin(t *testing.T, ext string) (*gleann.PluginManager, *gleann.Plugin) {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin daemon tests skipped in short mode")
	}
	pm, err := gleann.NewPluginManager()
	if err != nil {
		t.Skipf("no plugin manager: %v", err)
	}
	t.Cleanup(func() { pm.Close() })
	plugin := pm.FindDocumentExtractor(ext)
	if plugin == nil {
		t.Skipf("no document-extraction plugin registered for %s", ext)
	}
	if err := pm.EnsurePluginRunning(plugin); err != nil {
		t.Skipf("plugin not running: %v", err)
	}
	resp, err := http.Get(plugin.URL + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer resp.Body.Close()
	var h struct {
		Status   string          `json:"status"`
		Backends map[string]bool `json:"backends"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("parse health: %v", err)
	}
	if h.Status != "ok" {
		t.Fatalf("health status = %q", h.Status)
	}
	if !h.Backends["docling-eif"] && !h.Backends["docling"] {
		t.Skipf("Docling backend not loaded (model missing?): %v", h.Backends)
	}
	return pm, plugin
}

func nodeText(r *gleann.PluginResult) string {
	var b strings.Builder
	b.WriteString(r.Markdown)
	for _, n := range r.Nodes {
		for _, v := range n.Data {
			if s, ok := v.(string); ok {
				b.WriteString(" ")
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// Every office/PDF/CSV fixture must convert to graph nodes that contain its
// known content, and report a Document plus at least one Section.
func TestE2E_DoclingPlugin_FixtureFormats(t *testing.T) {
	fixtures := filepath.Join("..", "e2e", "fixtures", "binary")
	cases := []struct {
		file string
		want []string
	}{
		{"benchmark_report.pdf", []string{"Gleann Document Extraction"}},
		{"technical_report.docx", []string{"HNSW", "FAISS"}},
		{"benchmark_data.xlsx", []string{"Model", "Dimensions"}},
		{"research_slides.pptx", []string{"Knowledge Graph"}},
		{"benchmarks.csv", []string{"benchmark_name", "recall"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			pm, plugin := doclingPlugin(t, filepath.Ext(tc.file))
			res, err := pm.ProcessStructured(plugin, filepath.Join(fixtures, tc.file))
			if err != nil {
				t.Fatalf("ProcessStructured: %v", err)
			}
			var docs, sections int
			for _, n := range res.Nodes {
				switch n.Type {
				case "Document":
					docs++
				case "Section":
					sections++
				}
			}
			if docs != 1 || sections == 0 {
				t.Fatalf("want 1 Document and >=1 Section, got %d/%d", docs, sections)
			}
			if len(res.Edges) == 0 {
				t.Error("expected HAS_SECTION edges")
			}
			text := nodeText(res)
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("extracted text is missing %q", w)
				}
			}
		})
	}
}

// A scanned (image-only) PDF has no text layer, so it is the case that needs
// the Docling VLM. gleann routes plain images to --multimodal-model instead of
// the plugin, so scanned PDFs are the only way the VLM is reached from the
// indexing pipeline. The page title must be recovered; the body of the page is
// deliberately not asserted (see the log line for what the model returns).
func TestE2E_DoclingPlugin_ScannedPDFUsesVLM(t *testing.T) {
	pm, plugin := doclingPlugin(t, ".pdf")
	start := time.Now()
	res, err := pm.ProcessStructured(plugin, filepath.Join("..", "e2e", "fixtures", "binary", "scanned_report.pdf"))
	if err != nil {
		t.Fatalf("ProcessStructured: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("VLM extraction took %v, markdown=%q", elapsed, res.Markdown)
	if !strings.Contains(strings.ToLower(nodeText(res)), "gleann document extraction") {
		t.Errorf("title not recovered from scanned PDF; markdown=%q", res.Markdown)
	}
	if elapsed > 120*time.Second {
		t.Errorf("VLM extraction too slow: %v", elapsed)
	}
}
