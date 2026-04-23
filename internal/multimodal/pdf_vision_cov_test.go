package multimodal

import (
	"strings"
	"testing"
)

// ── pdfPagePrompt additional coverage ─────────────────────────

func TestPdfPagePromptCov_WithMarkerDetail(t *testing.T) {
	p := pdfPagePrompt(3, "Some marker extracted text here.")
	if !strings.Contains(p, "page 3") {
		t.Error("expected page number in prompt")
	}
	if !strings.Contains(p, "Some marker extracted text") {
		t.Error("expected marker text in prompt")
	}
}

// ── parseTablesFromMarkdown edge cases ────────────────────────

func TestParseTablesFromMarkdown_SingleTableCov(t *testing.T) {
	md := `Here is a table:

| Name | Age | City |
|------|-----|------|
| Alice | 30 | NYC |
| Bob   | 25 | LA  |

Some text after.`

	tables := parseTablesFromMarkdown(md)
	if len(tables) == 0 {
		t.Fatal("expected at least 1 table")
	}
	tbl := tables[0]
	if len(tbl.Headers) < 3 {
		t.Errorf("expected 3 headers, got %d: %v", len(tbl.Headers), tbl.Headers)
	}
	if len(tbl.Rows) < 2 {
		t.Errorf("expected 2 rows, got %d", len(tbl.Rows))
	}
}

func TestParseTablesFromMarkdown_NoTablesCov(t *testing.T) {
	md := "This is just plain text without any tables."
	tables := parseTablesFromMarkdown(md)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables, got %d", len(tables))
	}
}

func TestParseTablesFromMarkdown_NoTablesKeyword(t *testing.T) {
	md := "NO_TABLES_FOUND"
	tables := parseTablesFromMarkdown(md)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables, got %d", len(tables))
	}
}

// ── splitTableRow edge cases ──────────────────────────────────

func TestSplitTableRow_Empty(t *testing.T) {
	cols := splitTableRow("")
	// strings.Split("", "|") returns [""], so 1 element
	if len(cols) != 1 {
		t.Errorf("expected 1 column, got %d", len(cols))
	}
}

// ── parseChartsFromDescription additional coverage ────────────

func TestParseChartsFromDescription_SingleCov(t *testing.T) {
	desc := `CHART_TYPE: bar
CHART_TITLE: Revenue by Quarter
CHART_DESCRIPTION: Shows quarterly revenue growth
DATA_POINTS:
- Q1: 100
- Q2: 150
- Q3: 200
LABELS: Quarter, Revenue ($)
---`

	charts := parseChartsFromDescription(desc)
	if len(charts) == 0 {
		t.Fatal("expected at least 1 chart")
	}
	c := charts[0]
	if c.Type != "bar" {
		t.Errorf("expected type=bar, got %s", c.Type)
	}
	if c.Title != "Revenue by Quarter" {
		t.Errorf("expected title='Revenue by Quarter', got '%s'", c.Title)
	}
	if len(c.DataPoints) < 2 {
		t.Errorf("expected data points, got %d", len(c.DataPoints))
	}
}

func TestParseChartsFromDescription_Multiple(t *testing.T) {
	desc := `CHART_TYPE: line
CHART_TITLE: Temperature
CHART_DESCRIPTION: Monthly temps
DATA_POINTS:
- Jan: 30
- Feb: 35
LABELS: Month, Temp
---
CHART_TYPE: pie
CHART_TITLE: Market Share
CHART_DESCRIPTION: Company market share
DATA_POINTS:
- Company A: 40
- Company B: 60
LABELS: Company, Share
---`

	charts := parseChartsFromDescription(desc)
	if len(charts) < 2 {
		t.Fatalf("expected 2 charts, got %d", len(charts))
	}
	if charts[0].Type != "line" {
		t.Errorf("first chart type: %s", charts[0].Type)
	}
	if charts[1].Type != "pie" {
		t.Errorf("second chart type: %s", charts[1].Type)
	}
}

// ── extractHeadings edge case ─────────────────────────────────

func TestExtractHeadings_None(t *testing.T) {
	headings := extractHeadings("Plain text with no headings.")
	if len(headings) != 0 {
		t.Errorf("expected 0 headings, got %d", len(headings))
	}
}

// ── extractNumbers edge case ──────────────────────────────────

func TestExtractNumbers_None(t *testing.T) {
	nums := extractNumbers("No numbers here at all.")
	if len(nums) != 0 {
		t.Errorf("expected 0 numbers, got %d", len(nums))
	}
}

// ── extractSentences edge case ────────────────────────────────

func TestExtractSentences_Short(t *testing.T) {
	sents := extractSentences("Hi. Ok.")
	if len(sents) != 0 {
		t.Errorf("expected 0 sentences (all too short), got %d", len(sents))
	}
}

func TestExtractSentences_MultipleCov(t *testing.T) {
	text := "This is the first sentence. Here is the second one. And a third sentence follows."
	sents := extractSentences(text)
	if len(sents) < 2 {
		t.Fatalf("expected at least 2 sentences, got %d", len(sents))
	}
}

// ── truncate edge case ────────────────────────────────────────

func TestTruncate_Exact(t *testing.T) {
	if truncate("12345", 5) != "12345" {
		t.Error("exact-length string should not be truncated")
	}
}

// ── CheckFaithfulness additional ──────────────────────────────

func TestCheckFaithfulness_EmptyInputs(t *testing.T) {
	result := CheckFaithfulness("", "")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Empty text → 0 checks
	if result.TotalChecks != 0 {
		t.Errorf("expected 0 checks for empty input, got %d", result.TotalChecks)
	}
}

func TestCheckFaithfulness_Hallucination(t *testing.T) {
	source := "# Introduction\nThe project uses Go language for development."
	extracted := "# Introduction\nThe project uses Go language for development. " +
		"Quantum computing revolutionizes blockchain neural networks with unprecedented efficiency."
	result := CheckFaithfulness(source, extracted)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HallucinCount == 0 {
		t.Error("expected at least 1 hallucination detected")
	}
}
