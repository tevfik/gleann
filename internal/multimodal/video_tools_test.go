package multimodal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ffmpeg stand-in: the output pattern is the last argument. Writes two jpg
// frames; with a scene filter it "finds no cuts" and writes nothing, which
// exercises the fixed-FPS fallback. Every call is appended to $FFLOG.
func fakeFFmpegScript(log string) string {
	return `echo "$*" >> ` + log + `
for a; do last=$a; done
case "$*" in
  *scene*) exit 0 ;;
esac
case "$last" in
  *frame_%04d.jpg)
    d=${last%/*}
    printf one > "$d/frame_0001.jpg"
    printf two > "$d/frame_0002.jpg" ;;
  *) printf png > "$last" ;;
esac`
}

func newVideoFile(t *testing.T) string {
	return writeFile(t, filepath.Join(t.TempDir(), "clip.mp4"), "not really a video")
}

func TestExtractFramesFixedRate(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ffmpeg.log")
	fakeTools(t, map[string]string{"ffprobe": "echo 4.0", "ffmpeg": fakeFFmpegScript(log)})

	frames, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{MaxFrames: 2, Width: 320, Quality: 200})
	if err != nil {
		t.Fatal(err)
	}
	defer CleanupFrames(frames)
	if len(frames) != 2 {
		t.Fatalf("frames = %+v", frames)
	}
	// 2 frames over 4s = 0.5 fps: timestamps 0s and 2s.
	if frames[0].Timestamp != 0 || frames[1].Timestamp != 2 || frames[1].Index != 1 {
		t.Errorf("timestamps = %+v", frames)
	}
	args, _ := os.ReadFile(log)
	for _, want := range []string{"fps=0.5000,scale=320:-1", "-frames:v 2", "-q:v 4"} { // invalid quality -> 85 -> (100-85)*31/100
		if !strings.Contains(string(args), want) {
			t.Errorf("ffmpeg args %q lack %q", args, want)
		}
	}
}

func TestExtractFramesCapsToMaxFrames(t *testing.T) {
	fakeTools(t, map[string]string{"ffprobe": "echo 60", "ffmpeg": fakeFFmpegScript(filepath.Join(t.TempDir(), "log"))})
	frames, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{MaxFrames: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer CleanupFrames(frames)
	if len(frames) != 1 {
		t.Errorf("frames = %d, want the MaxFrames cap of 1", len(frames))
	}
}

func TestExtractFramesSceneModeFallsBackToFixedRate(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ffmpeg.log")
	fakeTools(t, map[string]string{"ffprobe": "echo 10", "ffmpeg": fakeFFmpegScript(log)})

	frames, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{MaxFrames: 4, SceneThreshold: 0.4, Width: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer CleanupFrames(frames)
	if len(frames) != 2 {
		t.Fatalf("fallback produced %d frames", len(frames))
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "gt(scene") || !strings.Contains(lines[0], "scale=100:-1") ||
		strings.Contains(lines[1], "scene") {
		t.Errorf("expected a scene call then a plain fps call, got:\n%s", calls)
	}
}

func TestExtractFramesFailures(t *testing.T) {
	t.Run("ffmpeg missing", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffprobe": "echo 4"})
		_, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{})
		if err == nil || !strings.Contains(err.Error(), "ffmpeg not found") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("duration unavailable", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffmpeg": "exit 0"})
		_, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{})
		if err == nil || !strings.Contains(err.Error(), "get duration") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("duration not a number", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffprobe": "echo N/A", "ffmpeg": "exit 0"})
		if _, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{}); err == nil {
			t.Error("expected an error for unparsable duration")
		}
	})
	t.Run("ffmpeg exits non-zero", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffprobe": "echo 4", "ffmpeg": "echo 'moov atom not found' >&2; exit 1"})
		_, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{})
		if err == nil || !strings.Contains(err.Error(), "moov atom not found") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("zero duration uses the fallback rate", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "log")
		fakeTools(t, map[string]string{"ffprobe": "echo 0", "ffmpeg": fakeFFmpegScript(log)})
		frames, err := ExtractFrames(newVideoFile(t), FrameExtractionConfig{})
		if err != nil {
			t.Fatal(err)
		}
		defer CleanupFrames(frames)
		if args, _ := os.ReadFile(log); !strings.Contains(string(args), "fps=0.5000") {
			t.Errorf("args = %s", args)
		}
	})
}

