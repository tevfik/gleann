// Package multimodal — metadata extractor tests (Bug #9).
package multimodal

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makePNG writes a tiny solid-colour PNG to the given path so we can
// exercise the dimension-detection path without committing binary
// fixtures.
func makePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{0xff, 0x00, 0x00, 0xff})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestExtractFileMetadata_PNG verifies dimensions, format and stat info
// are filled in for a stdlib-supported format.
func TestExtractFileMetadata_PNG(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tiny.png")
	makePNG(t, p, 32, 24)
	md, err := ExtractFileMetadata(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if md.Width != 32 || md.Height != 24 {
		t.Errorf("dimensions=%dx%d, want 32x24", md.Width, md.Height)
	}
	if md.Format != "png" {
		t.Errorf("format=%q, want png", md.Format)
	}
	if md.SizeBytes <= 0 {
		t.Errorf("size=%d, want >0", md.SizeBytes)
	}
	if md.ModTime.IsZero() {
		t.Error("mod_time should be set")
	}
}

// TestExtractFileMetadata_Missing covers the error path.
func TestExtractFileMetadata_Missing(t *testing.T) {
	if _, err := ExtractFileMetadata(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Error("expected error for missing file")
	}
}

// TestExtractFileMetadata_NonImage verifies a non-image still returns
// basic stat metadata (no dimensions) without erroring.
func TestExtractFileMetadata_NonImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(p, []byte("not-an-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	md, err := ExtractFileMetadata(p)
	if err != nil {
		t.Fatal(err)
	}
	if md.Width != 0 || md.Height != 0 {
		t.Errorf("dimensions=%dx%d, want 0x0 for non-image", md.Width, md.Height)
	}
	if md.SizeBytes != 12 {
		t.Errorf("size=%d, want 12", md.SizeBytes)
	}
}
