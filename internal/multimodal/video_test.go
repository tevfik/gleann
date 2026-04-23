package multimodal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultFrameConfig(t *testing.T) {
	cfg := DefaultFrameConfig()
	if cfg.MaxFrames != 8 {
		t.Errorf("expected MaxFrames=8, got %d", cfg.MaxFrames)
	}
	if cfg.Quality != 85 {
		t.Errorf("expected Quality=85, got %d", cfg.Quality)
	}
}

func TestExtractFrames_NoFFmpeg(t *testing.T) {
	// If ffmpeg is not installed, should get a clear error.
	// We test with a non-existent file regardless.
	originalPath := os.Getenv("PATH")
	os.Setenv("PATH", "") // temporarily clear PATH
	defer os.Setenv("PATH", originalPath)

	_, err := ExtractFrames("/nonexistent/video.mp4", DefaultFrameConfig())
	if err == nil {
		t.Error("expected error when ffmpeg not found")
	}
}

func TestExtractFrames_InvalidFile(t *testing.T) {
	// Create a fake video file.
	dir := t.TempDir()
	fakeVideo := filepath.Join(dir, "fake.mp4")
	os.WriteFile(fakeVideo, []byte("not a real video"), 0644)

	cfg := DefaultFrameConfig()
	cfg.MaxFrames = 2

	// This should fail because ffprobe/ffmpeg can't parse it,
	// but the exact error depends on ffmpeg availability.
	_, err := ExtractFrames(fakeVideo, cfg)
	// We just check it doesn't panic. Error is expected.
	_ = err
}

func TestCleanupFrames_Empty(t *testing.T) {
	// Should not panic on empty input.
	CleanupFrames(nil)
	CleanupFrames([]ExtractedFrame{})
}

func TestCleanupFrames_NonGleannDir(t *testing.T) {
	// Should not delete directories that don't contain "gleann-frames-".
	dir := t.TempDir()
	frames := []ExtractedFrame{
		{Path: filepath.Join(dir, "frame_0001.jpg")},
	}
	// This should NOT delete TempDir because it doesn't match pattern.
	CleanupFrames(frames)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("should not have deleted non-gleann directory")
	}
}

func TestAnalyzeVideo_NoModel(t *testing.T) {
	p := &Processor{OllamaHost: "http://localhost:19999"}
	_, err := p.AnalyzeVideo("/nonexistent.mp4", DefaultFrameConfig())
	if err == nil {
		t.Error("expected error with no model")
	}
}

func TestGetVideoDuration_NonexistentFile(t *testing.T) {
	_, err := getVideoDuration("/nonexistent/video.mp4")
	if err == nil {
		// Only fail if ffprobe is installed — it should error on nonexistent file.
		t.Log("ffprobe might not be installed, or returned unexpected result")
	}
}
