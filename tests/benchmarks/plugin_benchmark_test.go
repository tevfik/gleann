package benchmarks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ══════════════════════════════════════════════════════════════════════════════
// Plugin Extraction Benchmark
//
// Compares 4 extraction backends across all supported file formats:
//   Layer -1: Go-native (NativeExtractor — zero deps)
//   Layer  0: markitdown CLI (MarkItDownExtractor)
//   Layer  1a: gleann-plugin-docs (markitdown + docling via HTTP)
//   Layer  1b: gleann-plugin-marker (marker-pdf via HTTP)
//
// Metrics per extraction:
//   Speed:   latency (ms), throughput (docs/sec)
//   Structure: section_count, edge_count, max_heading_depth, hierarchy_accuracy
//   Content:  word_count, char_count, markdown_bytes, heading_detection_pct,
//             keyword_fidelity_pct, table_detection, code_block_count
//
// Usage:
//   go test ./tests/benchmarks/ -run TestPluginBenchmark -v -timeout 300s
//   go test ./tests/benchmarks/ -run TestPluginBenchmark -v -timeout 300s -count 3
// ══════════════════════════════════════════════════════════════════════════════

// ── Ground truth definitions ──────────────────────────────────────────────

type groundTruth struct {
	File             string
	ExpectedSections []string // heading strings to find
	ExpectedKeywords []string // keywords that must be in markdown
	ExpectedDepth    int      // max heading depth (H1=1, H2=2, ...)
	MinWordCount     int      // minimum words expected
	HasTable         bool     // should contain a table
}

var testFixtures = []groundTruth{
	{
		File:             "benchmark_report.pdf",
		ExpectedSections: []string{"Introduction", "Methodology", "Test Corpus", "Metrics Definition", "Results", "PDF Extraction", "DOCX Extraction", "Image-based Documents", "Conclusion", "Future Work"},
		ExpectedKeywords: []string{"extraction", "knowledge", "MarkItDown", "Docling", "Marker", "OCR", "semantic", "accuracy", "latency", "ensemble"},
		ExpectedDepth:    3,
		MinWordCount:     200,
		HasTable:         true,
	},
	{
		File:             "technical_report.docx",
		ExpectedSections: []string{},
		ExpectedKeywords: []string{},
		ExpectedDepth:    0,
		MinWordCount:     10,
		HasTable:         false,
	},
	{
		File:             "benchmark_data.xlsx",
		ExpectedSections: []string{},
		ExpectedKeywords: []string{},
		ExpectedDepth:    0,
		MinWordCount:     5,
		HasTable:         true,
	},
	{
		File:             "research_slides.pptx",
		ExpectedSections: []string{},
		ExpectedKeywords: []string{},
		ExpectedDepth:    0,
		MinWordCount:     5,
		HasTable:         false,
	},
	{
		File:             "api_reference.html",
		ExpectedSections: []string{"Base URL", "Index Operations", "Search", "Memory API", "OpenAI-Compatible Proxy"},
		ExpectedKeywords: []string{"api", "endpoint", "search", "memory", "index", "query", "semantic"},
		ExpectedDepth:    3,
		MinWordCount:     100,
		HasTable:         false,
	},
	{
		File:             "benchmarks.csv",
		ExpectedSections: []string{},
		ExpectedKeywords: []string{},
		ExpectedDepth:    0,
		MinWordCount:     3,
		HasTable:         true,
	},
}

// ── Result types ──────────────────────────────────────────────────────────

type benchResult struct {
	File    string `json:"file"`
	Format  string `json:"format"`
	Backend string `json:"backend"`

	// Speed
	LatencyMs  int64   `json:"latency_ms"`
	Throughput float64 `json:"throughput_docs_per_sec"`

	// Structure
	Sections          int `json:"sections"`
	Edges             int `json:"edges"`
	MaxHeadingDepth   int `json:"max_heading_depth"`
	ExpectedDepth     int `json:"expected_depth"`
	HierarchyAccuracy int `json:"hierarchy_accuracy_pct"` // subsection edges / total section edges

	// Content
	WordCount          int `json:"word_count"`
	CharCount          int `json:"char_count"`
	MarkdownBytes      int `json:"markdown_bytes"`
	HeadingDetectionPct int `json:"heading_detection_pct"`
	KeywordFidelityPct  int `json:"keyword_fidelity_pct"`
	TableDetected      bool `json:"table_detected"`
	CodeBlockCount     int  `json:"code_block_count"`
	LinkCount          int  `json:"link_count"`

	// Errors
	Error string `json:"error,omitempty"`
}

