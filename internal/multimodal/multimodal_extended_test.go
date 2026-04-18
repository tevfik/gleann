package multimodal

import "testing"

func TestDetectMediaTypeExtended(t *testing.T) {
	tests := []struct {
		path string
		want MediaType
	}{
		{"image.png", MediaTypeImage},
		{"image.PNG", MediaTypeImage},
		{"photo.jpg", MediaTypeImage},
		{"photo.jpeg", MediaTypeImage},
		{"anim.gif", MediaTypeImage},
		{"pic.webp", MediaTypeImage},
		{"pic.bmp", MediaTypeImage},
		{"pic.tiff", MediaTypeImage},
		{"pic.svg", MediaTypeImage},
		{"song.mp3", MediaTypeAudio},
		{"sound.wav", MediaTypeAudio},
		{"track.flac", MediaTypeAudio},
		{"audio.ogg", MediaTypeAudio},
		{"voice.m4a", MediaTypeAudio},
		{"video.mp4", MediaTypeVideo},
		{"clip.avi", MediaTypeVideo},
		{"movie.mkv", MediaTypeVideo},
		{"screen.mov", MediaTypeVideo},
		{"web.webm", MediaTypeVideo},
		{"code.go", MediaTypeUnknown},
		{"readme.md", MediaTypeUnknown},
		{"data.json", MediaTypeUnknown},
		{"", MediaTypeUnknown},
		{"no_extension", MediaTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := DetectMediaType(tt.path)
			if got != tt.want {
				t.Errorf("DetectMediaType(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsMultimodalExtended(t *testing.T) {
	multimodal := []string{"photo.jpg", "song.mp3", "video.mp4", "pic.png", "audio.wav"}
	for _, f := range multimodal {
		if !IsMultimodal(f) {
			t.Errorf("IsMultimodal(%q) = false, want true", f)
		}
	}

	notMultimodal := []string{"code.go", "readme.md", "data.csv", "", "noext"}
	for _, f := range notMultimodal {
		if IsMultimodal(f) {
			t.Errorf("IsMultimodal(%q) = true, want false", f)
		}
	}
}

func TestApplyModelHeuristics(t *testing.T) {
	tests := []struct {
		name       string
		modelName  string
		wantVision bool
		wantAudio  bool
	}{
		{"gemma4", "gemma4:4b", true, true},
		{"llava", "llava:7b", true, false},
		{"qwen3-vl", "qwen3-vl:7b", true, false},
		{"moondream", "moondream2:1.8b", true, false},
		{"unknown", "llama3.2", false, false},
		{"minicpm-v", "minicpm-v:latest", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := &ModelCapabilities{Name: tt.modelName}
			applyModelHeuristics(caps)
			if caps.Vision != tt.wantVision {
				t.Errorf("Vision = %v, want %v", caps.Vision, tt.wantVision)
			}
			if caps.Audio != tt.wantAudio {
				t.Errorf("Audio = %v, want %v", caps.Audio, tt.wantAudio)
			}
		})
	}
}

func TestNewProcessorExtended(t *testing.T) {
	p := NewProcessor("http://localhost:11434", "gemma4")
	if p.OllamaHost != "http://localhost:11434" {
		t.Errorf("OllamaHost = %q", p.OllamaHost)
	}
	if p.Model != "gemma4" {
		t.Errorf("Model = %q", p.Model)
	}

	// Empty host defaults
	p = NewProcessor("", "")
	if p.OllamaHost != "http://localhost:11434" {
		t.Errorf("expected default host, got %q", p.OllamaHost)
	}
}

func TestProcessorCanProcess(t *testing.T) {
	p := &Processor{Model: "gemma4"}
	if !p.CanProcess("image.jpg") {
		t.Error("should be able to process jpg with model set")
	}
	if p.CanProcess("code.go") {
		t.Error("should not process .go files")
	}

	p = &Processor{Model: ""}
	if p.CanProcess("image.jpg") {
		t.Error("should not process without model")
	}
}

func TestDescriptionPromptExtended(t *testing.T) {
	tests := []struct {
		mt       MediaType
		filename string
		contains string
	}{
		{MediaTypeImage, "photo.jpg", "image"},
		{MediaTypeAudio, "song.mp3", "audio"},
		{MediaTypeVideo, "clip.mp4", "video"},
		{MediaTypeUnknown, "file.dat", "content"},
	}

	for _, tt := range tests {
		got := descriptionPrompt(tt.mt, tt.filename)
		if got == "" {
			t.Errorf("descriptionPrompt(%d, %q) returned empty", tt.mt, tt.filename)
		}
	}
}

func TestDetectCapabilitiesHeuristic(t *testing.T) {
	// Empty host → skips HTTP call, applies heuristics only.
	caps := DetectCapabilities("", "gemma4")
	if !caps.Vision {
		t.Error("gemma4 should have vision via heuristics")
	}
	if !caps.Audio {
		t.Error("gemma4 should have audio via heuristics")
	}
	if !caps.Multimodal {
		t.Error("gemma4 should be multimodal")
	}

	// Both empty → no capabilities.
	caps = DetectCapabilities("", "")
	if caps.Vision || caps.Audio || caps.Multimodal {
		t.Error("empty model should have no capabilities")
	}
}

func TestMediaTypeConstants(t *testing.T) {
	if MediaTypeUnknown != 0 {
		t.Error("MediaTypeUnknown should be 0")
	}
	if MediaTypeImage <= MediaTypeUnknown {
		t.Error("MediaTypeImage should be > Unknown")
	}
	if MediaTypeAudio <= MediaTypeImage {
		t.Error("MediaTypeAudio should be > Image")
	}
}
