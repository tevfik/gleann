// Package multimodal — scene-change keyframe behavioural tests (Bug #5).
package multimodal

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// hasFFmpeg returns true when the system can synthesize and analyse video
// for the scene-change tests; CI runners without ffmpeg cleanly skip.
func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// synthVideo generates a tiny 5-second test video with two distinct scenes:
// 2.5s of solid red followed by 2.5s of solid blue. ffmpeg's scene
// detector should flag the cut at t≈2.5s, demonstrating the new pipeline
// captures it rather than uniformly sampling 8 mid-frames.
func synthVideo(t *testing.T) string {
	t.Helper()
	if !hasFFmpeg() {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "two-scenes.mp4")
	// concat filter: 2.5s red, 2.5s blue.
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=160x90:d=2.5:r=10",
		"-f", "lavfi", "-i", "color=c=blue:s=160x90:d=2.5:r=10",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
		"-map", "[v]", "-pix_fmt", "yuv420p", out,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg synth failed (likely missing libavfilter feature): %v\n%s", err, combined)
	}
	return out
}

// TestExtractFrames_SceneChange verifies that with SceneThreshold > 0 the
// extracted frame count is small (one per visual scene) instead of the full
// MaxFrames uniformly-sampled count.
func TestExtractFrames_SceneChange(t *testing.T) {
	video := synthVideo(t)
	cfg := DefaultFrameConfig()
	cfg.MaxFrames = 8
	cfg.SceneThreshold = 0.3

	frames, err := ExtractFrames(video, cfg)
	if err != nil {
		t.Fatalf("ExtractFrames err: %v", err)
	}
	defer CleanupFrames(frames)

	// Two-scene synthetic clip should yield 1–2 frames in scene mode (the
	// initial frame may or may not be flagged depending on ffmpeg build).
	if len(frames) == 0 || len(frames) > 3 {
		t.Errorf("scene mode returned %d frames; want 1-3 for a 2-scene clip", len(frames))
	}
}

// TestExtractFrames_FixedFPSFallback verifies the legacy fixed-FPS path
// still works when SceneThreshold is explicitly 0.
func TestExtractFrames_FixedFPSFallback(t *testing.T) {
	video := synthVideo(t)
	cfg := DefaultFrameConfig()
	cfg.MaxFrames = 4
	cfg.SceneThreshold = 0
	frames, err := ExtractFrames(video, cfg)
	if err != nil {
		t.Fatalf("ExtractFrames err: %v", err)
	}
	defer CleanupFrames(frames)
	if len(frames) == 0 {
		t.Errorf("fixed-FPS mode returned 0 frames")
	}
}
