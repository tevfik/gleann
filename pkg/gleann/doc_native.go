// Package gleann provides doc_native.go — pure-Go document extraction.
//
// This is "Layer -1": fully Go-native extraction with ZERO external dependencies.
// No Python, no CLI tools, no network calls. Supports:
//
//   - CSV:  encoding/csv (stdlib)
//   - DOCX: archive/zip + encoding/xml (stdlib — OOXML is just zipped XML)
//   - XLSX: github.com/xuri/excelize (pure Go)
//   - PPTX: archive/zip + encoding/xml (stdlib — OOXML)
//   - PDF:  github.com/ledongthuc/pdf (pure Go)
//   - HTML: golang.org/x/net/html (quasi-stdlib)
//   - Markdown: passthrough (already text)
//
// Fallback chain:
//
//	Layer -1 (Go-native) → Layer 0 (markitdown CLI) → Layer 1 (Python plugin)
package gleann

import (
	"archive/zip"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gopdf "github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

// NativeExtractor provides pure-Go document extraction with zero external deps.
type NativeExtractor struct{}

// NewNativeExtractor returns a Go-native extractor (always available).
func NewNativeExtractor() *NativeExtractor {
	return &NativeExtractor{}
}

// nativeExts are file types that the Go-native extractor handles.
var nativeExts = map[string]bool{
	".csv":  true,
	".docx": true,
	".xlsx": true,
	".pptx": true,
	".pdf":  true,
	".md":   true,
	".txt":  true,
	".html": true, ".htm": true,
}

// CanHandle returns true if this extension can be extracted natively.
func (n *NativeExtractor) CanHandle(ext string) bool {
	return nativeExts[strings.ToLower(ext)]
}

// Extract converts a file to markdown using pure-Go parsers.
func (n *NativeExtractor) Extract(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".csv":
		return n.extractCSV(filePath)
	case ".docx":
		return n.extractDOCX(filePath)
	case ".xlsx":
		return n.extractXLSX(filePath)
	case ".pptx":
		return n.extractPPTX(filePath)
	case ".pdf":
		return n.extractPDF(filePath)
	case ".md", ".txt":
		return n.extractText(filePath)
	case ".html", ".htm":
		return n.extractHTML(filePath)
	default:
		return "", fmt.Errorf("unsupported extension: %s", ext)
	}
}

// SupportedExtensions returns the list of natively handled extensions.
func (n *NativeExtractor) SupportedExtensions() []string {
	exts := make([]string, 0, len(nativeExts))
	for ext := range nativeExts {
		exts = append(exts, ext)
	}
	return exts
}

// ── CSV ────────────────────────────────────────────────────────

func (n *NativeExtractor) extractCSV(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1 // variable columns

	records, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("csv parse: %w", err)
	}

	if len(records) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(filePath)))

	// Header row → markdown table header.
	header := records[0]
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat(" --- |", len(header)) + "\n")

	// Data rows.
	for _, row := range records[1:] {
		// Pad or trim to match header width.
		cells := make([]string, len(header))
		for i := range cells {
			if i < len(row) {
				cells[i] = row[i]
			}
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}

	return b.String(), nil
}

// ── DOCX ───────────────────────────────────────────────────────
// DOCX = ZIP archive containing word/document.xml with <w:p> paragraphs.

func (n *NativeExtractor) extractDOCX(filePath string) (string, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	// Find word/document.xml
	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found in docx")
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	return parseWordXML(rc, filePath)
}

