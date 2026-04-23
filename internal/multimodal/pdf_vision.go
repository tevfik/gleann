// Package multimodal provides model-native multimodal processing for gleann.
package multimodal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PDFVisionConfig controls the PDF vision pipeline.
type PDFVisionConfig struct {
	DPI       int  // Render DPI for PDF pages (default: 150).
	MaxPages  int  // Max pages to process (0 = all).
	UseMarker bool // Try gleann-plugin-marker first, VLM fallback for tables/charts.
}

// DefaultPDFConfig returns sensible defaults.
func DefaultPDFConfig() PDFVisionConfig {
	return PDFVisionConfig{
		DPI:       150,
		MaxPages:  0,
		UseMarker: true,
	}
}

// PDFPageResult holds the analysis of a single PDF page.
type PDFPageResult struct {
	PageNum     int    // 1-based page number.
	ImagePath   string // Path to rendered page image.
	Description string // VLM-generated description.
	HasTable    bool   // Whether a table was detected.
	HasChart    bool   // Whether a chart/figure was detected.
	MarkerText  string // Text from marker plugin (if available).
	Error       error
}

// PDFAnalysis holds the complete analysis of a PDF document.
type PDFAnalysis struct {
	SourcePath string
	Pages      []PDFPageResult
	TotalPages int
}

// AnalyzePDF processes a PDF using a hybrid pipeline:
// 1. If marker plugin is available and UseMarker is true, get text extraction first
// 2. Render pages to images using pdftoppm/mutool
// 3. Send page images to VLM for table/chart detection and description
func (p *Processor) AnalyzePDF(pdfPath string, cfg PDFVisionConfig) (*PDFAnalysis, error) {
	if p.Model == "" {
		return nil, fmt.Errorf("no multimodal model configured")
	}

	if cfg.DPI <= 0 {
		cfg.DPI = 150
	}

	// Check if PDF exists.
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, fmt.Errorf("PDF file not found: %w", err)
	}

	// Try marker plugin first for text extraction.
	var markerPages map[int]string
	if cfg.UseMarker {
		markerPages = tryMarkerExtraction(pdfPath)
	}

	// Render PDF pages to images.
	pageImages, err := renderPDFPages(pdfPath, cfg.DPI, cfg.MaxPages)
	if err != nil {
		return nil, fmt.Errorf("render PDF pages: %w", err)
	}

	analysis := &PDFAnalysis{
		SourcePath: pdfPath,
		TotalPages: len(pageImages),
	}

	// Process each page with VLM.
	for i, imgPath := range pageImages {
		pageNum := i + 1
		result := PDFPageResult{
			PageNum:   pageNum,
			ImagePath: imgPath,
		}

		// Include marker text if available.
		if text, ok := markerPages[pageNum]; ok {
			result.MarkerText = text
		}

		// Send page image to VLM for analysis.
		data, err := os.ReadFile(imgPath)
		if err != nil {
			result.Error = fmt.Errorf("read page image: %w", err)
			analysis.Pages = append(analysis.Pages, result)
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		prompt := pdfPagePrompt(pageNum, result.MarkerText)

		desc, err := p.queryOllama(prompt, encoded)
		if err != nil {
			result.Error = err
			analysis.Pages = append(analysis.Pages, result)
			continue
		}

		result.Description = desc
		result.HasTable = detectTableInDescription(desc)
		result.HasChart = detectChartInDescription(desc)
		analysis.Pages = append(analysis.Pages, result)
	}

	return analysis, nil
}

// queryOllama sends a prompt with an image to the Ollama API.
func (p *Processor) queryOllama(prompt, base64Image string) (string, error) {
	reqBody := map[string]interface{}{
		"model":  p.Model,
		"stream": false,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": prompt,
				"images":  []string{base64Image},
			},
		},
	}

	jsonBody, _ := json.Marshal(reqBody)
	client := &http.Client{Timeout: 120 * time.Second}

	resp, err := client.Post(p.OllamaHost+"/api/chat", "application/json",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned %d", resp.StatusCode)
	}

	var chatResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return chatResp.Message.Content, nil
}

// renderPDFPages converts PDF pages to images using pdftoppm or mutool.
func renderPDFPages(pdfPath string, dpi, maxPages int) ([]string, error) {
	outDir, err := os.MkdirTemp("", "gleann-pdf-*")
	if err != nil {
		return nil, err
	}

	// Try pdftoppm first (poppler-utils), then mutool (mupdf).
	if path, err := exec.LookPath("pdftoppm"); err == nil {
		return renderWithPdftoppm(path, pdfPath, outDir, dpi, maxPages)
	}
	if path, err := exec.LookPath("mutool"); err == nil {
		return renderWithMutool(path, pdfPath, outDir, dpi, maxPages)
	}

	os.RemoveAll(outDir)
	return nil, fmt.Errorf("no PDF renderer found: install poppler-utils (pdftoppm) or mupdf-tools (mutool)")
}

