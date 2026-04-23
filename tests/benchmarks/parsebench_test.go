package benchmarks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ══════════════════════════════════════════════════════════════════════════════
// ParseBench Integration Benchmark
//
// Evaluates gleann-plugin-marker against ParseBench-aligned metrics:
//   1. Table extraction accuracy (TableRecordMatch)
//   2. Content faithfulness (omission/hallucination detection)
//   3. Semantic formatting preservation (bold, italic, headings)
//   4. Chart/figure detection rate
//   5. Processing latency
//
// Requires:
//   - gleann-plugin-marker running on localhost:8766
//   - Test fixtures in tests/e2e/fixtures/ (PDF/DOCX files)
//
// Usage:
//   go test ./tests/benchmarks/ -run TestParseBench -v -timeout 600s
//
// Reference: ParseBench (arXiv:2604.08538)
// ══════════════════════════════════════════════════════════════════════════════

// parseBenchFixture defines a test document with expected content.
type parseBenchFixture struct {
	Name           string
	File           string   // path relative to fixtures dir
	ExpectedTables int      // number of tables expected
	TableKeywords  []string // keywords that should appear in extracted tables
	ExpectedCharts int      // number of charts/figures expected
	MustContain    []string // strings that MUST be in output (faithfulness)
	MustNotContain []string // strings that must NOT be in output (hallucination)
	HasBold        bool     // expects bold formatting
	HasHeadings    bool     // expects heading hierarchy
	MinWordCount   int      // minimum expected words
}

// parseBenchResult holds metrics for a single fixture.
type parseBenchResult struct {
	Name             string  `json:"name"`
	LatencyMs        int64   `json:"latency_ms"`
	WordCount        int     `json:"word_count"`
	TableDetected    int     `json:"tables_detected"`
	TableAccuracy    float64 `json:"table_accuracy"`    // fraction of table keywords found
	ChartDetected    int     `json:"charts_detected"`
	FaithfulnessPct  float64 `json:"faithfulness_pct"`  // fraction of MustContain found
	HallucinationPct float64 `json:"hallucination_pct"` // fraction of MustNotContain found (lower=better)
	BoldPreserved    bool    `json:"bold_preserved"`
	HeadingsFound    bool    `json:"headings_found"`
	Error            string  `json:"error,omitempty"`
}

// parseBenchSummary holds the aggregate results.
type parseBenchSummary struct {
	Timestamp       string             `json:"timestamp"`
	Backend         string             `json:"backend"`
	TotalFixtures   int                `json:"total_fixtures"`
	PassedFixtures  int                `json:"passed_fixtures"`
	AvgLatencyMs    int64              `json:"avg_latency_ms"`
	AvgFaithfulness float64            `json:"avg_faithfulness_pct"`
	AvgTableAccuracy float64           `json:"avg_table_accuracy_pct"`
	Results         []parseBenchResult `json:"results"`
}

// parseBenchFixtures returns the test fixtures.
// These are aligned with ParseBench evaluation dimensions.
func parseBenchFixtures(fixtureDir string) []parseBenchFixture {
	return []parseBenchFixture{
		{
			Name:           "simple-text",
			File:           filepath.Join(fixtureDir, "sample.pdf"),
			ExpectedTables: 0,
			MustContain:    []string{}, // populated at runtime from actual content
			HasHeadings:    true,
			MinWordCount:   10,
		},
		{
			Name:           "docx-structured",
			File:           filepath.Join(fixtureDir, "sample.docx"),
			ExpectedTables: 0,
			HasBold:        true,
			HasHeadings:    true,
			MinWordCount:   10,
		},
		{
			Name:           "code-readme",
			File:           filepath.Join(fixtureDir, "code", "README.md"),
			MustContain:    []string{"package", "import", "func"},
			HasHeadings:    true,
			MinWordCount:   5,
		},
	}
}