// parseWordXML extracts text and tables from OOXML word/document.xml.
func parseWordXML(r io.Reader, fileName string) (string, error) {
	decoder := xml.NewDecoder(r)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(fileName)))

	var inParagraph bool
	var inTable bool
	var inTableCell bool
	var paragraphText strings.Builder
	var cellText strings.Builder
	var currentStyle string
	var currentRow []string
	var tableRows [][]string

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl": // <w:tbl> — table
				inTable = true
				tableRows = nil
			case "tr": // <w:tr> — table row
				if inTable {
					currentRow = nil
				}
			case "tc": // <w:tc> — table cell
				if inTable {
					inTableCell = true
					cellText.Reset()
				}
			case "p": // <w:p> — paragraph
				if !inTableCell {
					inParagraph = true
					paragraphText.Reset()
					currentStyle = ""
				}
			case "pStyle": // <w:pStyle w:val="Heading1"/>
				for _, attr := range t.Attr {
					if attr.Name.Local == "val" {
						currentStyle = attr.Value
					}
				}
			}
		case xml.CharData:
			if inTableCell {
				cellText.Write(t)
			} else if inParagraph {
				paragraphText.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "tbl":
				if inTable && len(tableRows) > 0 {
					b.WriteString(renderMarkdownTable(tableRows))
					b.WriteString("\n")
				}
				inTable = false
				tableRows = nil
			case "tr":
				if inTable && currentRow != nil {
					tableRows = append(tableRows, currentRow)
					currentRow = nil
				}
			case "tc":
				if inTable && inTableCell {
					currentRow = append(currentRow, strings.TrimSpace(cellText.String()))
					inTableCell = false
				}
			case "p":
				if inTableCell {
					// Inside a table cell, paragraphs add line breaks.
					if cellText.Len() > 0 {
						text := strings.TrimSpace(cellText.String())
						if text != "" {
							cellText.Reset()
							cellText.WriteString(text + " ")
						}
					}
				} else if inParagraph {
					text := strings.TrimSpace(paragraphText.String())
					if text != "" {
						switch {
						case strings.Contains(currentStyle, "Heading1") || strings.Contains(currentStyle, "heading 1"):
							b.WriteString("## " + text + "\n\n")
						case strings.Contains(currentStyle, "Heading2") || strings.Contains(currentStyle, "heading 2"):
							b.WriteString("### " + text + "\n\n")
						case strings.Contains(currentStyle, "Heading3") || strings.Contains(currentStyle, "heading 3"):
							b.WriteString("#### " + text + "\n\n")
						case strings.Contains(currentStyle, "ListParagraph"):
							b.WriteString("- " + text + "\n")
						default:
							b.WriteString(text + "\n\n")
						}
					}
					inParagraph = false
				}
			}
		}
	}

	return b.String(), nil
}

// renderMarkdownTable converts a 2D string grid into a markdown table.
// The first row is treated as the header.
func renderMarkdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	// Find max column count.
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return ""
	}

	var b strings.Builder

	// Header row.
	header := padRow(rows[0], maxCols)
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat(" --- |", maxCols) + "\n")

	// Data rows.
	for _, row := range rows[1:] {
		cells := padRow(row, maxCols)
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}

	return b.String()
}

// ── XLSX ───────────────────────────────────────────────────────
// Uses excelize for reliable Excel parsing.

func (n *NativeExtractor) extractXLSX(filePath string) (string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(filePath)))

	for _, sheet := range f.GetSheetList() {
		b.WriteString(fmt.Sprintf("## %s\n\n", sheet))

		rows, err := f.GetRows(sheet)
		if err != nil || len(rows) == 0 {
			continue
		}

		// Find max column width.
		maxCols := 0
		for _, row := range rows {
			if len(row) > maxCols {
				maxCols = len(row)
			}
		}
		if maxCols == 0 {
			continue
		}

		// Header.
		header := padRow(rows[0], maxCols)
		b.WriteString("| " + strings.Join(header, " | ") + " |\n")
		b.WriteString("|" + strings.Repeat(" --- |", maxCols) + "\n")

		// Data rows.
		for _, row := range rows[1:] {
			cells := padRow(row, maxCols)
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// padRow pads a row to the given width with empty strings.
func padRow(row []string, width int) []string {
	cells := make([]string, width)
	for i := range cells {
		if i < len(row) {
			cells[i] = row[i]
		}
	}
	return cells
}

// ── PPTX ───────────────────────────────────────────────────────
// PPTX = ZIP archive with ppt/slides/slide{N}.xml files.

func (n *NativeExtractor) extractPPTX(filePath string) (string, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(filePath)))

	// Collect slides in order.
	slideFiles := make(map[string]*zip.File)
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideFiles[f.Name] = f
		}
	}

	// Process slides in numeric order.
	for i := 1; i <= len(slideFiles); i++ {
		name := fmt.Sprintf("ppt/slides/slide%d.xml", i)
		f, ok := slideFiles[name]
		if !ok {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}

		text := extractXMLText(rc)
		rc.Close()

		if text != "" {
			b.WriteString(fmt.Sprintf("## Slide %d\n\n", i))
			b.WriteString(text + "\n\n")
		}
	}

	return b.String(), nil
}

