package gleann

import (
	"archive/zip"
	"encoding/csv"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestNativeExtractor_CanHandle(t *testing.T) {
	n := NewNativeExtractor()

	yes := []string{".csv", ".docx", ".xlsx", ".pptx", ".pdf", ".md", ".txt", ".html", ".htm"}
	for _, ext := range yes {
		if !n.CanHandle(ext) {
			t.Errorf("expected CanHandle(%q) = true", ext)
		}
	}

	no := []string{".go", ".py", ".png", ".jpg", ".doc", ".xls", ".ppt"}
	for _, ext := range no {
		if n.CanHandle(ext) {
			t.Errorf("expected CanHandle(%q) = false", ext)
		}
	}
}

func TestNativeExtractor_SupportedExtensions(t *testing.T) {
	n := NewNativeExtractor()
	exts := n.SupportedExtensions()
	if len(exts) < 8 {
		t.Fatalf("expected at least 8 supported extensions, got %d", len(exts))
	}
}

func TestNativeExtractor_CSV(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "data.csv")

	f, _ := os.Create(path)
	w := csv.NewWriter(f)
	w.WriteAll([][]string{
		{"Name", "Age", "City"},
		{"Alice", "30", "Istanbul"},
		{"Bob", "25", "Ankara"},
	})
	f.Close()

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract csv: %v", err)
	}

	if !strings.Contains(md, "| Name | Age | City |") {
		t.Error("expected markdown table header")
	}
	if !strings.Contains(md, "Alice") {
		t.Error("expected data row with Alice")
	}
	if !strings.Contains(md, "Bob") {
		t.Error("expected data row with Bob")
	}
	t.Logf("CSV output:\n%s", md)
}

func TestNativeExtractor_CSV_Empty(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "empty.csv")
	os.WriteFile(path, []byte(""), 0o644)

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract empty csv: %v", err)
	}
	if md != "" {
		t.Errorf("expected empty output for empty csv, got: %q", md)
	}
}

func TestNativeExtractor_Text(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "readme.md")
	os.WriteFile(path, []byte("# Hello\n\nWorld"), 0o644)

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}

	if md != "# Hello\n\nWorld" {
		t.Errorf("expected passthrough, got: %q", md)
	}
}

func TestNativeExtractor_PlainText(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "notes.txt")
	os.WriteFile(path, []byte("Some notes here\nLine 2"), 0o644)

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract txt: %v", err)
	}
	if !strings.Contains(md, "Some notes here") {
		t.Error("expected text content")
	}
}

func TestNativeExtractor_HTML(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "page.html")
	html := `<html><body>
<h1>Title</h1>
<p>Paragraph one.</p>
<h2>Section</h2>
<p>Paragraph two.</p>
<ul><li>Item 1</li><li>Item 2</li></ul>
</body></html>`
	os.WriteFile(path, []byte(html), 0o644)

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract html: %v", err)
	}

	if !strings.Contains(md, "## Title") {
		t.Error("expected H1 converted to ## heading")
	}
	if !strings.Contains(md, "Paragraph one.") {
		t.Error("expected paragraph text")
	}
	if !strings.Contains(md, "### Section") {
		t.Error("expected H2 converted to ### heading")
	}
	t.Logf("HTML output:\n%s", md)
}

func TestNativeExtractor_DOCX(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.docx")

	// Create a minimal valid DOCX (zip with word/document.xml).
	createMinimalDOCX(t, path, []docxParagraph{
		{style: "Heading1", text: "Report Title"},
		{style: "", text: "This is the introduction."},
		{style: "Heading2", text: "Chapter 1"},
		{style: "", text: "Chapter content here."},
	})

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}

	if !strings.Contains(md, "## Report Title") {
		t.Error("expected Heading1 as ##")
	}
	if !strings.Contains(md, "This is the introduction.") {
		t.Error("expected body text")
	}
	if !strings.Contains(md, "### Chapter 1") {
		t.Error("expected Heading2 as ###")
	}
	t.Logf("DOCX output:\n%s", md)
}

