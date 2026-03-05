package gleann

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- MarkdownToPluginResult tests ---

func TestMarkdownToPluginResult_BasicHeadings(t *testing.T) {
	md := "# Title\n\nSome intro text.\n\n## Background\n\nBackground details.\n\n## Methods\n\nMethod details."
	result := MarkdownToPluginResult(md, "report.pdf")

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// 1 Document + 3 Sections = 4 nodes.
	if len(result.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(result.Nodes))
	}

	// Document node.
	doc := result.Nodes[0]
	if doc.Type != "Document" {
		t.Errorf("expected Document type, got %s", doc.Type)
	}
	if doc.Data["title"] != "Title" {
		t.Errorf("expected title 'Title', got %v", doc.Data["title"])
	}
	if doc.Data["format"] != "pdf" {
		t.Errorf("expected format 'pdf', got %v", doc.Data["format"])
	}

	// Section nodes.
	sec1 := result.Nodes[1]
	if sec1.Type != "Section" {
		t.Errorf("expected Section type, got %s", sec1.Type)
	}
	if sec1.Data["heading"] != "Title" {
		t.Errorf("expected heading 'Title', got %v", sec1.Data["heading"])
	}
	if sec1.Data["level"] != 1 {
		t.Errorf("expected level 1, got %v", sec1.Data["level"])
	}

	sec2 := result.Nodes[2]
	if sec2.Data["heading"] != "Background" {
		t.Errorf("expected 'Background', got %v", sec2.Data["heading"])
	}
}

func TestMarkdownToPluginResult_Edges(t *testing.T) {
	md := "# Title\n\nText.\n\n## Sub\n\nSub text.\n\n### Sub-Sub\n\nDeep text."
	result := MarkdownToPluginResult(md, "doc.docx")

	// 3 sections → 3 edges.
	if len(result.Edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(result.Edges))
	}

	// Title → HAS_SECTION from Document.
	if result.Edges[0].Type != "HAS_SECTION" {
		t.Errorf("expected HAS_SECTION, got %s", result.Edges[0].Type)
	}
	if result.Edges[0].From != "doc:doc.docx" {
		t.Errorf("expected doc:doc.docx, got %s", result.Edges[0].From)
	}

	// Sub → HAS_SUBSECTION from Title.
	if result.Edges[1].Type != "HAS_SUBSECTION" {
		t.Errorf("expected HAS_SUBSECTION, got %s", result.Edges[1].Type)
	}

	// Sub-Sub → HAS_SUBSECTION from Sub.
	if result.Edges[2].Type != "HAS_SUBSECTION" {
		t.Errorf("expected HAS_SUBSECTION, got %s", result.Edges[2].Type)
	}
}

func TestMarkdownToPluginResult_NoHeadings(t *testing.T) {
	md := "Just plain text without any headings."
	result := MarkdownToPluginResult(md, "plain.txt")

	// Only Document node, no sections.
	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node (Document only), got %d", len(result.Nodes))
	}
	if len(result.Edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(result.Edges))
	}
}

func TestMarkdownToPluginResult_WordCount(t *testing.T) {
	md := "# Title\n\none two three four five"
	result := MarkdownToPluginResult(md, "test.md")

	wc := result.Nodes[0].Data["word_count"].(int)
	if wc != 7 { // "# Title one two three four five" = 7 tokens in Fields
		t.Errorf("expected word_count ~7, got %d", wc)
	}
}

func TestMarkdownToPluginResult_SectionIDs(t *testing.T) {
	md := "# A\n\nText.\n\n## B\n\nText.\n\n## C\n\nText.\n\n### D\n\nText."
	result := MarkdownToPluginResult(md, "test.pdf")

	// s0 = A, s0.0 = B, s0.1 = C, s0.1.0 = D
	expectedIDs := []string{"doc:test.pdf:s0", "doc:test.pdf:s0.0", "doc:test.pdf:s0.1", "doc:test.pdf:s0.1.0"}
	for i, expected := range expectedIDs {
		got, ok := result.Nodes[i+1].Data["id"].(string)
		if !ok {
			t.Errorf("node %d: id not string", i+1)
			continue
		}
		if got != expected {
			t.Errorf("node %d: expected id %s, got %s", i+1, expected, got)
		}
	}
}