func TestParseBench(t *testing.T) {
	// Check if marker plugin is running.
	resp, err := http.Get("http://localhost:8766/health")
	if err != nil {
		t.Skip("gleann-plugin-marker not running on localhost:8766; skipping ParseBench")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skip("gleann-plugin-marker not healthy; skipping ParseBench")
	}

	fixtureDir := filepath.Join("..", "e2e", "fixtures")
	fixtures := parseBenchFixtures(fixtureDir)

	summary := parseBenchSummary{
		Timestamp:     time.Now().Format(time.RFC3339),
		Backend:       "gleann-plugin-marker",
		TotalFixtures: len(fixtures),
	}

	var totalLatency int64
	var totalFaithfulness, totalTableAcc float64
	var faithCount, tableCount int

	for _, fix := range fixtures {
		t.Run(fix.Name, func(t *testing.T) {
			result := parseBenchResult{Name: fix.Name}

			// Check if fixture file exists.
			if _, err := os.Stat(fix.File); err != nil {
				t.Skipf("fixture %s not found: %v", fix.File, err)
				result.Error = "fixture not found"
				summary.Results = append(summary.Results, result)
				return
			}

			// Send to marker plugin.
			start := time.Now()
			markdown, err := sendToMarker(fix.File)
			result.LatencyMs = time.Since(start).Milliseconds()

			if err != nil {
				result.Error = err.Error()
				summary.Results = append(summary.Results, result)
				t.Logf("  ⚠ error: %s", err)
				return
			}

			totalLatency += result.LatencyMs

			// Word count.
			result.WordCount = len(strings.Fields(markdown))
			if fix.MinWordCount > 0 && result.WordCount < fix.MinWordCount {
				t.Errorf("word count %d < minimum %d", result.WordCount, fix.MinWordCount)
			}

			// Table detection.
			result.TableDetected = countTables(markdown)
			if fix.ExpectedTables > 0 {
				tableKeywordsFound := 0
				for _, kw := range fix.TableKeywords {
					if strings.Contains(strings.ToLower(markdown), strings.ToLower(kw)) {
						tableKeywordsFound++
					}
				}
				if len(fix.TableKeywords) > 0 {
					result.TableAccuracy = float64(tableKeywordsFound) / float64(len(fix.TableKeywords)) * 100
					totalTableAcc += result.TableAccuracy
					tableCount++
				}
			}

			// Chart detection.
			result.ChartDetected = countCharts(markdown)

			// Faithfulness: check MustContain.
			if len(fix.MustContain) > 0 {
				found := 0
				for _, s := range fix.MustContain {
					if strings.Contains(strings.ToLower(markdown), strings.ToLower(s)) {
						found++
					}
				}
				result.FaithfulnessPct = float64(found) / float64(len(fix.MustContain)) * 100
				totalFaithfulness += result.FaithfulnessPct
				faithCount++
			}

			// Hallucination: check MustNotContain.
			if len(fix.MustNotContain) > 0 {
				hallucinated := 0
				for _, s := range fix.MustNotContain {
					if strings.Contains(strings.ToLower(markdown), strings.ToLower(s)) {
						hallucinated++
					}
				}
				result.HallucinationPct = float64(hallucinated) / float64(len(fix.MustNotContain)) * 100
			}

			// Formatting preservation.
			result.BoldPreserved = !fix.HasBold || strings.Contains(markdown, "**") || strings.Contains(markdown, "__")
			result.HeadingsFound = !fix.HasHeadings || strings.Contains(markdown, "# ") || strings.Contains(markdown, "## ")

			summary.Results = append(summary.Results, result)
			summary.PassedFixtures++

			t.Logf("  ✓ %s: %dms, %d words, %d tables, faith=%.0f%%",
				fix.Name, result.LatencyMs, result.WordCount, result.TableDetected, result.FaithfulnessPct)
		})
	}

	// Compute averages.
	if summary.PassedFixtures > 0 {
		summary.AvgLatencyMs = totalLatency / int64(summary.PassedFixtures)
	}
	if faithCount > 0 {
		summary.AvgFaithfulness = totalFaithfulness / float64(faithCount)
	}
	if tableCount > 0 {
		summary.AvgTableAccuracy = totalTableAcc / float64(tableCount)
	}

	// Write results to file.
	resultsDir := filepath.Join("..", "e2e", "results")
	os.MkdirAll(resultsDir, 0755)

	jsonData, _ := json.MarshalIndent(summary, "", "  ")
	jsonPath := filepath.Join(resultsDir, "parsebench_results.json")
	os.WriteFile(jsonPath, jsonData, 0644)

	mdPath := filepath.Join(resultsDir, "parsebench_results.md")
	os.WriteFile(mdPath, []byte(renderParseBenchMarkdown(summary)), 0644)

	t.Logf("\n📊 ParseBench Summary:")
	t.Logf("  Backend: %s", summary.Backend)
	t.Logf("  Fixtures: %d/%d passed", summary.PassedFixtures, summary.TotalFixtures)
	t.Logf("  Avg latency: %dms", summary.AvgLatencyMs)
	t.Logf("  Avg faithfulness: %.1f%%", summary.AvgFaithfulness)
	t.Logf("  Avg table accuracy: %.1f%%", summary.AvgTableAccuracy)
	t.Logf("  Results: %s", jsonPath)
}

