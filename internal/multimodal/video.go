// Package multimodal provides model-native multimodal processing for gleann.
package multimodal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// FrameExtractionConfig controls how frames are sampled from a video.
type FrameExtractionConfig struct {
	MaxFrames int     // Maximum number of frames to extract (default: 8).
	FPS       float64 // Frames per second to sample (0 = auto-calculate from MaxFrames).
	Width     int     // Resize width (0 = original).
	Quality   int     // JPEG quality 1-100 (default: 85).
}

// DefaultFrameConfig returns sensible defaults for frame extraction.
func DefaultFrameConfig() FrameExtractionConfig {
	return FrameExtractionConfig{
		MaxFrames: 8,
		Quality:   85,
	}
}

// ExtractedFrame represents a single frame extracted from a video.
type ExtractedFrame struct {
	Path      string  // Path to the extracted frame image.
	Timestamp float64 // Timestamp in seconds.
	Index     int     // Frame index (0-based).
}

// VideoAnalysis holds the results of video frame extraction and analysis.
type VideoAnalysis struct {
	SourcePath   string
	Frames       []ExtractedFrame
	Descriptions []string // One per frame, from multimodal model.
	Summary      string   // Combined summary of all frames.
	Duration     float64  // Video duration in seconds.
}

// ExtractFrames extracts keyframes from a video file using ffmpeg.
// Returns paths to extracted frame images in a temp directory.
// Requires ffmpeg to be installed.
func ExtractFrames(videoPath string, cfg FrameExtractionConfig) ([]ExtractedFrame, error) {
	if cfg.MaxFrames <= 0 {
		cfg.MaxFrames = 8
	}
	if cfg.Quality <= 0 || cfg.Quality > 100 {
		cfg.Quality = 85
	}

	// Check ffmpeg availability.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found: install ffmpeg for video frame extraction")
	}

	// Get video duration.
	duration, err := getVideoDuration(videoPath)
	if err != nil {
		return nil, fmt.Errorf("get duration: %w", err)
	}

	// Calculate FPS to get desired number of frames.
	fps := cfg.FPS
	if fps <= 0 && duration > 0 {
		fps = float64(cfg.MaxFrames) / duration
		if fps > 1.0 {
			fps = 1.0 // cap at 1 fps
		}
	}
	if fps <= 0 {
		fps = 0.5 // fallback: 1 frame every 2 seconds
	}

	// Create temp directory for frames.
	outDir, err := os.MkdirTemp("", "gleann-frames-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// Build ffmpeg command.
	pattern := filepath.Join(outDir, "frame_%04d.jpg")
	args := []string{
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=%.4f", fps),
		"-q:v", strconv.Itoa((100 - cfg.Quality) * 31 / 100), // ffmpeg quality: 2=best, 31=worst
		"-frames:v", strconv.Itoa(cfg.MaxFrames),
	}
	if cfg.Width > 0 {
		args = append(args, "-vf", fmt.Sprintf("fps=%.4f,scale=%d:-1", fps, cfg.Width))
	}
	args = append(args, pattern)

	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w\n%s", err, string(out))
	}

	// Collect extracted frames.
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("read output dir: %w", err)
	}

	var frames []ExtractedFrame
	for i, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jpg") {
			continue
		}
		timestamp := float64(i) / fps
		frames = append(frames, ExtractedFrame{
			Path:      filepath.Join(outDir, entry.Name()),
			Timestamp: timestamp,
			Index:     i,
		})
		if len(frames) >= cfg.MaxFrames {
			break
		}
	}

	return frames, nil
}

// AnalyzeVideo extracts frames from a video, processes each with the multimodal
// model, and returns a combined analysis with per-frame descriptions and a summary.
func (p *Processor) AnalyzeVideo(videoPath string, cfg FrameExtractionConfig) (*VideoAnalysis, error) {
	if p.Model == "" {
		return nil, fmt.Errorf("no multimodal model configured")
	}

	duration, _ := getVideoDuration(videoPath)

	frames, err := ExtractFrames(videoPath, cfg)
	if err != nil {
		return nil, fmt.Errorf("extract frames: %w", err)
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames extracted from %s", videoPath)
	}

	analysis := &VideoAnalysis{
		SourcePath: videoPath,
		Frames:     frames,
		Duration:   duration,
	}

	// Process each frame with the multimodal model.
	var allDescriptions []string
	for _, frame := range frames {
		result := p.ProcessFile(frame.Path)
		desc := result.Description
		if result.Error != nil {
			desc = fmt.Sprintf("[frame %d: error: %s]", frame.Index, result.Error)
		}
		analysis.Descriptions = append(analysis.Descriptions, desc)
		if result.Error == nil && desc != "" {
			allDescriptions = append(allDescriptions, fmt.Sprintf("Frame %d (%.1fs): %s", frame.Index, frame.Timestamp, desc))
		}
	}

	// Generate summary.
	if len(allDescriptions) > 0 {
		analysis.Summary = fmt.Sprintf("Video: %s (%.1fs, %d frames analyzed)\n\n%s",
			filepath.Base(videoPath), duration, len(allDescriptions),
			strings.Join(allDescriptions, "\n\n"))
	}

	return analysis, nil
}

// getVideoDuration uses ffprobe to get video duration in seconds.
func getVideoDuration(videoPath string) (float64, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0, fmt.Errorf("ffprobe not found")
	}

	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

// CleanupFrames removes the temporary frame directory.
func CleanupFrames(frames []ExtractedFrame) {
	if len(frames) == 0 {
		return
	}
	dir := filepath.Dir(frames[0].Path)
	if strings.Contains(dir, "gleann-frames-") {
		os.RemoveAll(dir)
	}
}