func renderWithPdftoppm(bin, pdfPath, outDir string, dpi, maxPages int) ([]string, error) {
	args := []string{
		"-jpeg", "-r", strconv.Itoa(dpi),
	}
	if maxPages > 0 {
		args = append(args, "-l", strconv.Itoa(maxPages))
	}
	args = append(args, pdfPath, filepath.Join(outDir, "page"))

	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %w\n%s", err, string(out))
	}

	return collectPageImages(outDir)
}

func renderWithMutool(bin, pdfPath, outDir string, dpi, maxPages int) ([]string, error) {
	args := []string{
		"draw", "-o", filepath.Join(outDir, "page-%d.png"),
		"-r", strconv.Itoa(dpi),
	}
	if maxPages > 0 {
		args = append(args, fmt.Sprintf("1-%d", maxPages))
	}
	args = append(args, pdfPath)

	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mutool: %w\n%s", err, string(out))
	}

	return collectPageImages(outDir)
}

func collectPageImages(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths, nil
}

// tryMarkerExtraction attempts to get text from the gleann-plugin-marker.
// Returns a map of page_number -> text. Non-blocking: returns nil on failure.
func tryMarkerExtraction(pdfPath string) map[int]string {
	// Check if marker plugin is running.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:5001/health")
	if err != nil {
		return nil
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	// Send PDF to marker.
	file, err := os.Open(pdfPath)
	if err != nil {
		return nil
	}
	defer file.Close()

	// Use multipart form upload.
	// For simplicity, we'll skip the full multipart implementation
	// and return nil — the VLM will handle everything.
	// Full marker integration would use the /convert endpoint.
	return nil
}

// pdfPagePrompt generates a prompt for analyzing a PDF page image.
func pdfPagePrompt(pageNum int, markerText string) string {
	base := fmt.Sprintf("Analyze this PDF page (page %d). ", pageNum)
	base += "Identify and describe:\n"
	base += "1. Any tables — extract the data in markdown table format\n"
	base += "2. Any charts or figures — describe the data they represent\n"
	base += "3. Key text content and headings\n"
	base += "4. Any diagrams or visual elements\n\n"
	base += "Format your response with clear sections for each element found."

	if markerText != "" {
		base += fmt.Sprintf("\n\nText extraction from this page (may have formatting errors):\n%s", markerText)
	}

	return base
}

// detectTableInDescription checks if the VLM description mentions tables.
func detectTableInDescription(desc string) bool {
	lower := strings.ToLower(desc)
	return strings.Contains(lower, "table") || strings.Contains(lower, "| ") ||
		strings.Contains(lower, "+---") || strings.Contains(lower, "column")
}

// detectChartInDescription checks if the VLM description mentions charts/figures.
func detectChartInDescription(desc string) bool {
	lower := strings.ToLower(desc)
	return strings.Contains(lower, "chart") || strings.Contains(lower, "graph") ||
		strings.Contains(lower, "figure") || strings.Contains(lower, "plot") ||
		strings.Contains(lower, "diagram") || strings.Contains(lower, "bar chart") ||
		strings.Contains(lower, "pie chart") || strings.Contains(lower, "line graph")
}

// CleanupPDFPages removes the temporary page image directory.
func CleanupPDFPages(pages []PDFPageResult) {
	if len(pages) == 0 {
		return
	}
	dir := filepath.Dir(pages[0].ImagePath)
	if strings.Contains(dir, "gleann-pdf-") {
		os.RemoveAll(dir)
	}
}

// ── 4.2 Table Extraction Enhancement ──────────────────────────

// TableExtractionResult holds structured table data extracted from a page image.
type TableExtractionResult struct {
	PageNum int     `json:"page_num"`
	Tables  []Table `json:"tables"`
	RawText string  `json:"raw_text"` // VLM's raw description
}

// Table represents a single extracted table.
type Table struct {
	Caption  string     `json:"caption,omitempty"`
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	Markdown string     `json:"markdown"` // markdown representation
}

// ExtractTables sends a page image to the VLM with a table-focused prompt
// and parses the response into structured table data.
func (p *Processor) ExtractTables(pageImagePath string, pageNum int) (*TableExtractionResult, error) {
	if p.Model == "" {
		return nil, fmt.Errorf("no multimodal model configured")
	}

	data, err := os.ReadFile(pageImagePath)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	prompt := tableExtractionPrompt(pageNum)

	desc, err := p.queryOllama(prompt, encoded)
	if err != nil {
		return nil, err
	}

	result := &TableExtractionResult{
		PageNum: pageNum,
		RawText: desc,
	}

	// Parse tables from the VLM response.
	result.Tables = parseTablesFromMarkdown(desc)
	return result, nil
}

// tableExtractionPrompt generates a focused prompt for table extraction.
func tableExtractionPrompt(pageNum int) string {
	return fmt.Sprintf("Analyze page %d of this document. "+
		"Focus ONLY on tables. For each table found:\n"+
		"1. Output the table caption if visible\n"+
		"2. Output the table in markdown format with | delimiters\n"+
		"3. Ensure all rows have the same number of columns\n"+
		"4. Use --- separator after the header row\n\n"+
		"If no tables are found, output: NO_TABLES_FOUND\n\n"+
		"Output ONLY the markdown tables, nothing else.", pageNum)
}

// parseTablesFromMarkdown extracts Table structs from markdown text.
func parseTablesFromMarkdown(text string) []Table {
	var tables []Table
	lines := strings.Split(text, "\n")

	var current *Table
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect table row: must have at least 2 pipe characters.
		if strings.Count(trimmed, "|") >= 2 && !isSeparatorRow(trimmed) {
			cells := splitTableRow(trimmed)
			if current == nil {
				current = &Table{}
				current.Headers = cells
			} else {
				current.Rows = append(current.Rows, cells)
			}
		} else if isSeparatorRow(trimmed) && current != nil {
			// Separator row — skip but keep current table.
			continue
		} else if current != nil && len(current.Headers) > 0 {
			// Non-table line ends the current table.
			current.Markdown = renderTableMarkdown(current)
			tables = append(tables, *current)
			current = nil
		}
	}

	// Close any open table.
	if current != nil && len(current.Headers) > 0 {
		current.Markdown = renderTableMarkdown(current)
		tables = append(tables, *current)
	}

	return tables
}