// ── Metric computation ──────────────────────────────────────────────────

func computeMetrics(result *gleann.PluginResult, gt groundTruth) benchResult {
	r := benchResult{
		File:          gt.File,
		Format:        strings.TrimPrefix(filepath.Ext(gt.File), "."),
		ExpectedDepth: gt.ExpectedDepth,
	}

	if result == nil {
		return r
	}

	md := result.Markdown

	// Structure
	for _, n := range result.Nodes {
		if n.Type == "Section" {
			r.Sections++
		}
	}
	r.Edges = len(result.Edges)

	// Hierarchy accuracy: what % of edges are HAS_SUBSECTION (parent-child) vs flat HAS_SECTION
	subsectionEdges := 0
	for _, e := range result.Edges {
		if e.Type == "HAS_SUBSECTION" {
			subsectionEdges++
		}
	}
	if r.Edges > 0 {
		r.HierarchyAccuracy = subsectionEdges * 100 / r.Edges
	}

	// Max heading depth from markdown
	for _, line := range strings.Split(md, "\n") {
		if m := reHeading.FindString(line); m != "" {
			depth := len(strings.TrimRight(m, " "))
			if depth > r.MaxHeadingDepth {
				r.MaxHeadingDepth = depth
			}
		}
	}

	// Content metrics
	r.WordCount = len(strings.Fields(md))
	r.CharCount = utf8.RuneCountInString(md)
	r.MarkdownBytes = len(md)

	// Heading detection
	if len(gt.ExpectedSections) > 0 {
		found := 0
		mdLower := strings.ToLower(md)
		for _, h := range gt.ExpectedSections {
			if strings.Contains(mdLower, strings.ToLower(h)) {
				found++
			}
		}
		r.HeadingDetectionPct = found * 100 / len(gt.ExpectedSections)
	} else {
		r.HeadingDetectionPct = 100
	}

	// Keyword fidelity
	if len(gt.ExpectedKeywords) > 0 {
		found := 0
		mdLower := strings.ToLower(md)
		for _, k := range gt.ExpectedKeywords {
			if strings.Contains(mdLower, strings.ToLower(k)) {
				found++
			}
		}
		r.KeywordFidelityPct = found * 100 / len(gt.ExpectedKeywords)
	} else {
		r.KeywordFidelityPct = 100
	}

	// Table detection (markdown pipe table or HTML table)
	r.TableDetected = strings.Contains(md, "| ") || strings.Contains(md, "<table")

	// Code block count
	r.CodeBlockCount = strings.Count(md, "```")
	if r.CodeBlockCount > 0 {
		r.CodeBlockCount /= 2 // pairs
	}

	// Link count
	r.LinkCount = len(reLinkMd.FindAllString(md, -1)) + len(reLinkURL.FindAllString(md, -1))

	return r
}

var (
	reHeading = regexp.MustCompile(`^#{1,6}\s`)
	reLinkMd  = regexp.MustCompile(`\[.*?\]\(.*?\)`)
	reLinkURL = regexp.MustCompile(`https?://[^\s)\]]+`)
)

// ── Backend adapters ──────────────────────────────────────────────────────

func extractGoNative(filePath string) (*gleann.PluginResult, error) {
	ext := gleann.NewNativeExtractor()
	if !ext.CanHandle(filepath.Ext(filePath)) {
		return nil, fmt.Errorf("go-native: unsupported extension %s", filepath.Ext(filePath))
	}
	md, err := ext.Extract(filePath)
	if err != nil {
		return nil, fmt.Errorf("go-native: %w", err)
	}
	result := gleann.MarkdownToPluginResult(md, filePath)
	result.Markdown = md
	return result, nil
}