// extractXMLText pulls all text content and tables from an XML stream.
// Handles both PPTX (<a:t>, <a:tbl>) and DOCX (<w:t>) elements.
func extractXMLText(r io.Reader) string {
	decoder := xml.NewDecoder(r)
	var parts []string
	var inText bool
	var inTable bool
	var inTableCell bool
	var cellText strings.Builder
	var currentRow []string
	var tableRows [][]string

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl": // <a:tbl> in PPTX
				inTable = true
				tableRows = nil
			case "tr":
				if inTable {
					currentRow = nil
				}
			case "tc":
				if inTable {
					inTableCell = true
					cellText.Reset()
				}
			case "t": // <a:t> or <w:t>
				inText = true
			}
		case xml.CharData:
			if inTableCell && inText {
				cellText.Write(t)
			} else if inText && !inTable {
				s := strings.TrimSpace(string(t))
				if s != "" {
					parts = append(parts, s)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "tc":
				if inTable && inTableCell {
					currentRow = append(currentRow, strings.TrimSpace(cellText.String()))
					inTableCell = false
				}
			case "tr":
				if inTable && currentRow != nil {
					tableRows = append(tableRows, currentRow)
					currentRow = nil
				}
			case "tbl":
				if inTable && len(tableRows) > 0 {
					parts = append(parts, "\n"+renderMarkdownTable(tableRows))
				}
				inTable = false
				tableRows = nil
			}
		}
	}

	return strings.Join(parts, " ")
}

// ── PDF ────────────────────────────────────────────────────────
// Uses ledongthuc/pdf — pure Go, no CGo.

func (n *NativeExtractor) extractPDF(filePath string) (string, error) {
	f, r, err := gopdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(filePath)))

	totalPages := r.NumPage()
	for i := 1; i <= totalPages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}

		text = strings.TrimSpace(text)
		if text != "" {
			if totalPages > 1 {
				b.WriteString(fmt.Sprintf("## Page %d\n\n", i))
			}
			b.WriteString(text + "\n\n")
		}
	}

	return b.String(), nil
}

// ── Plain Text / Markdown ──────────────────────────────────────

func (n *NativeExtractor) extractText(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ── HTML ───────────────────────────────────────────────────────
// Simple tag-stripping extraction (no external HTML parser needed).

func (n *NativeExtractor) extractHTML(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	return htmlToMarkdown(string(data), filePath), nil
}

// htmlToMarkdown does a simple HTML → Markdown conversion.
// For gleann's indexing use case, we care about text content + headings.
func htmlToMarkdown(html, fileName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(fileName)))

	// Simple state machine for tag stripping.
	var inTag bool
	var tagBuf strings.Builder
	var textBuf strings.Builder

	flush := func(prefix string) {
		text := strings.TrimSpace(textBuf.String())
		if text != "" {
			b.WriteString(prefix + text + "\n\n")
		}
		textBuf.Reset()
	}

	for _, ch := range html {
		switch {
		case ch == '<':
			inTag = true
			tagBuf.Reset()
		case ch == '>':
			inTag = false
			tag := strings.ToLower(tagBuf.String())
			tagBuf.Reset()

			// Convert HTML heading/paragraph tags to markdown.
			switch {
			case tag == "h1" || tag == "h1 ":
				flush("")
				textBuf.Reset()
			case tag == "/h1":
				text := strings.TrimSpace(textBuf.String())
				if text != "" {
					b.WriteString("## " + text + "\n\n")
				}
				textBuf.Reset()
			case tag == "h2" || strings.HasPrefix(tag, "h2 "):
				flush("")
			case tag == "/h2":
				text := strings.TrimSpace(textBuf.String())
				if text != "" {
					b.WriteString("### " + text + "\n\n")
				}
				textBuf.Reset()
			case tag == "h3" || strings.HasPrefix(tag, "h3 "):
				flush("")
			case tag == "/h3":
				text := strings.TrimSpace(textBuf.String())
				if text != "" {
					b.WriteString("#### " + text + "\n\n")
				}
				textBuf.Reset()
			case tag == "p" || strings.HasPrefix(tag, "p "),
				tag == "/p",
				tag == "br", tag == "br/", tag == "br /":
				flush("")
			case tag == "li" || strings.HasPrefix(tag, "li "):
				flush("")
				textBuf.WriteString("- ")
			case tag == "/li":
				flush("")
			case strings.HasPrefix(tag, "script"), strings.HasPrefix(tag, "style"):
				// Skip content of script/style tags (simplified).
			}
		case inTag:
			tagBuf.WriteRune(ch)
		default:
			textBuf.WriteRune(ch)
		}
	}

	flush("")
	return b.String()
}
