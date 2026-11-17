package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// pluginReply is what the fake plugin answers for an uploaded file name.
type pluginReply func(filename string) (status int, body map[string]any)

// structuredReply returns a plugin result with a document, two nested
// sections and a chunk, keyed by the uploaded name: "fail.*" makes the
// plugin fail, "raw.*" returns only markdown.
func structuredReply(filename string) (int, map[string]any) {
	switch {
	case strings.HasPrefix(filename, "fail"):
		return http.StatusInternalServerError, map[string]any{"detail": "converter crashed"}
	case strings.HasPrefix(filename, "logic"):
		return http.StatusOK, map[string]any{"error": "unsupported encryption"}
	case strings.HasPrefix(filename, "raw"):
		return http.StatusOK, map[string]any{"markdown": "# Raw Title\n\nThe raw markdown body mentions zephyrine."}
	}
	return http.StatusOK, map[string]any{
		"nodes": []map[string]any{
			{"_type": "Document", "title": "Quarterly", "format": "docx", "summary": "about sales", "word_count": 42.0, "page_count": 3.0},
			{"_type": "Section", "id": "s1", "heading": "Overview", "level": 1.0, "content": "Overview of the obsidianite market."},
			{"_type": "Section", "id": "s2", "heading": "Details", "level": 2.0, "content": "Detailed numbers for obsidianite."},
		},
		"edges": []map[string]any{
			{"_type": "HAS_SECTION", "from": "doc", "to": "s1"},
			{"_type": "HAS_SUBSECTION", "from": "s1", "to": "s2"},
		},
	}
}

// startPlugin serves /health and /convert and registers the server under a
// temporary HOME as a plugin with the given capability and extensions.
func startPlugin(t *testing.T, capability string, exts []string, reply pluginReply) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/convert":
			calls.Add(1)
			name := ""
			if _, hdr, err := r.FormFile("file"); err == nil {
				name = hdr.Filename
			}
			status, body := reply(name)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	registerPlugins(t, map[string]any{
		"name": "fake-" + capability, "url": srv.URL, "priority": 10,
		"capabilities": []string{capability}, "extensions": exts,
	})
	return srv, &calls
}

func registerPlugins(t *testing.T, plugins ...map[string]any) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	data, _ := json.Marshal(map[string]any{"plugins": plugins})
	writeFile(t, filepath.Join(home, ".gleann", "plugins.json"), string(data))
}

// fakeVision is an Ollama endpoint for a vision model: /api/show reports a
// clip family and /api/chat describes whatever image it is sent. With fail
// set every chat request fails.
func fakeVision(t *testing.T, fail bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]any{"families": []string{"clip"}}})
		case "/api/chat":
			if fail {
				http.Error(w, "model crashed", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "A diagram of the quokka pipeline."}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	return srv
}

// bySource groups item texts by their source metadata.
func bySource(items []gleann.Item) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		src, _ := it.Metadata["source"].(string)
		out[src] += it.Text + "\n"
	}
	return out
}

func extractorOf(items []gleann.Item, source string) string {
	for _, it := range items {
		if it.Metadata["source"] == source {
			e, _ := it.Metadata["extractor"].(string)
			return e
		}
	}
	return ""
}

func docFor(docs []*PluginDoc, source string) *PluginDoc {
	for _, d := range docs {
		if d.SourcePath == source {
			return d
		}
	}
	return nil
}