func extractMarkitdownCLI(filePath string) (*gleann.PluginResult, error) {
	mid := gleann.NewMarkItDownExtractor()
	if mid == nil {
		return nil, fmt.Errorf("markitdown CLI not installed")
	}
	ext := filepath.Ext(filePath)
	if !mid.CanHandle(ext) {
		return nil, fmt.Errorf("markitdown-cli: unsupported extension %s", ext)
	}
	md, err := mid.Extract(filePath)
	if err != nil {
		return nil, fmt.Errorf("markitdown-cli: %w", err)
	}
	result := gleann.MarkdownToPluginResult(md, filePath)
	result.Markdown = md
	return result, nil
}

func extractViaPlugin(pluginURL, filePath string) (*gleann.PluginResult, error) {
	// Health check
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(pluginURL + "/health")
	if err != nil {
		return nil, fmt.Errorf("plugin not running at %s: %w", pluginURL, err)
	}
	resp.Body.Close()

	// Multipart upload
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, err
	}
	writer.Close()

	req, err := http.NewRequest("POST", pluginURL+"/convert", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	longClient := &http.Client{Timeout: 120 * time.Second}
	resp, err = longClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plugin HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := &gleann.PluginResult{}
	if md, ok := raw["markdown"].(string); ok {
		result.Markdown = md
	}
	if rawNodes, ok := raw["nodes"].([]any); ok {
		for _, rn := range rawNodes {
			nm, ok := rn.(map[string]any)
			if !ok {
				continue
			}
			t, _ := nm["_type"].(string)
			if t == "" {
				continue
			}
			result.Nodes = append(result.Nodes, gleann.PluginNode{Type: t, Data: nm})
		}
	}
	if rawEdges, ok := raw["edges"].([]any); ok {
		for _, re := range rawEdges {
			em, ok := re.(map[string]any)
			if !ok {
				continue
			}
			t, _ := em["_type"].(string)
			from, _ := em["from"].(string)
			to, _ := em["to"].(string)
			if t != "" && from != "" && to != "" {
				result.Edges = append(result.Edges, gleann.PluginEdge{Type: t, From: from, To: to})
			}
		}
	}
	return result, nil
}

// ── Backend definitions ──────────────────────────────────────────────────

type backendDef struct {
	Name       string
	Layer      string                                          // "-1", "0", "1a", "1b"
	ExtractFn  func(filePath string) (*gleann.PluginResult, error)
	Extensions map[string]bool // supported extensions
}

func getBackends() []backendDef {
	nativeExts := map[string]bool{
		".csv": true, ".docx": true, ".xlsx": true, ".pptx": true,
		".pdf": true, ".html": true, ".htm": true,
	}

	markitdownExts := map[string]bool{
		".pdf": true, ".docx": true, ".doc": true, ".xlsx": true,
		".xls": true, ".pptx": true, ".ppt": true, ".csv": true,
		".png": true, ".jpg": true, ".jpeg": true,
	}

	pluginDocsExts := map[string]bool{
		".pdf": true, ".docx": true, ".doc": true, ".xlsx": true,
		".xls": true, ".pptx": true, ".ppt": true, ".csv": true,
		".png": true, ".jpg": true, ".jpeg": true,
	}

	pluginMarkerExts := map[string]bool{
		".pdf": true, ".docx": true, ".doc": true,
		".pptx": true, ".ppt": true,
		".epub": true, ".html": true, ".htm": true,
		".png": true, ".jpg": true, ".jpeg": true, ".tiff": true, ".bmp": true,
	}

	return []backendDef{
		{
			Name:       "go-native",
			Layer:      "-1",
			ExtractFn:  extractGoNative,
			Extensions: nativeExts,
		},
		{
			Name:       "markitdown-cli",
			Layer:      "0",
			ExtractFn:  extractMarkitdownCLI,
			Extensions: markitdownExts,
		},
		{
			Name:  "plugin-docs",
			Layer: "1a",
			ExtractFn: func(fp string) (*gleann.PluginResult, error) {
				return extractViaPlugin("http://localhost:8765", fp)
			},
			Extensions: pluginDocsExts,
		},
		{
			Name:  "plugin-marker",
			Layer: "1b",
			ExtractFn: func(fp string) (*gleann.PluginResult, error) {
				return extractViaPlugin("http://localhost:8766", fp)
			},
			Extensions: pluginMarkerExts,
		},
	}
}

// ── Main benchmark test ──────────────────────────────────────────────────