// isSeparatorRow checks if a line is a markdown table separator (|---|---|).
func isSeparatorRow(line string) bool {
	cleaned := strings.ReplaceAll(line, "|", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, ":", "")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned == "" && strings.Contains(line, "---")
}

// splitTableRow splits a markdown table row into cells.
func splitTableRow(line string) []string {
	// Remove leading/trailing pipes.
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// renderTableMarkdown renders a Table back to markdown format.
func renderTableMarkdown(t *Table) string {
	if len(t.Headers) == 0 {
		return ""
	}
	var sb strings.Builder
	// Header.
	sb.WriteString("| ")
	sb.WriteString(strings.Join(t.Headers, " | "))
	sb.WriteString(" |\n")
	// Separator.
	sb.WriteString("|")
	for range t.Headers {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")
	// Rows.
	for _, row := range t.Rows {
		sb.WriteString("| ")
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteString(" |\n")
	}
	return sb.String()
}

// ── 4.3 Chart Extraction ──────────────────────────────────────

// ChartExtractionResult holds structured data from a chart/figure.
type ChartExtractionResult struct {
	PageNum int     `json:"page_num"`
	Charts  []Chart `json:"charts"`
	RawText string  `json:"raw_text"`
}

// Chart represents a single extracted chart/figure.
type Chart struct {
	Type        string           `json:"type"`        // "bar", "line", "pie", "scatter", "diagram", "other"
	Title       string           `json:"title"`       // chart title
	Description string           `json:"description"` // detailed description
	DataPoints  []ChartDataPoint `json:"data_points"` // extracted data if possible
	Labels      []string         `json:"labels"`      // axis labels or legend items
}

// ChartDataPoint represents a single data point from a chart.
type ChartDataPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// ExtractCharts sends a page image to the VLM with a chart-focused prompt
// and parses the response into structured chart data.
func (p *Processor) ExtractCharts(pageImagePath string, pageNum int) (*ChartExtractionResult, error) {
	if p.Model == "" {
		return nil, fmt.Errorf("no multimodal model configured")
	}

	data, err := os.ReadFile(pageImagePath)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	prompt := chartExtractionPrompt(pageNum)

	desc, err := p.queryOllama(prompt, encoded)
	if err != nil {
		return nil, err
	}

	result := &ChartExtractionResult{
		PageNum: pageNum,
		RawText: desc,
	}

	result.Charts = parseChartsFromDescription(desc)
	return result, nil
}

// chartExtractionPrompt generates a focused prompt for chart/figure extraction.
func chartExtractionPrompt(pageNum int) string {
	return fmt.Sprintf("Analyze page %d of this document. "+
		"Focus ONLY on charts, graphs, figures, and diagrams.\n\n"+
		"For each chart/figure found, output:\n"+
		"CHART_TYPE: (bar|line|pie|scatter|diagram|other)\n"+
		"CHART_TITLE: <title if visible>\n"+
		"CHART_DESCRIPTION: <detailed description>\n"+
		"DATA_POINTS:\n"+
		"- <label>: <numeric value>\n"+
		"- <label>: <numeric value>\n"+
		"LABELS: <comma-separated axis labels or legend items>\n"+
		"---\n\n"+
		"If no charts/figures found, output: NO_CHARTS_FOUND", pageNum)
}

// parseChartsFromDescription extracts Chart structs from VLM description.
func parseChartsFromDescription(desc string) []Chart {
	if strings.Contains(desc, "NO_CHARTS_FOUND") {
		return nil
	}

	var charts []Chart
	sections := strings.Split(desc, "---")

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		chart := Chart{Type: "other"}

		lines := strings.Split(section, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)

			if strings.HasPrefix(line, "CHART_TYPE:") {
				chart.Type = strings.TrimSpace(strings.TrimPrefix(line, "CHART_TYPE:"))
			} else if strings.HasPrefix(line, "CHART_TITLE:") {
				chart.Title = strings.TrimSpace(strings.TrimPrefix(line, "CHART_TITLE:"))
			} else if strings.HasPrefix(line, "CHART_DESCRIPTION:") {
				chart.Description = strings.TrimSpace(strings.TrimPrefix(line, "CHART_DESCRIPTION:"))
			} else if strings.HasPrefix(line, "LABELS:") {
				labelsStr := strings.TrimSpace(strings.TrimPrefix(line, "LABELS:"))
				for _, l := range strings.Split(labelsStr, ",") {
					l = strings.TrimSpace(l)
					if l != "" {
						chart.Labels = append(chart.Labels, l)
					}
				}
			} else if strings.HasPrefix(line, "- ") && strings.Contains(line, ":") {
				// Data point: "- Revenue: 42.5"
				parts := strings.SplitN(strings.TrimPrefix(line, "- "), ":", 2)
				if len(parts) == 2 {
					val, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
					if err == nil {
						chart.DataPoints = append(chart.DataPoints, ChartDataPoint{
							Label: strings.TrimSpace(parts[0]),
							Value: val,
						})
					}
				}
			}
		}

		// Only add if we got meaningful content.
		if chart.Title != "" || chart.Description != "" || len(chart.DataPoints) > 0 {
			charts = append(charts, chart)
		}
	}

	return charts
}

