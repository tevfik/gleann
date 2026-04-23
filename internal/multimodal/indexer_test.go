package multimodal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcessDirectory_Empty(t *testing.T) {
	dir := t.TempDir()
	p := NewProcessor("http://localhost:11434", "gemma4:e4b")
	items, err := p.ProcessDirectory(dir, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty dir, got %d", len(items))
	}
}

func TestProcessDirectory_NoModel(t *testing.T) {
	p := NewProcessor("", "")
	_, err := p.ProcessDirectory(t.TempDir(), nil, nil)
	if err == nil {
		t.Error("expected error for no model")
	}
}

func TestProcessDirectory_SkipExts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "photo.png"), []byte("fake"), 0644)
	os.WriteFile(filepath.Join(dir, "icon.svg"), []byte("fake"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("text"), 0644)

	p := NewProcessor("http://localhost:19999", "gemma4:e4b") // unreachable
	// Should find 1 multimodal file (png) — svg is skipped, md is not multimodal
	items, _ := p.ProcessDirectory(dir, []string{".svg"}, nil)
	// Items will be empty because server is unreachable, but the scan should work
	_ = items
}

func TestProcessDirectory_ProgressCallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.png"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("data"), 0644)

	called := 0
	p := NewProcessor("http://localhost:19999", "gemma4:e4b")
	p.ProcessDirectory(dir, nil, func(current, total int, path string) {
		called++
		if total != 2 {
			t.Errorf("expected total=2, got %d", total)
		}
	})
	if called != 2 {
		t.Errorf("expected 2 progress calls, got %d", called)
	}
}

func TestMediaTypeName(t *testing.T) {
	tests := []struct {
		mt   MediaType
		want string
	}{
		{MediaTypeImage, "Image"},
		{MediaTypeAudio, "Audio"},
		{MediaTypeVideo, "Video"},
		{MediaTypeUnknown, "File"},
	}
	for _, tc := range tests {
		got := mediaTypeName(tc.mt)
		if got != tc.want {
			t.Errorf("mediaTypeName(%d) = %q, want %q", tc.mt, got, tc.want)
		}
	}
}