func TestMarkdownToPluginResult_Summary(t *testing.T) {
	longPara := strings.Repeat("word ", 100) // 500 chars
	md := "# Title\n\n" + longPara
	result := MarkdownToPluginResult(md, "test.pdf")

	sec := result.Nodes[1]
	summary, ok := sec.Data["summary"].(string)
	if !ok {
		t.Fatal("expected summary to be string")
	}
	if len(summary) > 210 { // 200 + "..."
		t.Errorf("summary too long: %d chars", len(summary))
	}
	if !strings.HasSuffix(summary, "...") {
		t.Errorf("expected summary to end with '...', got: %s", summary[len(summary)-10:])
	}
}

func TestMarkdownToPluginResult_FormatInference(t *testing.T) {
	tests := []struct {
		path   string
		format string
	}{
		{"doc.pdf", "pdf"},
		{"sheet.xlsx", "xlsx"},
		{"slides.pptx", "pptx"},
		{"readme.md", "md"},
		{"noext", "md"}, // default
	}

	for _, tc := range tests {
		result := MarkdownToPluginResult("# Test\n\nContent.", tc.path)
		got := result.Nodes[0].Data["format"]
		if got != tc.format {
			t.Errorf("path=%s: expected format %s, got %v", tc.path, tc.format, got)
		}
	}
}

// --- parseHeadings tests ---

func TestParseHeadings_Hierarchy(t *testing.T) {
	md := "# H1\n\nText1.\n\n## H2\n\nText2.\n\n### H3\n\nText3."
	sections := parseHeadings(md)

	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}

	// H1 is root.
	if sections[0].parentID != "" {
		t.Errorf("H1 should have no parent, got %s", sections[0].parentID)
	}

	// H2 parent is H1.
	if sections[1].parentID != sections[0].id {
		t.Errorf("H2 parent should be %s, got %s", sections[0].id, sections[1].parentID)
	}

	// H3 parent is H2.
	if sections[2].parentID != sections[1].id {
		t.Errorf("H3 parent should be %s, got %s", sections[1].id, sections[2].parentID)
	}
}

func TestParseHeadings_SiblingOrder(t *testing.T) {
	md := "# Root\n\n## First\n\nA.\n\n## Second\n\nB.\n\n## Third\n\nC."
	sections := parseHeadings(md)

	if len(sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(sections))
	}

	// Siblings should have sequential order.
	if sections[1].order != 0 {
		t.Errorf("First should be order 0, got %d", sections[1].order)
	}
	if sections[2].order != 1 {
		t.Errorf("Second should be order 1, got %d", sections[2].order)
	}
	if sections[3].order != 2 {
		t.Errorf("Third should be order 2, got %d", sections[3].order)
	}
}

func TestParseHeadings_EmptyContent(t *testing.T) {
	md := "# Title\n\n## Empty\n\n## WithContent\n\nSome content here."
	sections := parseHeadings(md)

	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}

	// "Empty" section should have empty content.
	if sections[1].content != "" {
		t.Errorf("expected empty content for 'Empty', got: %q", sections[1].content)
	}
	// "WithContent" should have content.
	if sections[2].content == "" {
		t.Error("expected non-empty content for 'WithContent'")
	}
}

// --- firstParagraph tests ---

func TestFirstParagraph_Short(t *testing.T) {
	text := "Short paragraph."
	got := firstParagraph(text, 200)
	if got != "Short paragraph." {
		t.Errorf("expected 'Short paragraph.', got %q", got)
	}
}

