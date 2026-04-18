package multimodal

import (
	"testing"
)

// ── DetectMediaType deeper branches ───────────────────────────

func TestDetectMediaTypeCov2(t *testing.T) {
	tests := []struct {
		path string
		want MediaType
	}{
		{"photo.png", MediaTypeImage},
		{"photo.jpg", MediaTypeImage},
		{"photo.jpeg", MediaTypeImage},
		{"photo.gif", MediaTypeImage},
		{"photo.webp", MediaTypeImage},
		{"photo.bmp", MediaTypeImage},
		{"photo.tiff", MediaTypeImage},
		{"photo.svg", MediaTypeImage},
		{"song.mp3", MediaTypeAudio},
		{"song.wav", MediaTypeAudio},
		{"song.flac", MediaTypeAudio},
		{"song.ogg", MediaTypeAudio},
		{"song.m4a", MediaTypeAudio},
		{"song.aac", MediaTypeAudio},
		{"song.wma", MediaTypeAudio},
		{"song.opus", MediaTypeAudio},
		{"clip.mp4", MediaTypeVideo},
		{"clip.avi", MediaTypeVideo},
		{"clip.mkv", MediaTypeVideo},
		{"clip.mov", MediaTypeVideo},
		{"clip.webm", MediaTypeVideo},
		{"clip.flv", MediaTypeVideo},
		{"clip.wmv", MediaTypeVideo},
		{"doc.pdf", MediaTypeUnknown},
		{"readme.md", MediaTypeUnknown},
		{"noext", MediaTypeUnknown},
	}
	for _, tt := range tests {
		got := DetectMediaType(tt.path)
		if got != tt.want {
			t.Errorf("DetectMediaType(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// ── IsMultimodal deeper branches ──────────────────────────────

func TestIsMultimodalCov2(t *testing.T) {
	if !IsMultimodal("test.png") {
		t.Fatal("image should be multimodal")
	}
	if !IsMultimodal("test.mp3") {
		t.Fatal("audio should be multimodal")
	}
	if !IsMultimodal("test.mp4") {
		t.Fatal("video should be multimodal")
	}
	if IsMultimodal("test.txt") {
		t.Fatal("text should not be multimodal")
	}
}

// ── MediaType constants ───────────────────────────────────────

func TestMediaTypeStringsCov2(t *testing.T) {
	if MediaTypeUnknown != 0 {
		t.Fatal("unexpected")
	}
	if MediaTypeImage != 1 {
		t.Fatal("unexpected")
	}
	if MediaTypeAudio != 2 {
		t.Fatal("unexpected")
	}
	if MediaTypeVideo != 3 {
		t.Fatal("unexpected")
	}
}
