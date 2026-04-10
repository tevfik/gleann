package multimodal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectMediaType(t *testing.T) {
	tests := []struct {
		path     string
		expected MediaType
	}{
		{"photo.png", MediaTypeImage},
		{"photo.JPG", MediaTypeImage},
		{"photo.jpeg", MediaTypeImage},
		{"photo.webp", MediaTypeImage},
		{"song.mp3", MediaTypeAudio},
		{"song.WAV", MediaTypeAudio},
		{"song.flac", MediaTypeAudio},
		{"video.mp4", MediaTypeVideo},
		{"video.MKV", MediaTypeVideo},
		{"readme.md", MediaTypeUnknown},
		{"data.json", MediaTypeUnknown},
		{"", MediaTypeUnknown},
	}

	for _, tt := range tests {
		got := DetectMediaType(tt.path)
		if got != tt.expected {
			t.Errorf("DetectMediaType(%q) = %d, want %d", tt.path, got, tt.expected)
		}
	}
}

func TestIsMultimodal(t *testing.T) {
	if !IsMultimodal("photo.png") {
		t.Error("expected photo.png to be multimodal")
	}
	if !IsMultimodal("audio.mp3") {
		t.Error("expected audio.mp3 to be multimodal")
	}
	if IsMultimodal("readme.md") {
		t.Error("expected readme.md to NOT be multimodal")
	}
}

func TestDetectCapabilities_KnownModels(t *testing.T) {
	// Without an actual Ollama server, test the heuristic detection.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"modelfile": "",
			"details": map[string]interface{}{
				"families":       []string{"gemma"},
				"parameter_size": "4B",
			},
		})
	}))
	defer srv.Close()

	caps := DetectCapabilities(srv.URL, "gemma4:e4b")
	if !caps.Vision {
		t.Error("expected gemma4 to have vision")
	}
	if !caps.Audio {
		t.Error("expected gemma4 to have audio")
	}
	if !caps.Multimodal {
		t.Error("expected gemma4 to be multimodal")
	}
}

func TestDetectCapabilities_NonMultimodal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"modelfile": "",
			"details": map[string]interface{}{
				"families":       []string{"llama"},
				"parameter_size": "3B",
			},
		})
	}))
	defer srv.Close()

	caps := DetectCapabilities(srv.URL, "llama3.2:3b")
	if caps.Multimodal {
		t.Error("llama3.2 should NOT be multimodal")
	}
}

func TestDetectCapabilities_Unreachable(t *testing.T) {
	caps := DetectCapabilities("http://localhost:19999", "gemma4:e4b")
	// Should still detect via heuristic.
	if !caps.Multimodal {
		t.Error("expected gemma4 heuristic to set multimodal even when offline")
	}
}

func TestDescriptionPrompt(t *testing.T) {
	p := descriptionPrompt(MediaTypeImage, "chart.png")
	if p == "" {
		t.Error("expected non-empty prompt for image")
	}
	p = descriptionPrompt(MediaTypeAudio, "meeting.mp3")
	if p == "" {
		t.Error("expected non-empty prompt for audio")
	}
}

func TestNewProcessor(t *testing.T) {
	p := NewProcessor("http://localhost:11434", "gemma4:e4b")
	if p.Model != "gemma4:e4b" {
		t.Errorf("expected model gemma4:e4b, got %s", p.Model)
	}
	if p.OllamaHost != "http://localhost:11434" {
		t.Errorf("unexpected host: %s", p.OllamaHost)
	}
}

func TestNewProcessor_EnvVar(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_MODEL", "qwen3-vl:latest")
	p := NewProcessor("", "")
	if p.Model != "qwen3-vl:latest" {
		t.Errorf("expected GLEANN_MULTIMODAL_MODEL, got %s", p.Model)
	}
}

func TestCanProcess(t *testing.T) {
	p := NewProcessor("", "gemma4:e4b")
	if !p.CanProcess("photo.png") {
		t.Error("expected CanProcess=true for png with model set")
	}
	if p.CanProcess("readme.md") {
		t.Error("expected CanProcess=false for md")
	}

	pNoModel := NewProcessor("", "")
	if pNoModel.CanProcess("photo.png") {
		t.Error("expected CanProcess=false when no model set")
	}
}

func TestAutoDetectModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]string{
				{"name": "llama3:8b"},
				{"name": "bge-m3:latest"},
				{"name": "gemma4:e4b"},
			},
		})
	}))
	defer srv.Close()

	model := AutoDetectModel(srv.URL)
	if model != "gemma4:e4b" {
		t.Errorf("expected gemma4:e4b, got %s", model)
	}
}

func TestAutoDetectModel_NoMultimodal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]string{
				{"name": "llama3:8b"},
				{"name": "bge-m3:latest"},
			},
		})
	}))
	defer srv.Close()

	model := AutoDetectModel(srv.URL)
	if model != "" {
		t.Errorf("expected empty string when no multimodal model, got %s", model)
	}
}

func TestAutoDetectModel_Unreachable(t *testing.T) {
	model := AutoDetectModel("http://localhost:19999")
	if model != "" {
		t.Errorf("expected empty string, got %s", model)
	}
}

func TestProcessFile_NoModel(t *testing.T) {
	p := NewProcessor("", "")
	result := p.ProcessFile("photo.png")
	if result.Error == nil {
		t.Error("expected error when no model configured")
	}
}
