package multimodal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPDFConfig(t *testing.T) {
	cfg := DefaultPDFConfig()
	if cfg.DPI != 150 {
		t.Errorf("expected DPI=150, got %d", cfg.DPI)
	}
	if !cfg.UseMarker {
		t.Error("expected UseMarker=true")
	}
}

func TestAnalyzePDF_NoModel(t *testing.T) {
	p := &Processor{OllamaHost: "http://localhost:19999"}
	_, err := p.AnalyzePDF("/nonexistent.pdf", DefaultPDFConfig())
	if err == nil {
		t.Error("expected error with no model")
	}
}

func TestAnalyzePDF_FileNotFound(t *testing.T) {
	p := NewProcessor("http://localhost:19999", "test-model")
	_, err := p.AnalyzePDF("/nonexistent/doc.pdf", DefaultPDFConfig())
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestDetectTableInDescription(t *testing.T) {
	tests := []struct {
		desc string
		want bool
	}{
		{"This page contains a table with data", true},
		{"| Col1 | Col2 | Col3 |", true},
		{"The column headers are visible", true},
		{"This is a plain text paragraph", false},
		{"", false},
	}
	for _, tt := range tests {
		got := detectTableInDescription(tt.desc)
		if got != tt.want {
			t.Errorf("detectTableInDescription(%q) = %v, want %v", tt.desc, got, tt.want)
		}
	}
}

func TestDetectChartInDescription(t *testing.T) {
	tests := []struct {
		desc string
		want bool
	}{
		{"The page shows a bar chart with sales data", true},
		{"Figure 1: Revenue growth", true},
		{"A line graph displays the trend", true},
		{"There is a diagram of the architecture", true},
		{"This is plain text content", false},
		{"", false},
	}
	for _, tt := range tests {
		got := detectChartInDescription(tt.desc)
		if got != tt.want {
			t.Errorf("detectChartInDescription(%q) = %v, want %v", tt.desc, got, tt.want)
		}
	}
}

func TestPdfPagePrompt(t *testing.T) {
	prompt := pdfPagePrompt(3, "")
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "page 3") {
		t.Error("expected page number in prompt")
	}
}

func TestPdfPagePrompt_WithMarkerText(t *testing.T) {
	prompt := pdfPagePrompt(1, "Some extracted text from marker")
	if !strings.Contains(prompt, "Some extracted text") {
		t.Error("expected marker text in prompt")
	}
}

func TestTryMarkerExtraction_NoServer(t *testing.T) {
	// With no marker server running (on non-standard port), should return nil.
	result := tryMarkerExtraction("/nonexistent.pdf")
	if result != nil {
		t.Error("expected nil when marker not available")
	}
}

func TestCollectPageImages_Empty(t *testing.T) {
	dir := t.TempDir()
	imgs, err := collectPageImages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("expected empty, got %d images", len(imgs))
	}
}

func TestCollectPageImages_Mixed(t *testing.T) {
	dir := t.TempDir()
	// Create some image files and non-image files.
	for _, f := range []string{"page-1.jpg", "page-2.png", "readme.txt", "page-3.jpeg"} {
		os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0644)
	}
	imgs, err := collectPageImages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(imgs) != 3 {
		t.Errorf("expected 3 images, got %d", len(imgs))
	}
}

func TestCleanupPDFPages_Empty(t *testing.T) {
	// Should not panic.
	CleanupPDFPages(nil)
	CleanupPDFPages([]PDFPageResult{})
}

func TestCleanupPDFPages_NonGleannDir(t *testing.T) {
	dir := t.TempDir()
	pages := []PDFPageResult{
		{ImagePath: filepath.Join(dir, "page-1.jpg")},
	}
	CleanupPDFPages(pages)
	// Dir should NOT be deleted since it doesn't match pattern.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("should not delete non-gleann directory")
	}
}

func TestRenderPDFPages_NoRenderer(t *testing.T) {
	// Clear PATH to ensure no PDF renderer is found.
	originalPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", originalPath)

	_, err := renderPDFPages("/nonexistent.pdf", 150, 0)
	if err == nil {
		t.Error("expected error when no renderer found")
	}
}

// ── Table Extraction Tests ─────────────────────────────────────

func TestParseTablesFromMarkdown_Simple(t *testing.T) {
	md := `Here is a table:

| Name | Age | City |
| --- | --- | --- |
| Alice | 30 | NYC |
| Bob | 25 | LA |

Some text after.`

	tables := parseTablesFromMarkdown(md)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if len(tables[0].Headers) != 3 {
		t.Errorf("expected 3 headers, got %d", len(tables[0].Headers))
	}
	if len(tables[0].Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(tables[0].Rows))
	}
	if tables[0].Headers[0] != "Name" {
		t.Errorf("expected header 'Name', got %q", tables[0].Headers[0])
	}
}

func TestParseTablesFromMarkdown_Multiple(t *testing.T) {
	md := `| A | B |
| --- | --- |
| 1 | 2 |

Some separator text.

| X | Y | Z |
| --- | --- | --- |
| a | b | c |`

	tables := parseTablesFromMarkdown(md)
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
}

func TestParseTablesFromMarkdown_Empty(t *testing.T) {
	tables := parseTablesFromMarkdown("No tables here, just text.")
	if len(tables) != 0 {
		t.Errorf("expected 0 tables, got %d", len(tables))
	}
}