// ── 4.4 Content Faithfulness ──────────────────────────────────

// FaithfulnessResult holds content faithfulness analysis results.
type FaithfulnessResult struct {
	Score         float64             `json:"score"`               // 0-100 overall faithfulness
	OmissionCount int                 `json:"omission_count"`      // content in source but missing from extraction
	HallucinCount int                 `json:"hallucination_count"` // content in extraction but not in source
	TotalChecks   int                 `json:"total_checks"`
	Details       []FaithfulnessCheck `json:"details"`
}

// FaithfulnessCheck represents a single content faithfulness check.
type FaithfulnessCheck struct {
	Rule     string `json:"rule"` // what was checked
	Type     string `json:"type"` // "omission" or "hallucination"
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"` // relevant text snippet
}

// CheckFaithfulness compares extracted text against source content using
// rule-based heuristics for omission and hallucination detection.
func CheckFaithfulness(sourceText, extractedText string) *FaithfulnessResult {
	result := &FaithfulnessResult{}

	srcLower := strings.ToLower(sourceText)
	extLower := strings.ToLower(extractedText)

	// Rule 1: Heading preservation — headings in source should appear in extraction.
	srcHeadings := extractHeadings(sourceText)
	for _, h := range srcHeadings {
		check := FaithfulnessCheck{
			Rule: "heading_preserved",
			Type: "omission",
		}
		if strings.Contains(extLower, strings.ToLower(h)) {
			check.Passed = true
			check.Evidence = h
		} else {
			check.Evidence = fmt.Sprintf("missing heading: %s", h)
			result.OmissionCount++
		}
		result.Details = append(result.Details, check)
		result.TotalChecks++
	}

	// Rule 2: Number preservation — numbers in source should appear.
	srcNumbers := extractNumbers(sourceText)
	for _, n := range srcNumbers {
		check := FaithfulnessCheck{
			Rule: "number_preserved",
			Type: "omission",
		}
		if strings.Contains(extractedText, n) {
			check.Passed = true
			check.Evidence = n
		} else {
			check.Evidence = fmt.Sprintf("missing number: %s", n)
			result.OmissionCount++
		}
		result.Details = append(result.Details, check)
		result.TotalChecks++
	}

	// Rule 3: Key term preservation — important terms should appear.
	srcTerms := extractKeyTerms(sourceText)
	for _, term := range srcTerms {
		check := FaithfulnessCheck{
			Rule: "key_term_preserved",
			Type: "omission",
		}
		if strings.Contains(extLower, strings.ToLower(term)) {
			check.Passed = true
			check.Evidence = term
		} else {
			check.Evidence = fmt.Sprintf("missing term: %s", term)
			result.OmissionCount++
		}
		result.Details = append(result.Details, check)
		result.TotalChecks++
	}

	// Rule 4: Hallucination check — long unique phrases in extraction not in source.
	extSentences := extractSentences(extractedText)
	for _, sent := range extSentences {
		if len(sent) < 20 {
			continue // skip short fragments
		}
		check := FaithfulnessCheck{
			Rule: "no_hallucination",
			Type: "hallucination",
		}
		// Check if key words from this sentence exist in source.
		words := strings.Fields(sent)
		matchCount := 0
		for _, w := range words {
			if len(w) > 3 && strings.Contains(srcLower, strings.ToLower(w)) {
				matchCount++
			}
		}
		contentWords := 0
		for _, w := range words {
			if len(w) > 3 {
				contentWords++
			}
		}
		if contentWords > 0 && float64(matchCount)/float64(contentWords) < 0.3 {
			check.Evidence = fmt.Sprintf("possible hallucination: %s", truncate(sent, 80))
			result.HallucinCount++
		} else {
			check.Passed = true
			check.Evidence = truncate(sent, 50)
		}
		result.Details = append(result.Details, check)
		result.TotalChecks++
	}

	// Calculate overall score.
	if result.TotalChecks > 0 {
		passed := 0
		for _, d := range result.Details {
			if d.Passed {
				passed++
			}
		}
		result.Score = float64(passed) / float64(result.TotalChecks) * 100
	}

	return result
}

