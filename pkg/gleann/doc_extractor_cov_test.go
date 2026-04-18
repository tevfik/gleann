package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

// ── DocExtractor.Extract — layer fallthrough tests ──────────────

func TestDocExtractor_ExtractNative_MD(t *testing.T) {
	// Create a real .md file and test the native extraction path.
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "test.md")
	content := "# Title\n\nSome content here.\n\n## Subtitle\n\nMore content.\n"
	if err := os.WriteFile(mdFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	de := NewDocExtractor(nil, nil) // only native
	result, err := de.Extract(mdFile)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Nodes) < 2 {
		t.Errorf("expected at least 2 nodes (doc + sections), got %d", len(result.Nodes))
	}
}

func TestDocExtractor_ExtractNative_CSV(t *testing.T) {
	dir := t.TempDir()
	csvFile := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(csvFile, []byte("a,b,c\n1,2,3\n4,5,6\n"), 0644); err != nil {
		t.Fatal(err)
	}

	de := NewDocExtractor(nil, nil)
	result, err := de.Extract(csvFile)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestDocExtractor_ExtractNative_TXT(t *testing.T) {
	dir := t.TempDir()
	txtFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txtFile, []byte("Just a text file."), 0644); err != nil {
		t.Fatal(err)
	}

	de := NewDocExtractor(nil, nil)
	result, err := de.Extract(txtFile)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestDocExtractor_ExtractNative_HTML(t *testing.T) {
	dir := t.TempDir()
	htmlFile := filepath.Join(dir, "page.html")
	content := `<html><body><h1>Hello</h1><p>World</p></body></html>`
	if err := os.WriteFile(htmlFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	de := NewDocExtractor(nil, nil)
	result, err := de.Extract(htmlFile)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestDocExtractor_NoExtractorAvailable(t *testing.T) {
	dir := t.TempDir()
	// .xyz is not handled by any extractor
	f := filepath.Join(dir, "test.xyz")
	os.WriteFile(f, []byte("data"), 0644)

	de := NewDocExtractor(nil, nil)
	_, err := de.Extract(f)
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestDocExtractor_ExtractNative_NilNative(t *testing.T) {
	de := &DocExtractor{native: nil, markitdown: nil, plugins: nil}
	_, err := de.ExtractNative("/tmp/test.md")
	if err == nil {
		t.Error("expected error when native is nil")
	}
}

func TestDocExtractor_ExtractWithCLI_NilMarkitdownCov(t *testing.T) {
	de := &DocExtractor{native: nil, markitdown: nil, plugins: nil}
	_, err := de.ExtractWithCLI("/tmp/test.pdf")
	if err == nil {
		t.Error("expected error when markitdown is nil")
	}
}

// ── MarkdownToPluginResult — additional coverage ────────────────

func TestMarkdownToPluginResult_SubsectionEdges(t *testing.T) {
	md := "# Main\n\nContent\n\n## Sub1\n\nSub content\n\n### SubSub\n\nDeep content\n"
	result := MarkdownToPluginResult(md, "test.md")
	if result == nil {
		t.Fatal("nil result")
	}
	hasSubsection := false
	for _, e := range result.Edges {
		if e.Type == "HAS_SUBSECTION" {
			hasSubsection = true
			break
		}
	}
	if !hasSubsection {
		t.Error("expected HAS_SUBSECTION edge for nested headings")
	}
}

func TestMarkdownToPluginResult_NoExtension(t *testing.T) {
	result := MarkdownToPluginResult("hello", "README")
	if result == nil {
		t.Fatal("nil result")
	}
	if result.Nodes[0].Data["format"] != "md" {
		t.Errorf("expected default format 'md', got %v", result.Nodes[0].Data["format"])
	}
}

func TestMarkdownToPluginResult_DifferentFormat(t *testing.T) {
	result := MarkdownToPluginResult("hello", "test.docx")
	if result == nil {
		t.Fatal("nil result")
	}
	if result.Nodes[0].Data["format"] != "docx" {
		t.Errorf("expected format 'docx', got %v", result.Nodes[0].Data["format"])
	}
}

func TestMarkdownToPluginResult_HashAndSummary(t *testing.T) {
	md := "# Title\n\nFirst paragraph that serves as summary.\n\nMore content.\n"
	result := MarkdownToPluginResult(md, "test.md")
	if result.Nodes[0].Data["hash"] == "" {
		t.Error("expected non-empty hash")
	}
	if result.Nodes[0].Data["summary"] == "" {
		t.Error("expected non-empty summary")
	}
}

// ── parseHeadings — more edge cases ─────────────────────────────

func TestParseHeadings_MixedLevels(t *testing.T) {
	md := "# H1\n\n## H2\n\n#### H4 (skip H3)\n\n## Another H2\n"
	sections := parseHeadings(md)
	if len(sections) < 3 {
		t.Errorf("expected at least 3 sections, got %d", len(sections))
	}
}

func TestParseHeadings_ContentBetweenHeadings(t *testing.T) {
	md := "# First\n\nParagraph 1\n\nParagraph 2\n\n# Second\n\nMore text\n"
	sections := parseHeadings(md)
	if len(sections) < 2 {
		t.Errorf("expected at least 2 sections, got %d", len(sections))
	}
	// First section should have content
	found := false
	for _, s := range sections {
		if s.heading == "First" && len(s.content) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected first section to have content")
	}
}

// ── firstParagraph — additional ─────────────────────────────────

func TestFirstParagraph_WithCode(t *testing.T) {
	md := "```go\nfunc main() {}\n```\n\nActual paragraph.\n"
	result := firstParagraph(md, 100)
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestFirstParagraph_AllHeadings(t *testing.T) {
	md := "# H1\n\n## H2\n\n### H3\n"
	result := firstParagraph(md, 100)
	// Should return something even if all lines are headings
	_ = result // Just don't crash
}