func TestIsSeparatorRow(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"| --- | --- | --- |", true},
		{"|---|---|", true},
		{"| :---: | ---: |", true},
		{"| Name | Age |", false},
		{"regular text", false},
	}
	for _, tt := range tests {
		got := isSeparatorRow(tt.line)
		if got != tt.want {
			t.Errorf("isSeparatorRow(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestSplitTableRow(t *testing.T) {
	cells := splitTableRow("| Alice | 30 | NYC |")
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d: %v", len(cells), cells)
	}
	if cells[0] != "Alice" {
		t.Errorf("expected 'Alice', got %q", cells[0])
	}
}

func TestRenderTableMarkdown(t *testing.T) {
	tbl := &Table{
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"1", "2"}, {"3", "4"}},
	}
	md := renderTableMarkdown(tbl)
	if !strings.Contains(md, "| A | B |") {
		t.Errorf("expected header row, got: %s", md)
	}
	if !strings.Contains(md, "| 1 | 2 |") {
		t.Errorf("expected data row, got: %s", md)
	}
}

func TestTableExtractionPrompt(t *testing.T) {
	prompt := tableExtractionPrompt(5)
	if !strings.Contains(prompt, "page 5") {
		t.Error("expected page number in prompt")
	}
	if !strings.Contains(prompt, "table") {
		t.Error("expected table keyword in prompt")
	}
}

func TestExtractTables_NoModel(t *testing.T) {
	p := &Processor{}
	_, err := p.ExtractTables("/fake.png", 1)
	if err == nil {
		t.Error("expected error with no model")
	}
}

// ── Chart Extraction Tests ─────────────────────────────────────

func TestParseChartsFromDescription_NoCharts(t *testing.T) {
	charts := parseChartsFromDescription("NO_CHARTS_FOUND")
	if len(charts) != 0 {
		t.Errorf("expected 0 charts, got %d", len(charts))
	}
}

func TestParseChartsFromDescription_Simple(t *testing.T) {
	desc := `CHART_TYPE: bar
CHART_TITLE: Revenue by Quarter
CHART_DESCRIPTION: Bar chart showing quarterly revenue
DATA_POINTS:
- Q1: 100.5
- Q2: 150.3
- Q3: 200.0
LABELS: Q1, Q2, Q3, Q4
---`

	charts := parseChartsFromDescription(desc)
	if len(charts) != 1 {
		t.Fatalf("expected 1 chart, got %d", len(charts))
	}
	if charts[0].Type != "bar" {
		t.Errorf("expected type 'bar', got %q", charts[0].Type)
	}
	if charts[0].Title != "Revenue by Quarter" {
		t.Errorf("expected title, got %q", charts[0].Title)
	}
	if len(charts[0].DataPoints) != 3 {
		t.Errorf("expected 3 data points, got %d", len(charts[0].DataPoints))
	}
	if len(charts[0].Labels) != 4 {
		t.Errorf("expected 4 labels, got %d", len(charts[0].Labels))
	}
}

func TestChartExtractionPrompt(t *testing.T) {
	prompt := chartExtractionPrompt(3)
	if !strings.Contains(prompt, "page 3") {
		t.Error("expected page number")
	}
	if !strings.Contains(prompt, "chart") {
		t.Error("expected chart keyword")
	}
}

func TestExtractCharts_NoModel(t *testing.T) {
	p := &Processor{}
	_, err := p.ExtractCharts("/fake.png", 1)
	if err == nil {
		t.Error("expected error with no model")
	}
}

// ── Content Faithfulness Tests ─────────────────────────────────

func TestCheckFaithfulness_Perfect(t *testing.T) {
	source := "# Introduction\n\nThis document discusses Project Alpha with a budget of 42500."
	extracted := "# Introduction\n\nThis document discusses Project Alpha with a budget of 42500."

	result := CheckFaithfulness(source, extracted)
	if result.Score < 80 {
		t.Errorf("expected high faithfulness, got %.1f", result.Score)
	}
	if result.OmissionCount > 0 {
		t.Errorf("expected no omissions, got %d", result.OmissionCount)
	}
}

func TestCheckFaithfulness_WithOmissions(t *testing.T) {
	source := "# Summary\n\nThe total revenue was 1500000 in FY2024."
	extracted := "Some unrelated text that misses the key points."

	result := CheckFaithfulness(source, extracted)
	if result.OmissionCount == 0 {
		t.Error("expected omissions")
	}
}

func TestExtractHeadings(t *testing.T) {
	text := "# Title\n## Section 1\nSome text\n### Sub Section\n"
	headings := extractHeadings(text)
	if len(headings) != 3 {
		t.Errorf("expected 3 headings, got %d: %v", len(headings), headings)
	}
}

func TestExtractNumbers(t *testing.T) {
	text := "The budget is 42500 and growth rate is 3.14 percent."
	numbers := extractNumbers(text)
	if len(numbers) < 2 {
		t.Errorf("expected at least 2 numbers, got %d: %v", len(numbers), numbers)
	}
}

func TestExtractKeyTerms(t *testing.T) {
	text := "Project Alpha launched in January. Product Beta was released in March."
	terms := extractKeyTerms(text)
	if len(terms) == 0 {
		t.Error("expected some key terms")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("short string should not be truncated")
	}
	result := truncate("this is a longer string", 10)
	if len(result) > 14 { // 10 + "..."
		t.Errorf("expected truncated, got: %s", result)
	}
}
