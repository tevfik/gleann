package gleann

import (
	"archive/zip"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestNativePipelineFallback verifies that the NativeExtractor successfully
// extracts content from binary document formats (DOCX, XLSX, PPTX, CSV, HTML)
// when no plugin is available. This simulates the readDocuments() fallback chain.
func TestNativePipelineFallback(t *testing.T) {
	n := NewNativeExtractor()
	tmp := t.TempDir()

	// Create a mixed-format document directory.
	writeTestCSV(t, filepath.Join(tmp, "data.csv"))
	writeTestDOCX(t, filepath.Join(tmp, "report.docx"))
	writeTestXLSX(t, filepath.Join(tmp, "metrics.xlsx"))
	writeTestPPTX(t, filepath.Join(tmp, "slides.pptx"))
	writeTestHTML(t, filepath.Join(tmp, "page.html"))
	os.WriteFile(filepath.Join(tmp, "readme.md"), []byte("# Readme\n\nHello world"), 0o644)
	os.WriteFile(filepath.Join(tmp, "notes.txt"), []byte("Plain text notes"), 0o644)

	// Walk directory and extract – simulates readDocuments() fallback path.
	var totalChars int
	var extractedFiles int
	var failedFiles []string

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !n.CanHandle(ext) {
			continue
		}

		path := filepath.Join(tmp, entry.Name())
		md, err := n.Extract(path)
		if err != nil {
			failedFiles = append(failedFiles, entry.Name()+": "+err.Error())
			continue
		}

		md = strings.TrimSpace(md)
		if md == "" {
			failedFiles = append(failedFiles, entry.Name()+": empty output")
			continue
		}

		totalChars += len(md)
		extractedFiles++
		t.Logf("  ✓ %s → %d chars", entry.Name(), len(md))
	}

	if len(failedFiles) > 0 {
		t.Errorf("extraction failures: %v", failedFiles)
	}

	if extractedFiles != 7 {
		t.Errorf("expected 7 extracted files, got %d", extractedFiles)
	}

	t.Logf("Pipeline fallback: %d files → %d total chars", extractedFiles, totalChars)
}

// TestNativeExtractorChunkability verifies extracted markdown can be chunked
// into meaningful pieces (simulates the full pipeline: extract → chunk → items).
func TestNativeExtractorChunkability(t *testing.T) {
	n := NewNativeExtractor()
	tmp := t.TempDir()

	// Create a DOCX with enough content to produce multiple chunks.
	path := filepath.Join(tmp, "long_report.docx")
	var paras []pipelineDocxPara
	for i := 0; i < 50; i++ {
		if i%10 == 0 {
			paras = append(paras, pipelineDocxPara{
				style: "Heading1",
				text:  fmt.Sprintf("Chapter %d", i/10+1),
			})
		}
		paras = append(paras, pipelineDocxPara{
			text: fmt.Sprintf("This is paragraph %d with enough text content to fill chunks. "+
				"The system processes documents by extracting text, converting to markdown, "+
				"then splitting into overlapping chunks for embedding computation. Each chunk "+
				"preserves heading context for better retrieval quality.", i+1),
		})
	}
	writePipelineDOCX(t, path, paras)

	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Verify structural properties.
	lines := strings.Split(md, "\n")
	headingCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			headingCount++
		}
	}

	if headingCount < 3 {
		t.Errorf("expected at least 3 headings, got %d", headingCount)
	}

	if len(md) < 5000 {
		t.Errorf("expected substantial output (>5000 chars), got %d", len(md))
	}

	t.Logf("DOCX → %d chars, %d headings, %d lines", len(md), headingCount, len(lines))
}

// TestNativeExtractorConcurrency verifies extraction is safe for concurrent use.
func TestNativeExtractorConcurrency(t *testing.T) {
	n := NewNativeExtractor()
	tmp := t.TempDir()

	// Create test files.
	for i := 0; i < 10; i++ {
		path := filepath.Join(tmp, fmt.Sprintf("file%d.csv", i))
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		w := csv.NewWriter(f)
		w.WriteAll([][]string{
			{"Name", "Value"},
			{fmt.Sprintf("item%d", i), fmt.Sprintf("%d", i*100)},
		})
		f.Close()
	}

	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			path := filepath.Join(tmp, fmt.Sprintf("file%d.csv", idx))
			md, err := n.Extract(path)
			if err != nil {
				errs <- fmt.Errorf("file%d: %w", idx, err)
				return
			}
			if !strings.Contains(md, fmt.Sprintf("item%d", idx)) {
				errs <- fmt.Errorf("file%d: missing expected content", idx)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// ── Test helpers ───────────────────────────────────────────────

func writeTestCSV(t *testing.T, path string) {
	t.Helper()
	f, _ := os.Create(path)
	w := csv.NewWriter(f)
	w.WriteAll([][]string{
		{"Name", "Department", "Status"},
		{"Alice", "Engineering", "Active"},
		{"Bob", "Marketing", "Active"},
		{"Carol", "Engineering", "On Leave"},
	})
	f.Close()
}

type pipelineDocxPara struct {
	style string
	text  string
}

func writeTestDOCX(t *testing.T, path string) {
	t.Helper()
	writePipelineDOCX(t, path, []pipelineDocxPara{
		{style: "Heading1", text: "Project Overview"},
		{text: "This document describes the project architecture."},
		{style: "Heading2", text: "Components"},
		{text: "The system has three main components."},
	})
}

func writePipelineDOCX(t *testing.T, path string, paragraphs []pipelineDocxPara) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	ct, _ := zw.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
</Types>`))

	doc, _ := zw.Create("word/document.xml")
	var xmlBuf strings.Builder
	xmlBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, p := range paragraphs {
		xmlBuf.WriteString("<w:p>")
		if p.style != "" {
			xmlBuf.WriteString(`<w:pPr><w:pStyle w:val="`)
			xml.Escape(&xmlBuf, []byte(p.style))
			xmlBuf.WriteString(`"/></w:pPr>`)
		}
		xmlBuf.WriteString(`<w:r><w:t>`)
		xml.Escape(&xmlBuf, []byte(p.text))
		xmlBuf.WriteString(`</w:t></w:r></w:p>`)
	}
	xmlBuf.WriteString("</w:body></w:document>")
	doc.Write([]byte(xmlBuf.String()))
	zw.Close()
}

func writeTestXLSX(t *testing.T, path string) {
	t.Helper()
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "Metric")
	f.SetCellValue("Sheet1", "B1", "Value")
	f.SetCellValue("Sheet1", "A2", "CPU")
	f.SetCellValue("Sheet1", "B2", "85%")
	f.SetCellValue("Sheet1", "A3", "Memory")
	f.SetCellValue("Sheet1", "B3", "72%")
	f.SaveAs(path)
}

func writeTestPPTX(t *testing.T, path string) {
	t.Helper()
	f, _ := os.Create(path)
	defer f.Close()
	zw := zip.NewWriter(f)
	for i := 1; i <= 3; i++ {
		slideXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Slide %d: Demo content</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld>
</p:sld>`, i)
		w, _ := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", i))
		w.Write([]byte(slideXML))
	}
	zw.Close()
}

func writeTestHTML(t *testing.T, path string) {
	t.Helper()
	html := `<html><body>
<h1>Documentation</h1>
<p>Welcome to the documentation portal.</p>
<h2>Getting Started</h2>
<p>Follow these steps to begin.</p>
</body></html>`
	os.WriteFile(path, []byte(html), 0o644)
}