// A document plugin's structured result becomes section-aware chunks and a
// graph document; a failing plugin falls back to the native extractor; a
// result with only markdown is chunked by headings.
func TestReadDocumentsPluginResults(t *testing.T) {
	_, calls := startPlugin(t, gleann.CapDocumentExtraction, []string{".docx", ".csv"}, structuredReply)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "report.docx"), "PK fake docx")
	writeFile(t, filepath.Join(dir, "raw.docx"), "PK fake docx")
	writeFile(t, filepath.Join(dir, "fail.csv"), "name,mineral\nann,fluorite\n")
	writeFile(t, filepath.Join(dir, "logic.docx"), "PK broken docx")
	var errBuf bytes.Buffer

	items, docs, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, ChunkOverlap: 16, Stderr: &errBuf})
	if err != nil {
		t.Fatal(err)
	}
	got := bySource(items)
	if !strings.Contains(got["report.docx"], "obsidianite market") || !strings.Contains(got["report.docx"], "Detailed numbers") {
		t.Errorf("structured sections not chunked: %q", got["report.docx"])
	}
	if !strings.Contains(got["raw.docx"], "zephyrine") {
		t.Errorf("markdown-only plugin result not chunked: %q", got["raw.docx"])
	}
	// The failing plugin is reported and the native CSV reader takes over.
	if !strings.Contains(got["fail.csv"], "fluorite") || extractorOf(items, "fail.csv") != "native" {
		t.Errorf("native fallback after a plugin failure: %q (extractor %q)", got["fail.csv"], extractorOf(items, "fail.csv"))
	}
	if !strings.Contains(errBuf.String(), "failed to extract fail.csv") || !strings.Contains(errBuf.String(), "failed to extract logic.docx") {
		t.Errorf("plugin failures not reported: %q", errBuf.String())
	}
	if d := docFor(docs, "report.docx"); d == nil || len(d.Result.Nodes) != 3 {
		t.Errorf("graph document of the plugin result missing: %+v", d)
	}
	if calls.Load() != 4 {
		t.Errorf("plugin called %d times, want once per plugin file (4)", calls.Load())
	}

	// The incremental reader takes the same paths.
	files := []string{filepath.Join(dir, "report.docx"), filepath.Join(dir, "raw.docx"), filepath.Join(dir, "fail.csv")}
	items, docs, records, err := readDocumentsForFiles(context.Background(), dir, files, ReadOptions{ChunkSize: 256, ChunkOverlap: 16}, newPrinter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	got = bySource(items)
	if !strings.Contains(got["report.docx"], "obsidianite") || !strings.Contains(got["raw.docx"], "zephyrine") || !strings.Contains(got["fail.csv"], "fluorite") {
		t.Errorf("incremental read: %v", got)
	}
	if docFor(docs, "report.docx") == nil || docFor(docs, "fail.csv") == nil {
		t.Errorf("incremental graph documents: %d", len(docs))
	}
	if len(records) != 3 {
		t.Errorf("incremental read produced %d tracker records, want 3", len(records))
	}
}

// Images go to the vision model; its description is the passage. A model
// failure is a warning and the file gets only a tracker record.
func TestReadDocumentsMultimodalImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ok := fakeVision(t, false)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "diagram.png"), "\x89PNG\r\n\x1a\nnot really")
	writeFile(t, filepath.Join(dir, "notes.txt"), "Plain notes about the pipeline.")

	mm := &multimodal.Processor{OllamaHost: ok.URL, Model: "vlm", Lang: "en"}
	var out bytes.Buffer
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, Multimodal: mm, NoPlugins: true, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(items)["diagram.png"]; !strings.Contains(got, "quokka pipeline") {
		t.Fatalf("image description missing: %v", bySource(items))
	}
	for _, it := range items {
		if it.Metadata["source"] == "diagram.png" && it.Metadata["media_type"] != "image" {
			t.Errorf("media_type = %v", it.Metadata["media_type"])
		}
	}
	if !strings.Contains(out.String(), "Multimodal: diagram.png") {
		t.Errorf("progress line missing: %q", out.String())
	}

	// The incremental reader describes the image the same way.
	items, _, records, err := readDocumentsForFiles(context.Background(), dir, []string{filepath.Join(dir, "diagram.png")}, ReadOptions{Multimodal: mm, NoPlugins: true}, newPrinter(nil, nil))
	if err != nil || !strings.Contains(bySource(items)["diagram.png"], "quokka") || len(records) != 1 {
		t.Fatalf("incremental image read: %v %v %d", bySource(items), err, len(records))
	}

	bad := fakeVision(t, true)
	mmBad := &multimodal.Processor{OllamaHost: bad.URL, Model: "vlm2", Lang: "en"}
	var errBuf bytes.Buffer
	items, _, err = ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, Multimodal: mmBad, NoPlugins: true, Stderr: &errBuf})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := bySource(items)["diagram.png"]; has {
		t.Error("a failed description must not produce a passage")
	}
	if !strings.Contains(errBuf.String(), "multimodal processing failed for diagram.png") {
		t.Errorf("model failure not reported: %q", errBuf.String())
	}
	items, _, records, err = readDocumentsForFiles(context.Background(), dir, []string{filepath.Join(dir, "diagram.png")}, ReadOptions{Multimodal: mmBad, NoPlugins: true}, newPrinter(nil, nil))
	if err != nil || len(items) != 0 || len(records) != 1 {
		t.Fatalf("incremental failed image: %d items, %d records, %v", len(items), len(records), err)
	}
}

