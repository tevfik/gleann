// Package multimodal — audio plugin routing tests (Bug #1).
package multimodal

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSoundPlugin returns a server that mimics gleann-plugin-sound by
// extracting the uploaded file name and returning a deterministic
// transcript so the test can assert end-to-end routing.
func stubSoundPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/convert" {
			http.NotFound(w, r)
			return
		}
		// Parse multipart and echo the file name into the markdown body
		// so the test can verify the file actually reached the plugin.
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var fileName string
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if part.FormName() == "file" {
				fileName = part.FileName()
				_, _ = io.Copy(io.Discard, part)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"markdown":"transcript-of-` + fileName + `"}`))
	}))
}

// TestAudioRouting_ViaPlugin verifies that with GLEANN_AUDIO_PLUGIN_URL set
// an MP3 file lands in the plugin (not Ollama) and the transcript is
// surfaced through ProcessFile.
func TestAudioRouting_ViaPlugin(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	plugin := stubSoundPlugin(t)
	defer plugin.Close()
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", plugin.URL)

	dir := t.TempDir()
	audio := filepath.Join(dir, "meeting.mp3")
	_ = os.WriteFile(audio, []byte("RIFFfake-mp3-bytes"), 0o644)

	// Point Ollama at a black-hole port; if the audio ever reaches it the
	// test will fail with a connection error instead of using the plugin.
	proc := NewProcessor("http://127.0.0.1:1", "stub-model")
	r := proc.ProcessFile(audio)
	if r.Error != nil {
		t.Fatalf("err: %v", r.Error)
	}
	if !strings.Contains(r.Description, "transcript-of-meeting.mp3") {
		t.Errorf("description=%q, want transcript-of-meeting.mp3", r.Description)
	}
}

// TestAudioRouting_NoPluginNonAudioModel verifies the helpful error path:
// without a plugin and using a non-audio model, ProcessFile must refuse
// to send raw bytes to Ollama and instead surface a clear message.
func TestAudioRouting_NoPluginNonAudioModel(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "song.wav")
	_ = os.WriteFile(audio, []byte("RIFFfake-wav"), 0o644)

	proc := NewProcessor("http://127.0.0.1:1", "llava") // vision-only model
	r := proc.ProcessFile(audio)
	if r.Error == nil {
		t.Fatal("expected error for audio + vision-only model")
	}
	if !strings.Contains(r.Error.Error(), "audio file") {
		t.Errorf("err=%v, want guidance about audio", r.Error)
	}
}

// formDataContentType is a tiny helper to read multipart content type in
// case the multipart import is otherwise unused. Keeps go vet happy.
var _ = multipart.NewWriter
