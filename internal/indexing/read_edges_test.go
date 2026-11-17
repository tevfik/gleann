package indexing

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/multimodal"
)

// A directory the path is not under (here a relative one against an absolute
// path) makes filepath.Rel fail; the source falls back to the base name.
func TestSourceRelFallsBackToBaseName(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "sub", "file.go")
	if got := sourceRel("relative/dir", abs); got != "file.go" {
		t.Errorf("sourceRel with unrelatable dir = %q, want file.go", got)
	}
	if got := sourceRel(filepath.Dir(abs), abs); got != "file.go" {
		t.Errorf("sourceRel = %q", got)
	}
}

// A plugin registry that cannot be parsed disables plugins instead of
// failing the read.
func TestOpenPluginManagerBrokenRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeFile(t, filepath.Join(home, ".gleann", "plugins.json"), "{not json")
	pm, closeFn := openPluginManager(false, ModeAll)
	if pm != nil {
		t.Error("a broken plugin registry produced a manager")
	}
	closeFn() // must be safe to call

	pm, closeFn = openPluginManager(true, ModeAll)
	if pm != nil {
		t.Error("NoPlugins produced a manager")
	}
	closeFn()
}

// A nil context is treated as Background; a cancelled one aborts the read
// with the context error instead of returning partial results.
func TestReadDocumentsContexts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		writeFile(t, filepath.Join(dir, n), "Some text for "+n+" about marmosets.")
	}

	var nilCtx context.Context
	items, _, err := ReadDocuments(nilCtx, dir, ReadOptions{ChunkSize: 128, NoPlugins: true})
	if err != nil || len(items) != 3 {
		t.Fatalf("nil context: %d items, %v", len(items), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// CollectEligibleFiles may notice the cancellation first or the workers
	// may; either way the error must be the context's.
	items, docs, err := ReadDocuments(ctx, dir, ReadOptions{ChunkSize: 128, NoPlugins: true})
	if err == nil || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("cancelled read: err = %v", err)
	}
	if items != nil || docs != nil {
		t.Errorf("cancelled read returned results: %d items, %d docs", len(items), len(docs))
	}

	_, _, _, err = readDocumentsForFiles(ctx, dir, []string{filepath.Join(dir, "a.txt")}, ReadOptions{NoPlugins: true}, newPrinter(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("cancelled incremental read: err = %v", err)
	}
}

// Markdown that only has a heading yields no chunks from the markdown
// chunker; native-extracted documents then fall back to the sentence splitter
// and still reach the passages.
func TestReadDocumentsNativeHeadingOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "title.html"), "<html><body><h1>Lonely Heading</h1></body></html>")

	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 128, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(items)["title.html"]; !strings.Contains(got, "Lonely Heading") {
		t.Fatalf("heading-only html lost: %v", bySource(items))
	}
	if extractorOf(items, "title.html") != "native" {
		t.Errorf("extractor = %q", extractorOf(items, "title.html"))
	}

	items, _, _, err = readDocumentsForFiles(context.Background(), dir, []string{filepath.Join(dir, "title.html")}, ReadOptions{ChunkSize: 128, NoPlugins: true}, newPrinter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(items)["title.html"]; !strings.Contains(got, "Lonely Heading") {
		t.Fatalf("incremental heading-only html lost: %v", bySource(items))
	}
}

// fakePDFRenderer puts a pdftoppm stand-in first on PATH that "renders" one
// page image, so the vision path can run without poppler installed.
func fakePDFRenderer(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script renderer stand-in")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nfor last; do :; done\nprintf 'fakejpeg' > \"${last}-1.jpg\"\n"
	if err := os.WriteFile(filepath.Join(bin, "pdftoppm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A PDF the vision model can describe becomes vision_rag passages plus a
// graph document, in both the full and the incremental reader.
func TestReadDocumentsVisionPDFSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakePDFRenderer(t)
	vis := fakeVision(t, false)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "deck.pdf"), "%PDF-1.4\n%%EOF\n")
	mm := &multimodal.Processor{OllamaHost: vis.URL, Model: "vlm", Lang: "en"}

	items, docs, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, Multimodal: mm, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(items)["deck.pdf"]; !strings.Contains(got, "quokka pipeline") {
		t.Fatalf("vision description missing: %v", bySource(items))
	}
	if extractorOf(items, "deck.pdf") != "vision_rag" {
		t.Errorf("extractor = %q", extractorOf(items, "deck.pdf"))
	}
	if docFor(docs, "deck.pdf") == nil {
		t.Error("no graph document for the vision PDF")
	}

	var errBuf bytes.Buffer
	items, docs, records, err := readDocumentsForFiles(context.Background(), dir, []string{filepath.Join(dir, "deck.pdf")}, ReadOptions{ChunkSize: 256, Multimodal: mm, NoPlugins: true}, newPrinter(nil, &errBuf))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bySource(items)["deck.pdf"], "quokka pipeline") || docFor(docs, "deck.pdf") == nil || len(records) != 1 {
		t.Fatalf("incremental vision PDF: %v, docs %d, records %d (%s)", bySource(items), len(docs), len(records), errBuf.String())
	}
}

// Binary content behind a code extension is skipped by the full reader too,
// but its record is kept so it is not re-read on every sync.
func TestReadDocumentsSkipsBinaryContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "packed.go"), "package x\x00\x01\x02")
	writeFile(t, filepath.Join(dir, "ok.go"), "package x\n\nfunc Ok() {}\n")
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	got := bySource(items)
	if _, has := got["packed.go"]; has {
		t.Errorf("binary file produced passages: %v", got)
	}
	if got["ok.go"] == "" {
		t.Errorf("text file lost: %v", got)
	}
}
