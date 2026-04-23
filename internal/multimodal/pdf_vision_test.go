package multimodal

import (
	"os"
	"path/filepath"
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
	if !pdfContains(prompt, "page 3") {
		t.Error("expected page number in prompt")
	}
}

func TestPdfPagePrompt_WithMarkerText(t *testing.T) {
	prompt := pdfPagePrompt(1, "Some extracted text from marker")
	if !pdfContains(prompt, "Some extracted text") {
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

func pdfContains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && pdfContainsLower(s, substr)
}

func pdfContainsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