func TestNativeExtractor_PPTX(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "presentation.pptx")

	createMinimalPPTX(t, path, []string{
		"Welcome to Gleann",
		"Features and Benefits",
	})

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract pptx: %v", err)
	}

	if !strings.Contains(md, "## Slide 1") {
		t.Error("expected Slide 1 heading")
	}
	if !strings.Contains(md, "Welcome to Gleann") {
		t.Error("expected slide 1 text")
	}
	if !strings.Contains(md, "## Slide 2") {
		t.Error("expected Slide 2 heading")
	}
	t.Logf("PPTX output:\n%s", md)
}

func TestNativeExtractor_XLSX(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "data.xlsx")

	// Create using excelize.
	createMinimalXLSX(t, path)

	n := NewNativeExtractor()
	md, err := n.Extract(path)
	if err != nil {
		t.Fatalf("extract xlsx: %v", err)
	}

	if !strings.Contains(md, "Name") {
		t.Error("expected header row with Name")
	}
	if !strings.Contains(md, "Alice") {
		t.Error("expected data with Alice")
	}
	t.Logf("XLSX output:\n%s", md)
}

func TestNativeExtractor_UnsupportedExt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "image.png")
	os.WriteFile(path, []byte("fake"), 0o644)

	n := NewNativeExtractor()
	_, err := n.Extract(path)
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestNativeExtractor_MissingFile(t *testing.T) {
	n := NewNativeExtractor()
	_, err := n.Extract("/nonexistent/file.csv")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestDocExtractor_NativeLayerFallback(t *testing.T) {
	// DocExtractor with no markitdown and no plugins should use native.
	de := NewDocExtractor(nil, nil)

	tmp := t.TempDir()
	csvPath := filepath.Join(tmp, "test.csv")
	f, _ := os.Create(csvPath)
	w := csv.NewWriter(f)
	w.WriteAll([][]string{{"A", "B"}, {"1", "2"}})
	f.Close()

	result, err := de.Extract(csvPath)
	if err != nil {
		t.Fatalf("DocExtractor native fallback: %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Error("expected at least 1 node from native extraction")
	}
	t.Logf("DocExtractor native fallback: %d nodes, %d edges", len(result.Nodes), len(result.Edges))
}

func TestHtmlToMarkdown(t *testing.T) {
	html := `<h1>Title</h1><p>Hello world</p><h2>Sub</h2><p>Details</p>`
	md := htmlToMarkdown(html, "test.html")

	if !strings.Contains(md, "## Title") {
		t.Error("expected h1 converted")
	}
	if !strings.Contains(md, "Hello world") {
		t.Error("expected paragraph")
	}
}

func TestPadRow(t *testing.T) {
	row := []string{"a", "b"}
	padded := padRow(row, 4)
	if len(padded) != 4 {
		t.Fatalf("expected 4 cells, got %d", len(padded))
	}
	if padded[0] != "a" || padded[1] != "b" || padded[2] != "" || padded[3] != "" {
		t.Errorf("unexpected padding: %v", padded)
	}
}

// ── Test helpers ───────────────────────────────────────────────

type docxParagraph struct {
	style string
	text  string
}

func createMinimalDOCX(t *testing.T, path string, paragraphs []docxParagraph) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	// [Content_Types].xml
	ct, _ := zw.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
</Types>`))

	// word/document.xml
	doc, _ := zw.Create("word/document.xml")
	var xmlBuf strings.Builder
	xmlBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>`)

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

func createMinimalPPTX(t *testing.T, path string, slideTexts []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	for i, text := range slideTexts {
		name := "ppt/slides/slide" + strings.Replace(strings.TrimLeft(strings.Repeat("0", 0), "0")+string(rune('1'+i-1)), "\x00", "", -1)
		// Simpler approach:
		slideXML := `<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld>
</p:sld>`
		_ = name
		slideName := "ppt/slides/slide" + itoa(i+1) + ".xml"
		w, _ := zw.Create(slideName)
		w.Write([]byte(slideXML))
	}

	zw.Close()
}

func createMinimalXLSX(t *testing.T, path string) {
	t.Helper()
	f := excelize.NewFile()

	f.SetCellValue("Sheet1", "A1", "Name")
	f.SetCellValue("Sheet1", "B1", "Age")
	f.SetCellValue("Sheet1", "A2", "Alice")
	f.SetCellValue("Sheet1", "B2", 30)
	f.SetCellValue("Sheet1", "A3", "Bob")
	f.SetCellValue("Sheet1", "B3", 25)

	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	d := []byte{}
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