// Audio files are transcribed by the registered transcription plugin, which
// is health-checked first; when it is down the failure is reported and the
// file gets no passage.
func TestReadDocumentsAudioPlugin(t *testing.T) {
	srv, calls := startPlugin(t, gleann.CapAudioTranscription, nil, func(string) (int, map[string]any) {
		return http.StatusOK, map[string]any{"markdown": "Transcript: the heron sings at dawn."}
	})
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", srv.URL)
	t.Setenv("GLEANN_MULTIMODAL_MODEL", "")
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memo.mp3"), "ID3 fake audio")

	var out bytes.Buffer
	mm := NewMultimodalProcessor("", "", "", newPrinter(&out, nil).Printf)
	if mm == nil || !mm.PluginOnly {
		t.Fatalf("processor = %+v, want the plugin-only processor", mm)
	}
	if !strings.Contains(out.String(), "Audio indexing via transcription plugin") {
		t.Errorf("capability line missing: %q", out.String())
	}
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{Multimodal: mm})
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(items)["memo.mp3"]; !strings.Contains(got, "heron sings") {
		t.Fatalf("transcript missing: %v", bySource(items))
	}
	for _, it := range items {
		if it.Metadata["source"] == "memo.mp3" && it.Metadata["media_type"] != "audio" {
			t.Errorf("media_type = %v", it.Metadata["media_type"])
		}
	}
	if calls.Load() != 1 {
		t.Errorf("plugin transcribed %d times", calls.Load())
	}

	// The plugin goes down (and has no command to start it with).
	srv.Close()
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	var errBuf bytes.Buffer
	items, _, err = ReadDocuments(context.Background(), dir, ReadOptions{Multimodal: mm, Stderr: &errBuf})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("a failed transcription produced %d passages", len(items))
	}
	if !strings.Contains(errBuf.String(), "audio plugin") || !strings.Contains(errBuf.String(), "is not running") {
		t.Errorf("down plugin not reported: %q", errBuf.String())
	}
}

// A PDF the vision model cannot analyse (no renderer, or a broken file) falls
// back to the native extractor, which reports a broken PDF and skips it.
func TestReadDocumentsVisionPDFFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vis := fakeVision(t, false)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.pdf"), "%PDF-1.4\nnot a real pdf\n%%EOF\n")
	mm := &multimodal.Processor{OllamaHost: vis.URL, Model: "vlm", Lang: "en"}
	var errBuf bytes.Buffer
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{Multimodal: mm, NoPlugins: true, Stderr: &errBuf})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("broken PDF produced passages: %v", bySource(items))
	}
	if !strings.Contains(errBuf.String(), "vision extraction failed for broken.pdf") {
		t.Errorf("vision failure not reported: %q", errBuf.String())
	}
	errBuf.Reset()
	items, _, records, err := readDocumentsForFiles(context.Background(), dir, []string{filepath.Join(dir, "broken.pdf")}, ReadOptions{Multimodal: mm, NoPlugins: true}, newPrinter(nil, &errBuf))
	if err != nil || len(items) != 0 || len(records) != 1 {
		t.Fatalf("incremental broken PDF: %d items, %d records, %v", len(items), len(records), err)
	}
	if !strings.Contains(errBuf.String(), "incremental vision extraction failed") {
		t.Errorf("incremental vision failure not reported: %q", errBuf.String())
	}
}

