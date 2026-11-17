package multimodal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeTools installs shell-script stand-ins for external programs (pdftoppm,
// mutool, ffmpeg, ffprobe, exiftool) and makes them the only thing on PATH.
// Scripts may only use shell builtins. Skipped on Windows.
func fakeTools(t *testing.T, scripts map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake external tools are POSIX shell scripts")
	}
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// isolateCache keeps the content-hash cache of a test private.
func isolateCache(t *testing.T) {
	t.Helper()
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "")
}

// chatStub is a configurable Ollama /api/chat endpoint. reply picks the
// assistant text from the request (so a test can answer table and chart
// prompts differently); status overrides the HTTP status when non-zero.
type chatStub struct {
	srv    *httptest.Server
	mu     sync.Mutex
	prompt []string
	auth   []string
	calls  atomic.Int32
}

func newChatStub(t *testing.T, status int, reply func(prompt string) string) *chatStub {
	t.Helper()
	cs := &chatStub{}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.calls.Add(1)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string   `json:"content"`
				Images  []string `json:"images"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[0].Content
		}
		cs.mu.Lock()
		cs.prompt = append(cs.prompt, prompt)
		cs.auth = append(cs.auth, r.Header.Get("Authorization"))
		cs.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("backend says no"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply(prompt)},
		})
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

func (cs *chatStub) prompts() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return append([]string(nil), cs.prompt...)
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ── PDF pipeline ──────────────────────────────────────────────

// pdftoppm writes <prefix>-1.jpg and <prefix>-2.jpg; the prefix is its last
// argument, exactly as renderWithPdftoppm passes it.
const fakePdftoppm = `for a; do last=$a; done
printf img1 > "$last-1.jpg"
printf img2 > "$last-2.jpg"`

func TestAnalyzePDFRendersPagesAndExtractsTablesAndCharts(t *testing.T) {
	fakeTools(t, map[string]string{"pdftoppm": fakePdftoppm})
	stub := newChatStub(t, 0, func(prompt string) string {
		switch {
		case strings.Contains(prompt, "Focus ONLY on tables"):
			return "Revenue\n| Year | Total |\n| --- | --- |\n| 2024 | 42 |\n"
		case strings.Contains(prompt, "Focus ONLY on charts"):
			return "CHART_TYPE: bar\nCHART_TITLE: Sales\nDATA_POINTS:\n- Q1: 4.5\nLABELS: Q1, Q2\n---"
		}
		return "A page with a table and a bar chart."
	})
	pdf := writeFile(t, filepath.Join(t.TempDir(), "doc.pdf"), "%PDF-1.4")

	p := NewProcessor(stub.srv.URL, "vlm")
	an, err := p.AnalyzePDF(pdf, PDFVisionConfig{DPI: 0, MaxPages: 2})
	if err != nil {
		t.Fatal(err)
	}
	if an.TotalPages != 2 || len(an.Pages) != 2 || an.SourcePath != pdf {
		t.Fatalf("analysis = %+v", an)
	}
	pg := an.Pages[0]
	if pg.PageNum != 1 || !pg.HasTable || !pg.HasChart || pg.Error != nil {
		t.Fatalf("page 1 = %+v", pg)
	}
	if pg.Tables == nil || len(pg.Tables.Tables) != 1 || pg.Tables.Tables[0].Rows[0][1] != "42" {
		t.Errorf("tables not extracted: %+v", pg.Tables)
	}
	if pg.Charts == nil || len(pg.Charts.Charts) != 1 || pg.Charts.Charts[0].Title != "Sales" ||
		pg.Charts.Charts[0].DataPoints[0].Value != 4.5 {
		t.Errorf("charts not extracted: %+v", pg.Charts)
	}
	// 2 pages x (describe + tables + charts).
	if got := stub.calls.Load(); got != 6 {
		t.Errorf("VLM calls = %d, want 6", got)
	}
	// The page images are removed once the analysis is done.
	if _, err := os.Stat(pg.ImagePath); !os.IsNotExist(err) {
		t.Errorf("temporary page image kept: %v", err)
	}
}

func TestAnalyzePDFRecordsPerPageVLMErrors(t *testing.T) {
	fakeTools(t, map[string]string{"pdftoppm": fakePdftoppm})
	stub := newChatStub(t, 500, nil)
	pdf := writeFile(t, filepath.Join(t.TempDir(), "doc.pdf"), "%PDF")

	an, err := NewProcessor(stub.srv.URL, "vlm").AnalyzePDF(pdf, PDFVisionConfig{})
	if err != nil {
		t.Fatalf("a VLM failure must not fail the whole document: %v", err)
	}
	if len(an.Pages) != 2 {
		t.Fatalf("pages = %d", len(an.Pages))
	}
	for _, pg := range an.Pages {
		if pg.Error == nil || !strings.Contains(pg.Error.Error(), "500") || pg.Description != "" {
			t.Errorf("page %d = %+v", pg.PageNum, pg)
		}
	}
}

func TestAnalyzePDFMarkerOnlySkipsVLM(t *testing.T) {
	fakeTools(t, map[string]string{"pdftoppm": fakePdftoppm})
	marker, converts := stubMarkerPlugin(t, "# Title text from marker")
	defer marker.Close()
	t.Setenv("GLEANN_MARKER_PLUGIN_URL", marker.URL)
	stub := newChatStub(t, 0, func(string) string { return "vlm text" })
	pdf := writeFile(t, filepath.Join(t.TempDir(), "doc.pdf"), "%PDF")

	cfg := PDFVisionConfig{UseMarker: true, MarkerOnly: true}
	an, err := NewProcessor(stub.srv.URL, "vlm").AnalyzePDF(pdf, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if converts.Load() != 1 {
		t.Errorf("marker /convert called %d times", converts.Load())
	}
	// Marker publishes the whole document under page 1: that page skips the
	// VLM, page 2 (no marker text) still goes to it.
	if an.Pages[0].Description != "# Title text from marker" || an.Pages[0].MarkerText == "" {
		t.Errorf("page 1 = %+v", an.Pages[0])
	}
	if an.Pages[1].Description != "vlm text" {
		t.Errorf("page 2 = %+v", an.Pages[1])
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("VLM calls = %d, want 1 (page 2 only)", got)
	}
}

func TestAnalyzePDFWithoutRendererFails(t *testing.T) {
	fakeTools(t, nil) // empty PATH
	pdf := writeFile(t, filepath.Join(t.TempDir(), "doc.pdf"), "%PDF")
	_, err := NewProcessor("http://127.0.0.1:1", "vlm").AnalyzePDF(pdf, PDFVisionConfig{})
	if err == nil || !strings.Contains(err.Error(), "no PDF renderer found") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderPDFPagesBackends(t *testing.T) {
	t.Run("mutool fallback with page limit", func(t *testing.T) {
		// mutool: draw -o <dir>/page-%d.png -r DPI 1-N file. Record argv.
		argsFile := filepath.Join(t.TempDir(), "args")
		fakeTools(t, map[string]string{"mutool": `printf '%s ' "$@" > ` + argsFile + `
out=$3
dir=${out%/*}
printf p > "$dir/page-1.png"`})
		pages, err := renderPDFPages("/x/in.pdf", 72, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 || !strings.HasSuffix(pages[0], "page-1.png") {
			t.Fatalf("pages = %v", pages)
		}
		defer CleanupPDFPages([]PDFPageResult{{ImagePath: pages[0]}})
		args, _ := os.ReadFile(argsFile)
		for _, want := range []string{"draw", "-r 72", "1-3", "/x/in.pdf"} {
			if !strings.Contains(string(args), want) {
				t.Errorf("mutool args %q lack %q", args, want)
			}
		}
	})

	t.Run("pdftoppm preferred and limited", func(t *testing.T) {
		argsFile := filepath.Join(t.TempDir(), "args")
		fakeTools(t, map[string]string{
			"pdftoppm": `printf '%s ' "$@" > ` + argsFile + "\n" + fakePdftoppm,
			"mutool":   "exit 9",
		})
		pages, err := renderPDFPages("/x/in.pdf", 100, 2)
		if err != nil {
			t.Fatal(err)
		}
		defer CleanupPDFPages([]PDFPageResult{{ImagePath: pages[0]}})
		if len(pages) != 2 {
			t.Errorf("pages = %v", pages)
		}
		args, _ := os.ReadFile(argsFile)
		if !strings.Contains(string(args), "-r 100") || !strings.Contains(string(args), "-l 2") {
			t.Errorf("pdftoppm args = %q", args)
		}
	})

	t.Run("pdftoppm failure surfaces its output", func(t *testing.T) {
		fakeTools(t, map[string]string{"pdftoppm": `echo "bad pdf" >&2; exit 1`})
		_, err := renderPDFPages("/x/in.pdf", 100, 0)
		if err == nil || !strings.Contains(err.Error(), "pdftoppm") || !strings.Contains(err.Error(), "bad pdf") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("mutool failure surfaces its output", func(t *testing.T) {
		fakeTools(t, map[string]string{"mutool": `echo "cannot open" >&2; exit 1`})
		_, err := renderPDFPages("/x/in.pdf", 100, 0)
		if err == nil || !strings.Contains(err.Error(), "mutool") || !strings.Contains(err.Error(), "cannot open") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRenderPDFPageToBase64(t *testing.T) {
	t.Run("pdftoppm renders the requested page", func(t *testing.T) {
		argsFile := filepath.Join(t.TempDir(), "args")
		fakeTools(t, map[string]string{"pdftoppm": `printf '%s ' "$@" > ` + argsFile + "\n" + fakePdftoppm})
		got, err := RenderPDFPageToBase64("/x/in.pdf", 4, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got == "" {
			t.Fatal("empty image")
		}
		args, _ := os.ReadFile(argsFile)
		for _, want := range []string{"-r 150", "-f 4", "-l 4"} {
			if !strings.Contains(string(args), want) {
				t.Errorf("args %q lack %q", args, want)
			}
		}
	})

	t.Run("mutool fallback", func(t *testing.T) {
		fakeTools(t, map[string]string{"mutool": `out=$3
printf png > "${out%/*}/page-1.png"`})
		got, err := RenderPDFPageToBase64("/x/in.pdf", 1, 96)
		if err != nil || got == "" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("no renderer", func(t *testing.T) {
		fakeTools(t, nil)
		if _, err := RenderPDFPageToBase64("/x/in.pdf", 1, 96); err == nil || !strings.Contains(err.Error(), "no PDF renderer") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("renderer fails", func(t *testing.T) {
		fakeTools(t, map[string]string{"pdftoppm": `echo broken >&2; exit 2`})
		if _, err := RenderPDFPageToBase64("/x/in.pdf", 1, 96); err == nil || !strings.Contains(err.Error(), "pdf render failed") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("renderer produced nothing", func(t *testing.T) {
		fakeTools(t, map[string]string{"pdftoppm": `exit 0`})
		if _, err := RenderPDFPageToBase64("/x/in.pdf", 99, 96); err == nil || !strings.Contains(err.Error(), "failed to collect") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestQueryOllamaResponses(t *testing.T) {
	t.Run("success sends model, prompt, image and key", func(t *testing.T) {
		stub := newChatStub(t, 0, func(string) string { return "answer" })
		p := NewProcessor(stub.srv.URL, "m")
		p.APIKey = "secret"
		got, err := p.queryOllama("the prompt", "QkFTRTY0")
		if err != nil || got != "answer" {
			t.Fatalf("got %q, %v", got, err)
		}
		if pr := stub.prompts(); len(pr) != 1 || pr[0] != "the prompt" {
			t.Errorf("prompts = %v", pr)
		}
		if stub.auth[0] != "Bearer secret" {
			t.Errorf("auth = %q", stub.auth[0])
		}
	})
	t.Run("non-200", func(t *testing.T) {
		stub := newChatStub(t, 503, nil)
		_, err := NewProcessor(stub.srv.URL, "m").queryOllama("p", "i")
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("malformed body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html>not json"))
		}))
		defer srv.Close()
		_, err := NewProcessor(srv.URL, "m").queryOllama("p", "i")
		if err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		_, err := NewProcessor(url, "m").queryOllama("p", "i")
		if err == nil || !strings.Contains(err.Error(), "ollama request") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestExtractTablesAndChartsErrorPaths(t *testing.T) {
	img := writeFile(t, filepath.Join(t.TempDir(), "p.jpg"), "jpeg")
	missing := filepath.Join(t.TempDir(), "absent.jpg")
	failing := newChatStub(t, 500, nil)
	empty := newChatStub(t, 0, func(string) string { return "NO_TABLES_FOUND NO_CHARTS_FOUND" })

	p := NewProcessor(failing.srv.URL, "m")
	if _, err := p.ExtractTables(missing, 1); err == nil || !strings.Contains(err.Error(), "read image") {
		t.Errorf("tables missing image: %v", err)
	}
	if _, err := p.ExtractCharts(missing, 1); err == nil || !strings.Contains(err.Error(), "read image") {
		t.Errorf("charts missing image: %v", err)
	}
	if _, err := p.ExtractTables(img, 1); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("tables VLM error: %v", err)
	}
	if _, err := p.ExtractCharts(img, 1); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("charts VLM error: %v", err)
	}

	p = NewProcessor(empty.srv.URL, "m")
	tr, err := p.ExtractTables(img, 7)
	if err != nil || tr.PageNum != 7 || len(tr.Tables) != 0 {
		t.Errorf("tables = %+v, %v", tr, err)
	}
	cr, err := p.ExtractCharts(img, 7)
	if err != nil || cr.PageNum != 7 || len(cr.Charts) != 0 {
		t.Errorf("charts = %+v, %v", cr, err)
	}
}
