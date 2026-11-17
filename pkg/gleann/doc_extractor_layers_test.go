package gleann

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeConverter writes a shell script standing in for anydoc/markitdown.
func fakeConverter(t *testing.T, body string) *AnyDocExtractor {
	t.Helper()
	p := filepath.Join(t.TempDir(), "anydoc")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &AnyDocExtractor{binaryPath: p, isAnyDoc: true}
}

func headingTitles(r *PluginResult) []string {
	var out []string
	for _, n := range r.Nodes {
		if n.Type == "Section" {
			if h, ok := n.Data["heading"].(string); ok {
				out = append(out, h)
			}
		}
	}
	return out
}

// The extractor tries the plugin server, then the converter CLI, then the
// pure-Go parsers, falling through on each failure.
func TestDocExtractorFallsThroughLayers(t *testing.T) {
	requireShell(t)
	file := filepath.Join(t.TempDir(), "table.csv")
	if err := os.WriteFile(file, []byte("name,qty\napple,3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	good, _ := convertPlugin(t, http.StatusOK, `{"nodes":[{"_type":"Document","path":"table.csv"}],"edges":[]}`)
	bad, _ := convertPlugin(t, http.StatusInternalServerError, "crashed")
	withPlugin := func(p *Plugin) *PluginManager {
		p.Capabilities = []string{CapDocumentExtraction}
		p.Extensions = []string{".csv"}
		return &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{*p}}}
	}
	cli := fakeConverter(t, `printf '# From CLI\n\nrows\n'`)
	brokenCLI := fakeConverter(t, `echo "cannot parse" >&2; exit 3`)

	// Layer 1 answers.
	r, err := NewDocExtractor(cli, withPlugin(good)).Extract(file)
	if err != nil || len(r.Nodes) != 1 || r.Nodes[0].Type != "Document" {
		t.Fatalf("plugin layer = %+v, %v", r, err)
	}
	// The plugin fails: the CLI answers.
	r, err = NewDocExtractor(cli, withPlugin(bad)).Extract(file)
	if err != nil || strings.Join(headingTitles(r), ",") != "From CLI" {
		t.Fatalf("CLI layer = %+v, %v", r, err)
	}
	// The CLI fails too: the native parser answers with the table.
	r, err = NewDocExtractor(brokenCLI, withPlugin(bad)).Extract(file)
	if err != nil || r == nil || len(r.Nodes) == 0 {
		t.Fatalf("native layer = %+v, %v", r, err)
	}
	if _, err := brokenCLI.Extract(file); err == nil || !strings.Contains(err.Error(), "cannot parse") {
		t.Fatalf("the CLI's stderr must be reported: %v", err)
	}
	if _, err := NewDocExtractor(brokenCLI, nil).ExtractWithCLI(file); err == nil || !strings.Contains(err.Error(), "markitdown extract") {
		t.Fatalf("ExtractWithCLI: %v", err)
	}
	if _, err := NewDocExtractor(nil, nil).ExtractWithCLI(file); err == nil {
		t.Fatal("ExtractWithCLI without a CLI succeeded")
	}
	missing := &AnyDocExtractor{binaryPath: filepath.Join(t.TempDir(), "nope")}
	if _, err := missing.Extract(file); err == nil || !strings.Contains(err.Error(), "exec") {
		t.Fatalf("missing CLI binary: %v", err)
	}

	// No layer handles the extension.
	odd := filepath.Join(t.TempDir(), "blob.xyz")
	if err := os.WriteFile(odd, []byte("?"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDocExtractor(nil, nil).Extract(odd); err == nil || !strings.Contains(err.Error(), "no extractor") {
		t.Fatalf("unknown extension: %v", err)
	}
	de := &DocExtractor{}
	if _, err := de.ExtractNative(file); err == nil {
		t.Fatal("ExtractNative without the native parser succeeded")
	}
}

func TestParseHeadingsSkipsFencesAndNonHeadings(t *testing.T) {
	md := "# Top\n" +
		"```\n# not a heading inside a fence\n```\n" +
		"~~~\n## also fenced\n~~~\n" +
		"####### seven hashes is text\n" +
		"#\n" +
		"## Child\n" +
		"#hashtag-without-space is accepted as a heading\n"
	got := parseHeadings(md)
	var titles []string
	for _, s := range got {
		titles = append(titles, s.heading)
	}
	if strings.Join(titles, "|") != "Top|Child|hashtag-without-space is accepted as a heading" {
		t.Fatalf("headings = %q", titles)
	}
	if parseHeadings("no headings here\n```\n# fenced\n```") != nil {
		t.Fatal("fenced-only headings were parsed")
	}
}
