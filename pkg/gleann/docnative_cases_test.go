package gleann

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// minimalPDF builds a valid PDF with one page per text ("" gives an empty
// page); the cross-reference table offsets are computed, not hard-coded.
func minimalPDF(texts ...string) []byte {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	buf.WriteString("%PDF-1.4\n")
	kids := make([]string, len(texts))
	for i := range texts {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(texts)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, txt := range texts {
		stream := ""
		if txt != "" {
			stream = fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", txt)
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}

// zipWith writes a zip whose entries use compression method 99, which
// archive/zip lists but cannot open.
func unopenableZip(t *testing.T, path string, names ...string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateRaw(&zip.FileHeader{Name: n, Method: 99, CompressedSize64: 2, UncompressedSize64: 2})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("zz"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNativeExtractPDF(t *testing.T) {
	n := NewNativeExtractor()
	dir := t.TempDir()

	multi := filepath.Join(dir, "multi.pdf")
	if err := os.WriteFile(multi, minimalPDF("Hello first page", "", "Second page text"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := n.Extract(multi)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, want := range []string{"# multi.pdf", "## Page 1", "Hello first page", "## Page 3", "Second page text"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## Page 2") {
		t.Errorf("a page without text must not get a heading:\n%s", out)
	}

	single := filepath.Join(dir, "single.pdf")
	if err := os.WriteFile(single, minimalPDF("Only page"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = n.Extract(single)
	if err != nil || !strings.Contains(out, "Only page") || strings.Contains(out, "## Page") {
		t.Errorf("single page = %q, %v (no page heading expected)", out, err)
	}

	bad := filepath.Join(dir, "bad.pdf")
	if err := os.WriteFile(bad, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Extract(bad); err == nil || !strings.Contains(err.Error(), "open pdf") {
		t.Errorf("garbage pdf: err = %v", err)
	}
}

func TestNativeExtractErrors(t *testing.T) {
	n := NewNativeExtractor()
	dir := t.TempDir()

	t.Run("csv read failure", func(t *testing.T) {
		d := filepath.Join(dir, "dir.csv")
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Extract(d); err == nil {
			t.Fatal("reading a directory as csv must fail")
		}
	})
	t.Run("docx without document.xml", func(t *testing.T) {
		p := filepath.Join(dir, "empty.docx")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("word/other.xml")
		_, _ = w.Write([]byte("<x/>"))
		_ = zw.Close()
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Extract(p); err == nil || !strings.Contains(err.Error(), "word/document.xml not found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("docx part that cannot be opened", func(t *testing.T) {
		p := filepath.Join(dir, "broken.docx")
		unopenableZip(t, p, "word/document.xml")
		if _, err := n.Extract(p); err == nil {
			t.Fatal("an undecodable document.xml must fail")
		}
	})
	t.Run("xlsx that is not a workbook", func(t *testing.T) {
		p := filepath.Join(dir, "junk.xlsx")
		if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Extract(p); err == nil || !strings.Contains(err.Error(), "open xlsx") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("pptx that is not a zip", func(t *testing.T) {
		p := filepath.Join(dir, "junk.pptx")
		if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Extract(p); err == nil || !strings.Contains(err.Error(), "open pptx") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("docx that is not a zip", func(t *testing.T) {
		p := filepath.Join(dir, "junk.docx")
		if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Extract(p); err == nil || !strings.Contains(err.Error(), "open docx") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("pptx with a gap in the slide numbers", func(t *testing.T) {
		p := filepath.Join(dir, "gap.pptx")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, name := range []string{"ppt/slides/slide1.xml", "ppt/slides/slide3.xml"} {
			w, _ := zw.Create(name)
			_, _ = w.Write([]byte(`<p:sld xmlns:a="a"><a:t>text of ` + name + `</a:t></p:sld>`))
		}
		_ = zw.Close()
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := n.Extract(p)
		if err != nil || !strings.Contains(out, "slide1.xml") {
			t.Fatalf("out = %q, err = %v", out, err)
		}
		if strings.Contains(out, "slide3.xml") {
			t.Errorf("slides are read by consecutive number, so slide3 is unreachable: %s", out)
		}
	})
	t.Run("pptx slide that cannot be opened is skipped", func(t *testing.T) {
		p := filepath.Join(dir, "broken.pptx")
		unopenableZip(t, p, "ppt/slides/slide1.xml")
		out, err := n.Extract(p)
		if err != nil || !strings.HasPrefix(out, "# broken.pptx") || strings.Contains(out, "Slide 1") {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
	t.Run("missing text and html", func(t *testing.T) {
		for _, name := range []string{"absent.txt", "absent.html"} {
			if _, err := n.Extract(filepath.Join(dir, name)); err == nil {
				t.Errorf("%s: a missing file must be an error", name)
			}
		}
	})
}

func TestNativeExtractXLSXEmptySheets(t *testing.T) {
	f := excelize.NewFile()
	if _, err := f.NewSheet("Blank"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue("Sheet1", "A1", "kept"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "two.xlsx")
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	out, err := NewNativeExtractor().Extract(p)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(out, "## Sheet1") || !strings.Contains(out, "kept") || !strings.Contains(out, "## Blank") {
		t.Errorf("output:\n%s", out)
	}
}
