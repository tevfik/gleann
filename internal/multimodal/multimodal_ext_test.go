package multimodal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestProcessFile_NoFile(t *testing.T) {
	p := NewProcessor("http://localhost:11434", "gemma4:e4b")
	result := p.ProcessFile("/nonexistent/photo.png")
	if result.Error == nil {
		t.Error("expected error for nonexistent file")
	}
	if result.MediaType != MediaTypeImage {
		t.Errorf("expected image media type for .png path, got %v", result.MediaType)
	}
}

func TestProcessFile_TooLarge(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "huge.png")
	fh, _ := os.Create(f)
	fh.Truncate(51 << 20) // 51MB > 50MB limit
	fh.Close()

	p := NewProcessor("http://localhost:11434", "gemma4:e4b")
	result := p.ProcessFile(f)
	if result.Error == nil {
		t.Error("expected error for oversized file")
	}
	if result.MediaType != MediaTypeImage {
		t.Errorf("expected image type, got %v", result.MediaType)
	}
}

func TestProcessFile_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]string{
				"content": "A photograph showing a cat sitting on a keyboard.",
			},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	f := filepath.Join(dir, "cat.png")
	os.WriteFile(f, []byte("fake-png-data"), 0644)

	p := NewProcessor(srv.URL, "gemma4:e4b")
	result := p.ProcessFile(f)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Description == "" {
		t.Error("expected non-empty description")
	}
	if result.MediaType != MediaTypeImage {
		t.Errorf("expected image type, got %v", result.MediaType)
	}
	if result.FilePath != f {
		t.Errorf("expected path %s, got %s", f, result.FilePath)
	}
}

func TestProcessFile_OllamaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not loaded"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.jpg")
	os.WriteFile(f, []byte("jpg-data"), 0644)

	p := NewProcessor(srv.URL, "gemma4:e4b")
	result := p.ProcessFile(f)
	if result.Error == nil {
		t.Error("expected error for 500 response")
	}
}

func TestProcessFile_Unreachable(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "unreachable.png")
	os.WriteFile(f, []byte("data"), 0644)

	p := NewProcessor("http://localhost:19998", "gemma4:e4b")
	result := p.ProcessFile(f)
	if result.Error == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestProcessFile_AudioType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]string{
				"content": "An audio recording of a meeting discussion.",
			},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	f := filepath.Join(dir, "meeting.mp3")
	os.WriteFile(f, []byte("fake-audio"), 0644)

	p := NewProcessor(srv.URL, "gemma4:e4b")
	result := p.ProcessFile(f)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.MediaType != MediaTypeAudio {
		t.Errorf("expected audio type, got %v", result.MediaType)
	}
}

func TestDescriptionPrompt_AllTypes(t *testing.T) {
	types := []struct {
		mt   MediaType
		want string
	}{
		{MediaTypeImage, "Describe this image"},
		{MediaTypeAudio, "Transcribe"},
		{MediaTypeVideo, "Describe this video"},
		{MediaTypeUnknown, "Describe the content"},
	}
	for _, tc := range types {
		got := descriptionPrompt(tc.mt, "test.file")
		if got == "" {
			t.Errorf("empty prompt for type %d", tc.mt)
		}
		if len(tc.want) > 0 && !contains(got, tc.want) {
			t.Errorf("prompt for type %d: expected to contain %q, got %q", tc.mt, tc.want, got)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDetectCapabilities_VisionFamily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"modelfile": "",
			"details": map[string]interface{}{
				"families":       []string{"clip", "llama"},
				"parameter_size": "7B",
			},
		})
	}))
	defer srv.Close()

	caps := DetectCapabilities(srv.URL, "custom-model")
	if !caps.Vision {
		t.Error("expected vision from clip family")
	}
}

func TestDetectCapabilities_EmptyHost(t *testing.T) {
	caps := DetectCapabilities("", "gemma4:e4b")
	// Should fall through to heuristics
	if !caps.Multimodal {
		t.Error("expected multimodal from heuristic")
	}
}

func TestDetectCapabilities_EmptyModel(t *testing.T) {
	caps := DetectCapabilities("http://localhost:11434", "")
	if caps.Multimodal {
		t.Error("expected non-multimodal for empty model name")
	}
}

func TestAutoDetectModel_PreferenceOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]string{
				{"name": "llava:7b"},
				{"name": "qwen3-vl:latest"},
				{"name": "moondream:latest"},
			},
		})
	}))
	defer srv.Close()

	model := AutoDetectModel(srv.URL)
	// qwen3-vl should be preferred over llava and moondream
	if model != "qwen3-vl:latest" {
		t.Errorf("expected qwen3-vl:latest (higher priority), got %s", model)
	}
}