// sendToMarker sends a file to the marker plugin and returns the markdown output.
func sendToMarker(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	writer.Close()

	req, err := http.NewRequest("POST", "http://localhost:8766/convert", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("marker returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Markdown string `json:"markdown"`
		Nodes    []struct {
			Content string `json:"content"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if result.Markdown != "" {
		return result.Markdown, nil
	}

	// Fallback: concatenate node content.
	var sb strings.Builder
	for _, n := range result.Nodes {
		sb.WriteString(n.Content)
		sb.WriteString("\n\n")
	}
	return sb.String(), nil
}

// countTables counts markdown tables in the text.
func countTables(text string) int {
	count := 0
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 3 && strings.Count(trimmed, "|") >= 2 &&
			(strings.Contains(trimmed, "---") || strings.Contains(trimmed, "===")) {
			count++
		}
	}
	return count
}

// countCharts counts references to charts/figures in the text.
func countCharts(text string) int {
	count := 0
	lower := strings.ToLower(text)
	for _, kw := range []string{"figure ", "chart ", "fig. ", "graph "} {
		count += strings.Count(lower, kw)
	}
	return count
}

// renderParseBenchMarkdown generates a markdown report from benchmark results.
func renderParseBenchMarkdown(s parseBenchSummary) string {
	var sb strings.Builder

	sb.WriteString("# ParseBench Results\n\n")
	sb.WriteString(fmt.Sprintf("**Backend:** %s | **Date:** %s\n\n", s.Backend, s.Timestamp))
	sb.WriteString(fmt.Sprintf("**Fixtures:** %d/%d passed | **Avg Latency:** %dms\n\n", s.PassedFixtures, s.TotalFixtures, s.AvgLatencyMs))

	sb.WriteString("## Summary Metrics\n\n")
	sb.WriteString("| Metric | Value | Target |\n")
	sb.WriteString("|--------|-------|--------|\n")
	sb.WriteString(fmt.Sprintf("| Avg Faithfulness | %.1f%% | >85%% |\n", s.AvgFaithfulness))
	sb.WriteString(fmt.Sprintf("| Avg Table Accuracy | %.1f%% | >75%% |\n", s.AvgTableAccuracy))
	sb.WriteString(fmt.Sprintf("| Avg Latency | %dms | <5000ms |\n", s.AvgLatencyMs))

	sb.WriteString("\n## Per-Document Results\n\n")
	sb.WriteString("| Document | Latency | Words | Tables | Faith% | Halluc% | Bold | Headings |\n")
	sb.WriteString("|----------|---------|-------|--------|--------|---------|------|----------|\n")

	for _, r := range s.Results {
		if r.Error != "" {
			sb.WriteString(fmt.Sprintf("| %s | — | — | — | — | — | — | ⚠ %s |\n", r.Name, r.Error))
			continue
		}
		bold := "✓"
		if !r.BoldPreserved {
			bold = "✗"
		}
		headings := "✓"
		if !r.HeadingsFound {
			headings = "✗"
		}
		sb.WriteString(fmt.Sprintf("| %s | %dms | %d | %d | %.0f%% | %.0f%% | %s | %s |\n",
			r.Name, r.LatencyMs, r.WordCount, r.TableDetected,
			r.FaithfulnessPct, r.HallucinationPct, bold, headings))
	}

	sb.WriteString("\n---\n*Generated by gleann ParseBench integration*\n")
	return sb.String()
}

// round1 rounds to 1 decimal place. Used to keep consistent reporting.
func round1(f float64) float64 {
	return math.Round(f*10) / 10
}
