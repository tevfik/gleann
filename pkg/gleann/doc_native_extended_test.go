package gleann

import (
	"os"
	"strings"
	"testing"
)

func TestRenderMarkdownTable(t *testing.T) {
	tests := []struct {
		name string
		rows [][]string
		want string
	}{
		{"empty", nil, ""},
		{"empty inner", [][]string{{}}, ""},
		{"header only", [][]string{{"A", "B"}}, "| A | B |\n| --- | --- |\n"},
		{"header and data", [][]string{{"Name", "Value"}, {"foo", "bar"}}, "| Name | Value |\n| --- | --- |\n| foo | bar |\n"},
		{"ragged rows", [][]string{{"A", "B", "C"}, {"1"}, {"x", "y"}}, "| A | B | C |\n| --- | --- | --- |\n| 1 |  |  |\n| x | y |  |\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderMarkdownTable(tt.rows)
			if got != tt.want {
				t.Errorf("\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestPadRowEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		row   []string
		width int
		want  int
	}{
		{"shorter", []string{"a"}, 3, 3},
		{"exact", []string{"a", "b"}, 2, 2},
		{"empty", nil, 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padRow(tt.row, tt.width)
			if len(got) != tt.want {
				t.Errorf("len = %d, want %d", len(got), tt.want)
			}
			// Verify original values preserved
			for i := 0; i < len(tt.row) && i < len(got); i++ {
				if got[i] != tt.row[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.row[i])
				}
			}
		})
	}
}

func TestHtmlToMarkdownExtended(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		fileName string
		contains []string
	}{
		{
			"heading h1",
			"<h1>Title</h1><p>Body text here.</p>",
			"test.html",
			[]string{"## Title", "Body text here"},
		},
		{
			"heading h2",
			"<h2>Subtitle</h2><p>Content</p>",
			"page.html",
			[]string{"### Subtitle", "Content"},
		},
		{
			"heading h3",
			"<h3>Section</h3><p>Details</p>",
			"doc.html",
			[]string{"#### Section", "Details"},
		},
		{
			"list items",
			"<ul><li>First</li><li>Second</li></ul>",
			"list.html",
			[]string{"- First", "- Second"},
		},
		{
			"br tag",
			"<p>Line one<br>Line two</p>",
			"br.html",
			[]string{"Line one", "Line two"},
		},
		{
			"empty",
			"",
			"empty.html",
			[]string{"# empty.html"},
		},
		{
			"file name in header",
			"<p>text</p>",
			"/path/to/file.html",
			[]string{"# file.html"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlToMarkdown(tt.html, tt.fileName)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestExtractXMLText(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{"simple text", "<a:t>Hello</a:t>", "Hello"},
		{"multiple texts", "<p><a:t>Hello</a:t> <a:t>World</a:t></p>", "Hello World"},
		{"empty", "<p></p>", ""},
		{"table content", "<a:tbl><a:tr><a:tc><a:t>Cell</a:t></a:tc></a:tr></a:tbl>", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractXMLText(strings.NewReader(tt.xml))
			got = strings.TrimSpace(got)
			if !strings.Contains(got, tt.want) && tt.want != "" {
				t.Errorf("got %q, want contains %q", got, tt.want)
			}
		})
	}
}

func TestNewNativeExtractor(t *testing.T) {
	ext := NewNativeExtractor()
	if ext == nil {
		t.Fatal("NewNativeExtractor() returned nil")
	}
}

func TestNativeExtractorCanHandle(t *testing.T) {
	ext := NewNativeExtractor()

	tests := []struct {
		ext  string
		want bool
	}{
		{".csv", true},
		{".CSV", true},
		{".html", true},
		{".htm", true},
		{".txt", true},
		{".md", true},
		{".go", false},
		{".py", false},
		{".exe", false},
		{".docx", true},
		{".xlsx", true},
		{".pptx", true},
		{".pdf", true},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := ext.CanHandle(tt.ext)
			if got != tt.want {
				t.Errorf("CanHandle(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

func TestNativeExtractorSupportedExtensions(t *testing.T) {
	ext := NewNativeExtractor()
	exts := ext.SupportedExtensions()
	if len(exts) == 0 {
		t.Error("SupportedExtensions() returned empty slice")
	}

	// Check a few known extensions
	extMap := make(map[string]bool)
	for _, e := range exts {
		extMap[e] = true
	}
	for _, want := range []string{".csv", ".html", ".txt", ".md", ".docx", ".xlsx"} {
		if !extMap[want] {
			t.Errorf("missing expected extension %q", want)
		}
	}
}

func TestMarkItDownCanHandle(t *testing.T) {
	// CanHandle works on the extension map even with nil extractor
	ext := &MarkItDownExtractor{} // no binary path
	if !ext.CanHandle(".pdf") {
		t.Error("CanHandle(.pdf) should be true")
	}
	if !ext.CanHandle(".docx") {
		t.Error("CanHandle(.docx) should be true")
	}
	if ext.CanHandle(".go") {
		t.Error("CanHandle(.go) should be false")
	}
}

func TestMarkItDownAvailable(t *testing.T) {
	var nilExt *MarkItDownExtractor
	if nilExt.Available() {
		t.Error("nil extractor should not be available")
	}

	ext := &MarkItDownExtractor{}
	if ext.Available() {
		t.Error("empty binary path should not be available")
	}

	ext = &MarkItDownExtractor{binaryPath: "/usr/bin/markitdown"}
	if !ext.Available() {
		t.Error("extractor with path should be available")
	}
}

func TestMarkItDownBinaryPath(t *testing.T) {
	var nilExt *MarkItDownExtractor
	if nilExt.BinaryPath() != "" {
		t.Error("nil extractor BinaryPath should be empty")
	}

	ext := &MarkItDownExtractor{binaryPath: "/usr/bin/markitdown"}
	if ext.BinaryPath() != "/usr/bin/markitdown" {
		t.Errorf("BinaryPath = %q", ext.BinaryPath())
	}
}

func TestMarkItDownExtractNilError(t *testing.T) {
	var nilExt *MarkItDownExtractor
	_, err := nilExt.Extract("test.pdf")
	if err == nil {
		t.Error("expected error from nil extractor")
	}

	ext := &MarkItDownExtractor{}
	_, err = ext.Extract("test.pdf")
	if err == nil {
		t.Error("expected error from empty binary path")
	}
}

func TestMarkItDownSupportedExtensions(t *testing.T) {
	ext := &MarkItDownExtractor{binaryPath: "/usr/bin/markitdown"}
	exts := ext.SupportedExtensions()
	if len(exts) == 0 {
		t.Error("expected non-empty extensions list")
	}
}

func TestNativeExtractorExtractCSVFromTempFile(t *testing.T) {
	ext := NewNativeExtractor()
	tmpFile := t.TempDir() + "/test.csv"
	data := "Name,Age,City\nAlice,30,NYC\nBob,25,LA\n"
	if err := writeTestFile(tmpFile, data); err != nil {
		t.Fatal(err)
	}

	result, err := ext.Extract(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Alice") {
		t.Error("CSV extraction should contain Alice")
	}
	if !strings.Contains(result, "Bob") {
		t.Error("CSV extraction should contain Bob")
	}
	if !strings.Contains(result, "|") {
		t.Error("CSV extraction should produce markdown table")
	}
}

func TestNativeExtractorExtractText(t *testing.T) {
	ext := NewNativeExtractor()
	tmpFile := t.TempDir() + "/test.txt"
	content := "Hello, World! This is plain text."
	if err := writeTestFile(tmpFile, content); err != nil {
		t.Fatal(err)
	}

	result, err := ext.Extract(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, content) {
		t.Errorf("expected content %q in result", content)
	}
}

func TestNativeExtractorExtractHTML(t *testing.T) {
	ext := NewNativeExtractor()
	tmpFile := t.TempDir() + "/test.html"
	html := "<html><body><h1>Title</h1><p>Paragraph content here.</p></body></html>"
	if err := writeTestFile(tmpFile, html); err != nil {
		t.Fatal(err)
	}

	result, err := ext.Extract(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Title") {
		t.Error("HTML extraction should contain Title")
	}
	if !strings.Contains(result, "Paragraph content here") {
		t.Error("HTML extraction should contain paragraph text")
	}
}

func TestNativeExtractorExtractMarkdown(t *testing.T) {
	ext := NewNativeExtractor()
	tmpFile := t.TempDir() + "/test.md"
	md := "# Heading\n\nSome markdown content.\n"
	if err := writeTestFile(tmpFile, md); err != nil {
		t.Fatal(err)
	}

	result, err := ext.Extract(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Heading") {
		t.Error("MD extraction should contain Heading")
	}
}

func TestNativeExtractorUnsupported(t *testing.T) {
	ext := NewNativeExtractor()
	tmpFile := t.TempDir() + "/test.xyz"
	if err := writeTestFile(tmpFile, "data"); err != nil {
		t.Fatal(err)
	}

	_, err := ext.Extract(tmpFile)
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestNativeExtractorFileNotFound(t *testing.T) {
	ext := NewNativeExtractor()
	_, err := ext.Extract("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