func TestAnalyzeVideoSummarisesFrames(t *testing.T) {
	isolateCache(t)
	fakeTools(t, map[string]string{"ffprobe": "echo 8.5", "ffmpeg": fakeFFmpegScript(filepath.Join(t.TempDir(), "log"))})
	stub := newChatStub(t, 0, func(string) string { return "a frame description" })

	an, err := NewProcessor(stub.srv.URL, "vlm").AnalyzeVideo(newVideoFile(t), FrameExtractionConfig{MaxFrames: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer CleanupFrames(an.Frames)
	if an.Duration != 8.5 || len(an.Descriptions) != 2 {
		t.Fatalf("analysis = %+v", an)
	}
	if !strings.Contains(an.Summary, "clip.mp4 (8.5s, 2 frames analyzed)") || !strings.Contains(an.Summary, "Frame 1 (") {
		t.Errorf("summary = %q", an.Summary)
	}
}

func TestAnalyzeVideoRecordsFrameErrors(t *testing.T) {
	isolateCache(t)
	fakeTools(t, map[string]string{"ffprobe": "echo 8", "ffmpeg": fakeFFmpegScript(filepath.Join(t.TempDir(), "log"))})
	stub := newChatStub(t, 500, nil)

	an, err := NewProcessor(stub.srv.URL, "vlm").AnalyzeVideo(newVideoFile(t), FrameExtractionConfig{MaxFrames: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer CleanupFrames(an.Frames)
	if len(an.Descriptions) != 2 || !strings.HasPrefix(an.Descriptions[0], "[frame 0: error:") {
		t.Errorf("descriptions = %q", an.Descriptions)
	}
	if an.Summary != "" {
		t.Errorf("no frame succeeded, summary must be empty: %q", an.Summary)
	}
}

func TestAnalyzeVideoNoFramesIsAnError(t *testing.T) {
	// ffmpeg succeeds but writes nothing, in plain (non-scene) mode.
	fakeTools(t, map[string]string{"ffprobe": "echo 8", "ffmpeg": "exit 0"})
	_, err := NewProcessor("http://127.0.0.1:1", "vlm").AnalyzeVideo(newVideoFile(t), FrameExtractionConfig{})
	if err == nil || !strings.Contains(err.Error(), "no frames extracted") {
		t.Errorf("err = %v", err)
	}
	_, err = NewProcessor("http://127.0.0.1:1", "").AnalyzeVideo(newVideoFile(t), FrameExtractionConfig{})
	if err == nil || !strings.Contains(err.Error(), "no multimodal model") {
		t.Errorf("no model: err = %v", err)
	}
}

func TestExtractFirstFrame(t *testing.T) {
	t.Run("writes a png", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffmpeg": fakeFFmpegScript(filepath.Join(t.TempDir(), "log"))})
		out, err := extractFirstFrame("anim.gif")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(out)
		if b, _ := os.ReadFile(out); string(b) != "png" || !strings.HasSuffix(out, ".png") {
			t.Errorf("out = %s (%q)", out, b)
		}
	})
	t.Run("ffmpeg missing", func(t *testing.T) {
		fakeTools(t, nil)
		if _, err := extractFirstFrame("anim.gif"); err == nil || !strings.Contains(err.Error(), "ffmpeg not found") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("ffmpeg fails and removes its temp file", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		fakeTools(t, map[string]string{"ffmpeg": "echo corrupt >&2; exit 1"})
		_, err := extractFirstFrame("anim.gif")
		if err == nil || !strings.Contains(err.Error(), "corrupt") {
			t.Errorf("err = %v", err)
		}
		if left, _ := filepath.Glob(filepath.Join(tmp, "gleann-frame-*")); len(left) != 0 {
			t.Errorf("temp frame leaked: %v", left)
		}
	})
}

func TestProcessFileVideoAndGIF(t *testing.T) {
	isolateCache(t)
	stub := newChatStub(t, 0, func(string) string { return "described" })
	p := NewProcessor(stub.srv.URL, "vlm")

	t.Run("video goes through frame analysis", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffprobe": "echo 3", "ffmpeg": fakeFFmpegScript(filepath.Join(t.TempDir(), "log"))})
		res := p.ProcessFile(newVideoFile(t))
		if res.Error != nil || !strings.Contains(res.Description, "frames analyzed") {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("video without ffmpeg is a clean error", func(t *testing.T) {
		fakeTools(t, nil)
		res := p.ProcessFile(writeFile(t, filepath.Join(t.TempDir(), "other.mp4"), "v"))
		if res.Error == nil || !strings.Contains(res.Error.Error(), "video indexing requires ffmpeg") {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("gif is described through its first frame", func(t *testing.T) {
		fakeTools(t, map[string]string{"ffmpeg": fakeFFmpegScript(filepath.Join(t.TempDir(), "log"))})
		res := p.ProcessFile(writeFile(t, filepath.Join(t.TempDir(), "anim.gif"), "GIF89a"))
		if res.Error != nil || res.Description != "described" {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("gif without ffmpeg is a clean error", func(t *testing.T) {
		fakeTools(t, nil)
		res := p.ProcessFile(writeFile(t, filepath.Join(t.TempDir(), "other.gif"), "GIF89a-no-ffmpeg"))
		if res.Error == nil || !strings.Contains(res.Error.Error(), "animated GIF indexing requires ffmpeg") {
			t.Errorf("result = %+v", res)
		}
	})
}

// ── exiftool enrichment ───────────────────────────────────────

func TestExtractFileMetadataExiftoolEnrichment(t *testing.T) {
	img := writeFile(t, filepath.Join(t.TempDir(), "p.jpg"), "not decodable")
	t.Setenv("GLEANN_EXIFTOOL", "1")
	fakeTools(t, map[string]string{"exiftool": `echo '[{"SourceFile":"x","Make":"  ACME ","Model":"","ImageWidth":4000,"GPSLatitude":52.520008,"Zoom":2.50,"Flash":true}]'`})
	md, err := ExtractFileMetadata(img)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Make": "ACME", "ImageWidth": "4000", "GPSLatitude": "52.520008", "Zoom": "2.5", "Flash": "true"}
	for k, v := range want {
		if md.Exif[k] != v {
			t.Errorf("Exif[%s] = %q, want %q (all: %v)", k, md.Exif[k], v, md.Exif)
		}
	}
	if _, ok := md.Exif["SourceFile"]; ok {
		t.Error("SourceFile must be dropped")
	}
	if _, ok := md.Exif["Model"]; ok {
		t.Error("blank values must be dropped")
	}
}

func TestExtractExifWithToolFailureModes(t *testing.T) {
	for name, scripts := range map[string]map[string]string{
		"not installed":  nil,
		"exits non-zero": {"exiftool": "exit 1"},
		"invalid json":   {"exiftool": "echo not-json"},
		"empty array":    {"exiftool": "echo '[]'"},
	} {
		t.Run(name, func(t *testing.T) {
			fakeTools(t, scripts)
			if got := extractExifWithTool("x.jpg"); got != nil {
				t.Errorf("got %v, want nil", got)
			}
		})
	}
}

func TestExifDisabledByDefault(t *testing.T) {
	t.Setenv("GLEANN_EXIFTOOL", "")
	img := writeFile(t, filepath.Join(t.TempDir(), "p.jpg"), "x")
	fakeTools(t, map[string]string{"exiftool": `echo '[{"Make":"ACME"}]'`})
	md, err := ExtractFileMetadata(img)
	if err != nil || md.Exif != nil {
		t.Errorf("exif collected although GLEANN_EXIFTOOL is unset: %+v, %v", md, err)
	}
}