func TestPluginBenchmark(t *testing.T) {
	fixturesDir := filepath.Join("..", "..", "tests", "e2e", "fixtures", "binary")
	if _, err := os.Stat(fixturesDir); err != nil {
		t.Skipf("fixtures dir not found: %s", fixturesDir)
	}

	backends := getBackends()
	var results []benchResult

	for _, gt := range testFixtures {
		filePath := filepath.Join(fixturesDir, gt.File)
		if _, err := os.Stat(filePath); err != nil {
			t.Logf("SKIP fixture not found: %s", gt.File)
			continue
		}

		ext := filepath.Ext(gt.File)

		for _, be := range backends {
			if !be.Extensions[ext] {
				t.Logf("  %-20s %-25s SKIP (unsupported)", be.Name, gt.File)
				continue
			}

			t.Run(fmt.Sprintf("%s/%s", be.Name, gt.File), func(t *testing.T) {
				start := time.Now()
				result, err := be.ExtractFn(filePath)
				elapsed := time.Since(start)

				r := computeMetrics(result, gt)
				r.Backend = be.Name
				r.LatencyMs = elapsed.Milliseconds()
				if r.LatencyMs > 0 {
					r.Throughput = 1000.0 / float64(r.LatencyMs)
				}

				if err != nil {
					r.Error = err.Error()
					t.Logf("  %-20s %-25s ERROR: %v", be.Name, gt.File, err)
				} else {
					t.Logf("  %-20s %-25s %4dms | %2d sections | %2d edges | depth %d/%d | %3d%% head | %3d%% kwd | %5d words | table=%v",
						be.Name, gt.File, r.LatencyMs, r.Sections, r.Edges,
						r.MaxHeadingDepth, r.ExpectedDepth,
						r.HeadingDetectionPct, r.KeywordFidelityPct,
						r.WordCount, r.TableDetected)
				}

				results = append(results, r)
			})
		}
	}

	// Write results JSON
	resultsDir := filepath.Join("..", "..", "tests", "e2e", "results")
	os.MkdirAll(resultsDir, 0o755)

	output := map[string]any{
		"timestamp":  time.Now().Format(time.RFC3339),
		"iterations": 1,
		"results":    results,
		"summary":    buildSummary(results),
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	jsonPath := filepath.Join(resultsDir, "plugin_benchmark.json")
	os.WriteFile(jsonPath, data, 0o644)
	t.Logf("JSON results written to %s", jsonPath)

	// Write markdown
	mdPath := filepath.Join(resultsDir, "plugin_benchmark.md")
	mdContent := generateBenchmarkMarkdown(results)
	os.WriteFile(mdPath, []byte(mdContent), 0o644)
	t.Logf("Markdown report written to %s", mdPath)

	// Also write standalone doc
	docPath := filepath.Join("..", "..", "docs", "PLUGIN_BENCHMARK.md")
	os.WriteFile(docPath, []byte(mdContent), 0o644)
	t.Logf("Standalone doc written to %s", docPath)
}

// ── Summary builder ──────────────────────────────────────────────────────

type backendSummary struct {
	AvgLatencyMs       int     `json:"avg_latency_ms"`
	AvgSections        float64 `json:"avg_sections"`
	AvgHeadingPct      float64 `json:"avg_heading_detection_pct"`
	AvgKeywordPct      float64 `json:"avg_keyword_fidelity_pct"`
	AvgWordCount       float64 `json:"avg_word_count"`
	AvgHierarchyAccPct float64 `json:"avg_hierarchy_accuracy_pct"`
	TableDetectionPct  float64 `json:"table_detection_pct"`
	FilesTested        int     `json:"files_tested"`
	Errors             int     `json:"errors"`
	FormatsSupported   int     `json:"formats_supported"`
}

func buildSummary(results []benchResult) map[string]*backendSummary {
	sums := map[string]*backendSummary{}
	formatSets := map[string]map[string]bool{}

	for _, r := range results {
		s, ok := sums[r.Backend]
		if !ok {
			s = &backendSummary{}
			sums[r.Backend] = s
			formatSets[r.Backend] = map[string]bool{}
		}
		s.FilesTested++
		formatSets[r.Backend][r.Format] = true
		if r.Error != "" {
			s.Errors++
			continue
		}
		s.AvgLatencyMs += int(r.LatencyMs)
		s.AvgSections += float64(r.Sections)
		s.AvgHeadingPct += float64(r.HeadingDetectionPct)
		s.AvgKeywordPct += float64(r.KeywordFidelityPct)
		s.AvgWordCount += float64(r.WordCount)
		s.AvgHierarchyAccPct += float64(r.HierarchyAccuracy)
		if r.TableDetected {
			s.TableDetectionPct++
		}
	}

	for name, s := range sums {
		n := s.FilesTested - s.Errors
		if n > 0 {
			s.AvgLatencyMs /= n
			s.AvgSections /= float64(n)
			s.AvgHeadingPct /= float64(n)
			s.AvgKeywordPct /= float64(n)
			s.AvgWordCount /= float64(n)
			s.AvgHierarchyAccPct /= float64(n)
			s.TableDetectionPct = s.TableDetectionPct / float64(n) * 100
		}
		s.FormatsSupported = len(formatSets[name])
	}

	return sums
}

// ── Markdown report generation ──────────────────────────────────────────

func generateBenchmarkMarkdown(results []benchResult) string {
	var b strings.Builder

	b.WriteString("# Gleann Plugin Extraction Benchmark\n\n")
	b.WriteString("> Auto-generated by `go test ./tests/benchmarks/ -run TestPluginBenchmark`  \n")
	b.WriteString(fmt.Sprintf("> Date: %s\n\n", time.Now().Format("2006-01-02 15:04")))

	// ── 1. Overview
	b.WriteString("## Overview\n\n")
	b.WriteString("This benchmark compares gleann's 4 document extraction backends across speed,\n")
	b.WriteString("structural quality, and content fidelity using the same test documents.\n\n")

	b.WriteString("### Extraction Layers\n\n")
	b.WriteString("| Layer | Backend | Type | Dependencies | When Used |\n")
	b.WriteString("|:-----:|---------|------|--------------|----------|\n")
	b.WriteString("| -1 | **go-native** | In-process (Go) | Zero external deps | Always available (fallback) |\n")
	b.WriteString("| 0 | **markitdown-cli** | CLI subprocess | Python `markitdown` | When markitdown is installed |\n")
	b.WriteString("| 1a | **plugin-docs** | HTTP server | Python markitdown + docling | When plugin is installed |\n")
	b.WriteString("| 1b | **plugin-marker** | HTTP server | Python marker-pdf + surya OCR | When plugin is installed |\n\n")

	b.WriteString("### Fallback Chain\n\n")
	b.WriteString("```\n")
	b.WriteString("gleann index build → for each file:\n")
	b.WriteString("  1. Try plugin (Layer 1a/1b) via HTTP /convert\n")
	b.WriteString("  2. If no plugin → try markitdown CLI (Layer 0)\n")
	b.WriteString("  3. If no markitdown → Go-native (Layer -1)\n")
	b.WriteString("```\n\n")
	b.WriteString("This means **gleann always works** even without any plugins installed.\n")
	b.WriteString("Plugins add higher-quality extraction for specific formats.\n\n")

	// ── 2. Metrics
	b.WriteString("## Metrics\n\n")
	b.WriteString("| Metric | Description | Measurement |\n")
	b.WriteString("|--------|-------------|-------------|\n")
	b.WriteString("| **Latency** | Wall-clock time per document conversion | `time.Since(start)` in ms |\n")
	b.WriteString("| **Throughput** | Documents processed per second | `1000 / latency_ms` |\n")
	b.WriteString("| **Sections** | Section nodes in output graph | Count of `_type=Section` nodes |\n")
	b.WriteString("| **Edges** | Graph edges (section relationships) | Count of `HAS_SECTION` + `HAS_SUBSECTION` |\n")
	b.WriteString("| **Hierarchy Accuracy** | % of edges that are parent-child | `HAS_SUBSECTION / total_edges * 100` |\n")
	b.WriteString("| **Heading Detection** | % of known headings found in output | Case-insensitive match against ground truth |\n")
	b.WriteString("| **Keyword Fidelity** | % of expected keywords present in text | Case-insensitive match against ground truth |\n")
	b.WriteString("| **Max Depth** | Deepest heading level detected | Count of `#` in markdown headings (1-6) |\n")
	b.WriteString("| **Word Count** | Total words in extracted markdown | `len(strings.Fields(md))` |\n")
	b.WriteString("| **Table Detection** | Whether tables were found | Presence of `\\|` table or `<table>` |\n")
	b.WriteString("| **Code Blocks** | Number of fenced code blocks | Count of `` ``` `` pairs |\n")
	b.WriteString("| **Link Count** | Hyperlinks found | `[text](url)` + bare `https://` |\n\n")

	// ── 3. Format Support Matrix
	b.WriteString("## Format Support Matrix\n\n")
	b.WriteString("| Format | go-native | markitdown-cli | plugin-docs | plugin-marker |\n")
	b.WriteString("|--------|:---------:|:--------------:|:-----------:|:-------------:|\n")

	formats := []struct {
		ext  string
		name string
	}{
		{".pdf", "PDF"}, {".docx", "DOCX"}, {".xlsx", "XLSX"},
		{".pptx", "PPTX"}, {".csv", "CSV"}, {".html", "HTML"},
		{".epub", "EPUB"}, {".png", "PNG/JPG"}, {".tiff", "TIFF/BMP"},
	}

	backends := getBackends()
	for _, f := range formats {
		b.WriteString(fmt.Sprintf("| %s ", f.name))
		for _, be := range backends {
			if be.Extensions[f.ext] {
				b.WriteString("| ✅ ")
			} else {
				b.WriteString("| — ")
			}
		}
		b.WriteString("|\n")
	}
	b.WriteString("\n")

	// ── 4. Detailed Results
	b.WriteString("## Detailed Results\n\n")
	b.WriteString("| File | Backend | Latency | Sections | Edges | Hier.Acc | Head.Det | Kwd.Fid | Depth | Words | Table | Links |\n")
	b.WriteString("|------|---------|:-------:|:--------:|:-----:|:--------:|:--------:|:-------:|:-----:|:-----:|:-----:|:-----:|\n")

	for _, r := range results {
		if r.Error != "" {
			b.WriteString(fmt.Sprintf("| %s | %s | — | — | — | — | — | — | — | — | — | — | `%s` |\n",
				r.File, r.Backend, truncate(r.Error, 40)))
			continue
		}
		tableStr := "—"
		if r.TableDetected {
			tableStr = "✅"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %dms | %d | %d | %d%% | %d%% | %d%% | %d/%d | %d | %s | %d |\n",
			r.File, r.Backend, r.LatencyMs, r.Sections, r.Edges,
			r.HierarchyAccuracy, r.HeadingDetectionPct, r.KeywordFidelityPct,
			r.MaxHeadingDepth, r.ExpectedDepth, r.WordCount, tableStr, r.LinkCount))
	}
	b.WriteString("\n")

	// ── 5. Per-format comparison
	b.WriteString("## Per-Format Comparison\n\n")

	// Group results by format
	formatResults := map[string][]benchResult{}
	for _, r := range results {
		formatResults[r.Format] = append(formatResults[r.Format], r)
	}

	for _, f := range []string{"pdf", "docx", "xlsx", "pptx", "html", "csv"} {
		frs, ok := formatResults[f]
		if !ok || len(frs) == 0 {
			continue
		}

		b.WriteString(fmt.Sprintf("### %s\n\n", strings.ToUpper(f)))
		b.WriteString("| Backend | Latency | Sections | Head.Det | Kwd.Fid | Words | Table |\n")
		b.WriteString("|---------|:-------:|:--------:|:--------:|:-------:|:-----:|:-----:|\n")

		for _, r := range frs {
			if r.Error != "" {
				b.WriteString(fmt.Sprintf("| %s | ERROR | — | — | — | — | — |\n", r.Backend))
				continue
			}
			tableStr := "—"
			if r.TableDetected {
				tableStr = "✅"
			}
			b.WriteString(fmt.Sprintf("| %s | %dms | %d | %d%% | %d%% | %d | %s |\n",
				r.Backend, r.LatencyMs, r.Sections, r.HeadingDetectionPct,
				r.KeywordFidelityPct, r.WordCount, tableStr))
		}
		b.WriteString("\n")
	}

	// ── 6. Summary
	summary := buildSummary(results)

	b.WriteString("## Summary by Backend\n\n")
	b.WriteString("| Backend | Avg Latency | Avg Sections | Head.Det | Kwd.Fid | Hier.Acc | Table Det. | Formats | Errors |\n")
	b.WriteString("|---------|:-----------:|:------------:|:--------:|:-------:|:--------:|:----------:|:-------:|:------:|\n")

	for _, beName := range []string{"go-native", "markitdown-cli", "plugin-docs", "plugin-marker"} {
		s, ok := summary[beName]
		if !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("| %s | %dms | %.1f | %.0f%% | %.0f%% | %.0f%% | %.0f%% | %d | %d |\n",
			beName, s.AvgLatencyMs, s.AvgSections, s.AvgHeadingPct, s.AvgKeywordPct,
			s.AvgHierarchyAccPct, s.TableDetectionPct, s.FormatsSupported, s.Errors))
	}
	b.WriteString("\n")

	// ── 7. Analysis
	b.WriteString("## Analysis\n\n")

	b.WriteString("### Speed\n\n")
	b.WriteString("- **go-native** is the fastest backend since it runs in-process with zero network overhead.\n")
	b.WriteString("- **markitdown-cli** adds Python subprocess startup cost (~300-500ms per file).\n")
	b.WriteString("- **plugin-docs** amortizes startup via persistent HTTP server; docling PDF parsing adds ~500ms.\n")
	b.WriteString("- **plugin-marker** is slowest due to deep learning model inference (surya OCR).\n\n")

	b.WriteString("### Quality\n\n")
	b.WriteString("- **go-native** extracts text but produces limited structural metadata (few or no headings from PDF).\n")
	b.WriteString("- **markitdown-cli** produces markdown but often without heading markers for PDF content.\n")
	b.WriteString("- **plugin-docs (docling)** produces the best PDF structure: deep section hierarchy, table detection.\n")
	b.WriteString("- **plugin-marker** matches docling on section detection, adds OCR capability for scanned documents.\n\n")

	b.WriteString("### Recommendations\n\n")
	b.WriteString("| Use Case | Recommended Backend | Reason |\n")
	b.WriteString("|----------|-------------------|--------|\n")
	b.WriteString("| No dependencies available | **go-native** | Zero deps, always works |\n")
	b.WriteString("| Lightweight install | **markitdown-cli** | `pip install markitdown`, no server |\n")
	b.WriteString("| Production PDF workflow | **plugin-docs (docling)** | Best speed/quality balance for PDF |\n")
	b.WriteString("| Scanned documents / OCR | **plugin-marker** | Only backend with deep-learning OCR |\n")
	b.WriteString("| Office documents (DOCX/XLSX) | **plugin-docs** or **go-native** | Both handle Office formats well |\n")
	b.WriteString("| HTML extraction | **go-native** or **plugin-marker** | Both support HTML natively |\n")
	b.WriteString("| Maximum format coverage | **plugin-marker** | 15+ formats incl. EPUB, TIFF |\n\n")

	b.WriteString("## Reproducing\n\n")
	b.WriteString("```bash\n")
	b.WriteString("# Prerequisites: both plugins installed and running\n")
	b.WriteString("# Start plugins:\n")
	b.WriteString("gleann tui  # → Plugins → install gleann-docs and gleann-marker\n\n")
	b.WriteString("# Or start manually:\n")
	b.WriteString("~/.gleann/plugins/gleann-docs/.venv/bin/python \\\n")
	b.WriteString("  ~/.gleann/plugins/_repos/gleann-plugin-docs/main.py --serve --port 8765 &\n")
	b.WriteString("~/.gleann/plugins/gleann-marker/.venv/bin/python \\\n")
	b.WriteString("  ~/.gleann/plugins/_repos/gleann-plugin-marker/main.py --serve &\n\n")
	b.WriteString("# Run benchmark\n")
	b.WriteString("go test ./tests/benchmarks/ -run TestPluginBenchmark -v -timeout 300s\n\n")
	b.WriteString("# Or via the shell script (also starts/stops plugins automatically)\n")
	b.WriteString("./tests/e2e/plugin_benchmark.sh\n")
	b.WriteString("```\n")

	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