// Text reads: markdown variants with and without headings, native formats
// whose markdown has no headings, empty files, whitespace-only files, binary
// content behind a text extension and files that vanish before the read.
func TestReadDocumentsTextVariants(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "guide.mdx"), "# Guide\n\nThe guide explains the tarnwick valve.\n\n## Setup\n\nTurn it clockwise.\n")
	writeFile(t, filepath.Join(dir, "flat.markdown"), "no headings here, just the plover sentence\n")
	writeFile(t, filepath.Join(dir, "page.html"), "<html><body><p>The html paragraph about lindworms.</p></body></html>")
	writeFile(t, filepath.Join(dir, "blank.txt"), "   \n\t\n")
	writeFile(t, filepath.Join(dir, "config.unknownext"), "key = value\nother = 2\n")

	items, docs, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	got := bySource(items)
	if !strings.Contains(got["guide.mdx"], "tarnwick") {
		t.Errorf("mdx not read: %v", got)
	}
	if d := docFor(docs, "guide.mdx"); d == nil || PluginResultToDoc(d.Result).Document.Title != "Guide" {
		t.Errorf("mdx graph document: %+v", d)
	}
	if !strings.Contains(got["flat.markdown"], "plover") {
		t.Errorf("markdown without headings must fall back to sentence chunks: %v", got)
	}
	if !strings.Contains(got["page.html"], "lindworms") || extractorOf(items, "page.html") != "native" {
		t.Errorf("html via the native extractor: %v", got)
	}
	if _, has := got["blank.txt"]; has {
		t.Error("a whitespace-only file produced a passage")
	}

	// The incremental reader over the same files, plus a binary file behind
	// a text extension, an empty text file and a file that no longer exists.
	writeFile(t, filepath.Join(dir, "packed.go"), "package x\x00\x01\x02")
	writeFile(t, filepath.Join(dir, "empty.go"), "  \n")
	writeFile(t, filepath.Join(dir, "plain.py"), "def greet():\n    return 'hi'\n")
	files := []string{"guide.mdx", "flat.markdown", "page.html", "blank.txt", "packed.go", "empty.go", "plain.py", "gone.go", "config.unknownext"}
	for i, f := range files {
		files[i] = filepath.Join(dir, f)
	}
	items, docs, records, err := readDocumentsForFiles(context.Background(), dir, files, ReadOptions{ChunkSize: 256, NoPlugins: true}, newPrinter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	got = bySource(items)
	for _, want := range []string{"guide.mdx", "flat.markdown", "page.html", "plain.py", "config.unknownext"} {
		if got[want] == "" {
			t.Errorf("incremental read lost %s: %v", want, got)
		}
	}
	for _, skip := range []string{"packed.go", "empty.go", "blank.txt", "gone.go"} {
		if got[skip] != "" {
			t.Errorf("incremental read produced passages for %s", skip)
		}
	}
	if docFor(docs, "guide.mdx") == nil {
		t.Error("incremental mdx graph document missing")
	}
	recorded := map[string]bool{}
	for _, r := range records {
		recorded[filepath.Base(r.Path)] = true
	}
	for _, f := range []string{"packed.go", "empty.go", "plain.py", "guide.mdx"} {
		if !recorded[f] {
			t.Errorf("no tracker record for %s (%v)", f, recorded)
		}
	}
	if recorded["gone.go"] {
		t.Error("a missing file got a tracker record")
	}
}