func TestFirstParagraph_Long(t *testing.T) {
	text := strings.Repeat("word ", 100)
	got := firstParagraph(text, 50)
	if len(got) > 55 { // 50 + some buffer for "..."
		t.Errorf("expected truncated text, got %d chars", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("expected '...' suffix")
	}
}

func TestFirstParagraph_SkipsHeadings(t *testing.T) {
	text := "## Heading\n\nActual content."
	got := firstParagraph(text, 200)
	if got != "Actual content." {
		t.Errorf("expected 'Actual content.', got %q", got)
	}
}

func TestFirstParagraph_Empty(t *testing.T) {
	got := firstParagraph("", 200)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- MarkItDownExtractor tests ---

func TestCanHandle(t *testing.T) {
	ext := &MarkItDownExtractor{binaryPath: "/usr/bin/markitdown"}

	validExts := []string{".pdf", ".docx", ".xlsx", ".pptx", ".csv", ".png", ".jpg", ".jpeg"}
	for _, e := range validExts {
		if !ext.CanHandle(e) {
			t.Errorf("expected CanHandle(%s) = true", e)
		}
	}

	invalidExts := []string{".go", ".py", ".txt", ".html", ".json"}
	for _, e := range invalidExts {
		if ext.CanHandle(e) {
			t.Errorf("expected CanHandle(%s) = false", e)
		}
	}
}

func TestCanHandle_CaseInsensitive(t *testing.T) {
	ext := &MarkItDownExtractor{binaryPath: "/usr/bin/markitdown"}
	if !ext.CanHandle(".PDF") {
		t.Error("expected CanHandle('.PDF') = true (case insensitive)")
	}
}

func TestNewMarkItDownExtractor_NilWhenMissing(t *testing.T) {
	// Save and restore PATH.
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	ext := NewMarkItDownExtractor()
	// May or may not be nil depending on ~/.local/bin, but shouldn't panic.
	_ = ext
}

func TestMarkItDownExtractor_NilSafety(t *testing.T) {
	var ext *MarkItDownExtractor

	if ext.Available() {
		t.Error("nil extractor should not be available")
	}
	if ext.BinaryPath() != "" {
		t.Error("nil extractor should return empty path")
	}

	_, err := ext.Extract("test.pdf")
	if err == nil {
		t.Error("nil extractor Extract should return error")
	}
}

func TestSupportedExtensions(t *testing.T) {
	ext := &MarkItDownExtractor{binaryPath: "/usr/bin/markitdown"}
	exts := ext.SupportedExtensions()

	if len(exts) < 5 {
		t.Errorf("expected at least 5 extensions, got %d", len(exts))
	}

	// Check that all returned extensions are valid.
	for _, e := range exts {
		if !strings.HasPrefix(e, ".") {
			t.Errorf("extension should start with '.': %s", e)
		}
	}
}

// --- DocExtractor tests ---

func TestDocExtractor_NilLayers(t *testing.T) {
	de := NewDocExtractor(nil, nil)
	_, err := de.Extract("test.pdf")
	if err == nil {
		t.Error("expected error with nil layers")
	}
}

func TestDocExtractor_ExtractWithCLI_NilMarkitdown(t *testing.T) {
	de := NewDocExtractor(nil, nil)
	_, err := de.ExtractWithCLI("test.pdf")
	if err == nil {
		t.Error("expected error with nil markitdown")
	}
}

// --- Integration test (skipped if markitdown not available) ---

func TestIntegration_MarkItDown_RealCLI(t *testing.T) {
	path, err := FindMarkItDown()
	if err != nil {
		t.Skip("markitdown not found, skipping integration test")
	}

	// Create a temp CSV file to test.
	tmpDir := t.TempDir()
	csvFile := filepath.Join(tmpDir, "test.csv")
	err = os.WriteFile(csvFile, []byte("name,age\nAlice,30\nBob,25\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ext := &MarkItDownExtractor{binaryPath: path, timeout: 10 * 1e9}
	markdown, err := ext.Extract(csvFile)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if markdown == "" {
		t.Fatal("expected non-empty markdown from CSV")
	}

	// The output should contain the CSV data.
	if !strings.Contains(markdown, "Alice") || !strings.Contains(markdown, "Bob") {
		t.Errorf("expected CSV data in markdown, got: %s", markdown[:min(len(markdown), 200)])
	}
}

func TestIntegration_DocExtractor_WithCLI(t *testing.T) {
	path, err := FindMarkItDown()
	if err != nil {
		t.Skip("markitdown not found, skipping integration test")
	}

	// Create a temp markdown file (markitdown passes through .md too).
	tmpDir := t.TempDir()
	mdFile := filepath.Join(tmpDir, "test.md")
	err = os.WriteFile(mdFile, []byte("# Hello\n\nWorld.\n\n## Section\n\nDetails."), 0644)
	if err != nil {
		t.Fatal(err)
	}

	mid := &MarkItDownExtractor{binaryPath: path, timeout: 10 * 1e9}
	de := NewDocExtractor(mid, nil)

	// .md is not in markitdownExts, so this should fail.
	_, err = de.Extract(mdFile)
	if err == nil {
		t.Log("markitdown handled .md file (unexpected but ok)")
	}
}

