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

// ── Benchmarks ─────────────────────────────────────────────────
// Run:  go test -bench=BenchmarkNative -benchmem ./pkg/gleann/

func BenchmarkNativeExtract_CSV_Small(b *testing.B) {
	path := benchCSV(b, 50, 5) // 50 rows, 5 cols
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_CSV_Large(b *testing.B) {
	path := benchCSV(b, 10000, 10) // 10K rows, 10 cols
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_DOCX_Small(b *testing.B) {
	path := benchDOCX(b, 20) // 20 paragraphs
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_DOCX_Large(b *testing.B) {
	path := benchDOCX(b, 500) // 500 paragraphs
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_XLSX_Small(b *testing.B) {
	path := benchXLSX(b, 100, 5) // 100 rows, 5 cols
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_XLSX_Large(b *testing.B) {
	path := benchXLSX(b, 5000, 10) // 5K rows, 10 cols
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_PPTX(b *testing.B) {
	path := benchPPTX(b, 30) // 30 slides
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_HTML_Small(b *testing.B) {
	path := benchHTML(b, 50) // 50 paragraphs
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_HTML_Large(b *testing.B) {
	path := benchHTML(b, 2000) // 2K paragraphs
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

func BenchmarkNativeExtract_Text(b *testing.B) {
	path := benchText(b, 10000) // 10K lines
	n := NewNativeExtractor()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Extract(path)
	}
}

// ── Extraction quality tests ───────────────────────────────────

func TestNativeExtract_DOCX_HeadingPreservation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "headings.docx")

	createBenchDOCX(t, path, []benchDocxPara{
		{style: "Heading1", text: "Introduction"},
		{style: "", text: "Welcome to the system."},
		{style: "Heading2", text: "Architecture"},
		{style: "", text: "The system uses microservices."},
		{style: "Heading3", text: "Database Layer"},
		{style: "", text: "PostgreSQL is the primary store."},
		{style: "ListParagraph", text: "Item one"},
		{style: "ListParagraph", text: "Item two"},
	})

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"H1", "## Introduction"},
		{"H2", "### Architecture"},
		{"H3", "#### Database Layer"},
		{"body", "microservices"},
		{"list", "- Item one"},
	}
	for _, c := range checks {
		if !strings.Contains(md, c.want) {
			t.Errorf("[%s] expected %q in output", c.label, c.want)
		}
	}
}

func TestNativeExtract_XLSX_MultiSheet(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multi.xlsx")

	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "ID")
	f.SetCellValue("Sheet1", "B1", "Name")
	f.SetCellValue("Sheet1", "A2", "1")
	f.SetCellValue("Sheet1", "B2", "Alice")

	f.NewSheet("Metrics")
	f.SetCellValue("Metrics", "A1", "Key")
	f.SetCellValue("Metrics", "B1", "Value")
	f.SetCellValue("Metrics", "A2", "CPU")
	f.SetCellValue("Metrics", "B2", "95%")

	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(md, "## Sheet1") {
		t.Error("expected Sheet1 heading")
	}
	if !strings.Contains(md, "## Metrics") {
		t.Error("expected Metrics heading")
	}
	if !strings.Contains(md, "CPU") {
		t.Error("expected Metrics data")
	}
}

func TestNativeExtract_OutputSize(t *testing.T) {
	// Verify output is proportional to input — no runaway memory.
	n := NewNativeExtractor()

	tmp := t.TempDir()

	// Small CSV
	smallPath := filepath.Join(tmp, "small.csv")
	writeBenchCSV(t, smallPath, 10, 3)
	smallMD, _ := n.Extract(smallPath)

	// Large CSV
	largePath := filepath.Join(tmp, "large.csv")
	writeBenchCSV(t, largePath, 1000, 3)
	largeMD, _ := n.Extract(largePath)

	ratio := float64(len(largeMD)) / float64(len(smallMD))
	// 100x more rows → output should be ~100x larger (±50% tolerance).
	if ratio < 50 || ratio > 150 {
		t.Errorf("output ratio %0.1f outside expected range 50-150 (small=%d, large=%d)",
			ratio, len(smallMD), len(largeMD))
	}
}

// ── Bench helpers ──────────────────────────────────────────────

func benchCSV(b *testing.B, rows, cols int) string {
	b.Helper()
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.csv")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	header := make([]string, cols)
	for i := range header {
		header[i] = fmt.Sprintf("Col%d", i)
	}
	w.Write(header)
	for r := 0; r < rows; r++ {
		row := make([]string, cols)
		for c := range row {
			row[c] = fmt.Sprintf("val_%d_%d", r, c)
		}
		w.Write(row)
	}
	w.Flush()
	return path
}

type benchDocxPara struct {
	style string
	text  string
}

func benchDOCX(b *testing.B, paragraphs int) string {
	b.Helper()
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.docx")
	paras := make([]benchDocxPara, paragraphs)
	for i := range paras {
		if i%10 == 0 {
			paras[i] = benchDocxPara{style: "Heading1", text: fmt.Sprintf("Section %d", i/10+1)}
		} else {
			paras[i] = benchDocxPara{text: fmt.Sprintf("Paragraph %d with some filler text for benchmarking the extraction speed.", i)}
		}
	}
	createBenchDOCXForBench(b, path, paras)
	return path
}

func benchXLSX(b *testing.B, rows, cols int) string {
	b.Helper()
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.xlsx")
	f := excelize.NewFile()
	for c := 0; c < cols; c++ {
		cell := fmt.Sprintf("%c1", 'A'+c)
		f.SetCellValue("Sheet1", cell, fmt.Sprintf("Col%d", c))
	}
	for r := 2; r <= rows+1; r++ {
		for c := 0; c < cols; c++ {
			cell := fmt.Sprintf("%c%d", 'A'+c, r)
			f.SetCellValue("Sheet1", cell, fmt.Sprintf("v%d_%d", r, c))
		}
	}
	if err := f.SaveAs(path); err != nil {
		b.Fatal(err)
	}
	return path
}

func benchPPTX(b *testing.B, slides int) string {
	b.Helper()
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.pptx")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for i := 1; i <= slides; i++ {
		slideXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Slide %d content with text for benchmarking</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld>
</p:sld>`, i)
		w, _ := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", i))
		w.Write([]byte(slideXML))
	}
	zw.Close()
	return path
}

func benchHTML(b *testing.B, paragraphs int) string {
	b.Helper()
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.html")
	var sb strings.Builder
	sb.WriteString("<html><body>\n")
	for i := 0; i < paragraphs; i++ {
		if i%20 == 0 {
			sb.WriteString(fmt.Sprintf("<h2>Section %d</h2>\n", i/20+1))
		}
		sb.WriteString(fmt.Sprintf("<p>Paragraph %d with content for benchmarking the extraction pipeline.</p>\n", i))
	}
	sb.WriteString("</body></html>\n")
	os.WriteFile(path, []byte(sb.String()), 0o644)
	return path
}

func benchText(b *testing.B, lines int) string {
	b.Helper()
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.txt")
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString(fmt.Sprintf("Line %d: This is benchmark text content for measuring extraction throughput.\n", i))
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)
	return path
}

func createBenchDOCXForBench(b *testing.B, path string, paragraphs []benchDocxPara) {
	b.Helper()
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
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

func createBenchDOCX(t *testing.T, path string, paragraphs []benchDocxPara) {
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

func writeBenchCSV(t *testing.T, path string, rows, cols int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	header := make([]string, cols)
	for i := range header {
		header[i] = fmt.Sprintf("Col%d", i)
	}
	w.Write(header)
	for r := 0; r < rows; r++ {
		row := make([]string, cols)
		for c := range row {
			row[c] = fmt.Sprintf("val_%d_%d", r, c)
		}
		w.Write(row)
	}
	w.Flush()
}