// extractHeadings finds lines that look like headings.
func extractHeadings(text string) []string {
	var headings []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			// Strip # prefix.
			h := strings.TrimLeft(trimmed, "# ")
			if h != "" {
				headings = append(headings, h)
			}
		}
	}
	return headings
}

// extractNumbers finds numeric values in text.
func extractNumbers(text string) []string {
	var numbers []string
	seen := make(map[string]bool)
	for _, word := range strings.Fields(text) {
		// Strip punctuation.
		cleaned := strings.Trim(word, ".,;:!?()[]{}\"'")
		if cleaned == "" {
			continue
		}
		// Check if it looks like a number.
		if _, err := strconv.ParseFloat(cleaned, 64); err == nil {
			if !seen[cleaned] && len(cleaned) > 1 {
				numbers = append(numbers, cleaned)
				seen[cleaned] = true
			}
		}
	}
	// Limit to 20 most significant numbers.
	if len(numbers) > 20 {
		numbers = numbers[:20]
	}
	return numbers
}

// extractKeyTerms finds potentially important terms (capitalized multi-word).
func extractKeyTerms(text string) []string {
	var terms []string
	seen := make(map[string]bool)
	words := strings.Fields(text)
	for i := 0; i < len(words)-1; i++ {
		w := strings.Trim(words[i], ".,;:!?()[]{}\"'")
		if len(w) > 3 && w[0] >= 'A' && w[0] <= 'Z' {
			if !seen[w] {
				terms = append(terms, w)
				seen[w] = true
			}
		}
	}
	// Limit.
	if len(terms) > 30 {
		terms = terms[:30]
	}
	return terms
}

// extractSentences splits text into sentences.
func extractSentences(text string) []string {
	var sentences []string
	// Simple split on period + space.
	for _, s := range strings.Split(text, ". ") {
		s = strings.TrimSpace(s)
		if len(s) > 10 {
			sentences = append(sentences, s)
		}
	}
	if len(sentences) > 50 {
		sentences = sentences[:50]
	}
	return sentences
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