// The incremental reader applies the mode filter and the ignore rules: an
// office document in code mode, code in docs mode and an ignored file are
// recorded (so they are not re-read every sync) but produce no passage.
func TestReadDocumentsForFilesModeAndIgnore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "secret.txt\n")
	writeFile(t, filepath.Join(dir, "secret.txt"), "the hidden ledger")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "notes.md"), "# Notes\n\nsome notes on gannets\n")
	writeFile(t, filepath.Join(dir, "sheet.csv"), "a,b\n1,2\n")
	writeFile(t, filepath.Join(dir, "photo.png"), "\x89PNG")
	writeFile(t, filepath.Join(dir, "data.weird"), "opaque data")
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	all := []string{"secret.txt", "main.go", "notes.md", "sheet.csv", "photo.png", "data.weird", "sub"}
	for i, f := range all {
		all[i] = filepath.Join(dir, f)
	}

	cases := []struct {
		mode      Mode
		want      []string
		recorded  []string
		forbidden []string
	}{
		{ModeCode, []string{"main.go", "notes.md"}, []string{"sheet.csv", "photo.png", "data.weird"}, []string{"sheet.csv", "secret.txt"}},
		{ModeDocs, []string{"notes.md", "sheet.csv"}, []string{"main.go"}, []string{"main.go", "secret.txt"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			items, _, records, err := readDocumentsForFiles(context.Background(), dir, all, ReadOptions{ChunkSize: 256, Mode: tc.mode, NoPlugins: true}, newPrinter(nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			got := bySource(items)
			for _, w := range tc.want {
				if got[w] == "" {
					t.Errorf("%s missing: %v", w, got)
				}
			}
			for _, f := range tc.forbidden {
				if got[f] != "" {
					t.Errorf("%s must be skipped in %s mode", f, tc.mode)
				}
			}
			recorded := map[string]bool{}
			for _, r := range records {
				recorded[filepath.Base(r.Path)] = true
			}
			for _, r := range tc.recorded {
				if !recorded[r] {
					t.Errorf("%s skipped by the mode filter but not recorded", r)
				}
			}
		})
	}

	// Cancelled before the first file.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := readDocumentsForFiles(ctx, dir, all, ReadOptions{NoPlugins: true}, newPrinter(nil, nil)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled incremental read: %v", err)
	}
	if items, docs, recs, err := readDocumentsForFiles(context.Background(), dir, nil, ReadOptions{}, newPrinter(nil, nil)); items != nil || docs != nil || recs != nil || err != nil {
		t.Error("no files must be a no-op")
	}
}

func TestVisionPDFItems(t *testing.T) {
	analysis := &multimodal.PDFAnalysis{Pages: []multimodal.PDFPageResult{
		{PageNum: 1, Description: "# Cover\n\nThe cover shows a kestrel.", MarkerText: "Kestrel Annual Report"},
		{PageNum: 2, Description: "plain description without heading", Tables: &multimodal.TableExtractionResult{
			Tables: []multimodal.Table{{Markdown: "| a | b |\n|---|---|\n| 1 | 2 |"}},
		}},
		{PageNum: 3},
	}}
	items, full := visionPDFItems(analysis, "report.pdf", chunking.NewMarkdownChunker(512, 0))
	for _, want := range []string{"--- Page 1 ---", "kestrel", "Kestrel Annual Report", "--- Page 2 ---", "| 1 | 2 |", "--- Page 3 ---"} {
		if !strings.Contains(full, want) {
			t.Errorf("full markdown lacks %q:\n%s", want, full)
		}
	}
	pages := map[any]bool{}
	for _, it := range items {
		if it.Metadata["source"] != "report.pdf" || it.Metadata["extractor"] != "vision_rag" || it.Metadata["has_image"] != true {
			t.Errorf("item metadata = %v", it.Metadata)
		}
		pages[it.Metadata["page"]] = true
	}
	if !pages[1] || !pages[2] || !pages[3] {
		t.Errorf("not every page produced a chunk: %v", pages)
	}
}

func TestSmallReadHelpers(t *testing.T) {
	if mediaTypeName(multimodal.MediaTypeAudio) != "audio" || mediaTypeName(multimodal.MediaTypeVideo) != "video" || mediaTypeName(multimodal.MediaTypeImage) != "image" {
		t.Error("mediaTypeName")
	}
	long := bytes.Repeat([]byte("a"), 600)
	if looksBinary(long) {
		t.Error("plain text looks binary")
	}
	long[100] = 0
	if !looksBinary(long) {
		t.Error("a NUL in the first 512 bytes is binary")
	}
	long[100] = 'a'
	long[550] = 0
	if looksBinary(long) {
		t.Error("only the first 512 bytes are checked")
	}
	if n := workerCount(); n < 1 || n > 16 {
		t.Errorf("workerCount = %d", n)
	}
	if makeFileRecord("x", nil, nil) != nil {
		t.Error("a record without file info")
	}
}
