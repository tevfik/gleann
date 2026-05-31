package multimodal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// TestPluginIntegration_AudioVisionAutoWire spins up two stub plugin HTTP
// servers (audio + vision), writes a registry pointing at them, then asks
// the PluginManager to resolve the multimodal env vars and confirms both
// audio_plugin.go and vision_plugin.go pick the URLs up via env.
func TestPluginIntegration_AudioVisionAutoWire(t *testing.T) {
	// --- stub vision plugin (schema v2 contract) ----------------------
	visionTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok", "ready": true, "schema_version": 2,
			})
		case "/info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "gleann-plugin-vision", "version": "0.1.0", "schema_version": 2,
				"capabilities": []string{gleann.CapVisionExtraction},
			})
		case "/convert":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version": 2,
				"markdown":       "stub-vision-md",
				"ocr_text":       "hello",
				"clip_embedding": []float32{0.1, 0.2, 0.3},
				"width":          64, "height": 64,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer visionTS.Close()

	// --- stub sound plugin (schema v2 audio contract) -----------------
	audioTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok", "ready": true, "schema_version": 2,
			})
		case "/convert":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version": 2,
				"markdown":       "# Transcription\n\nhello world",
				"text":           "hello world",
				"language":       "en",
				"segments": []map[string]any{
					{"start": 0.0, "end": 1.0, "text": "hello world"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer audioTS.Close()

	// --- write a temp registry --------------------------------------
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "")
	t.Setenv("GLEANN_VISION_PLUGIN_URL", "")

	registry := gleann.PluginRegistry{Plugins: []gleann.Plugin{
		{
			Name:          "gleann-plugin-sound",
			URL:           audioTS.URL,
			Capabilities:  []string{gleann.CapAudioTranscription, gleann.CapDocumentExtraction},
			Extensions:    []string{".wav", ".mp3"},
			SchemaVersion: 2,
			Version:       "0.2.0",
		},
		{
			Name:          "gleann-plugin-vision",
			URL:           visionTS.URL,
			Capabilities:  []string{gleann.CapVisionExtraction},
			Extensions:    []string{".png", ".jpg"},
			SchemaVersion: 2,
			Version:       "0.1.0",
		},
	}}
	if err := os.MkdirAll(filepath.Join(tmpHome, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(registry, "", "  ")
	if err := os.WriteFile(filepath.Join(tmpHome, ".gleann", "plugins.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	// --- auto-discover ---------------------------------------------
	mgr, err := gleann.NewPluginManager()
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	audioURL, visionURL := mgr.ResolveMultimodalPluginEnv()
	if audioURL != audioTS.URL {
		t.Fatalf("audio URL not auto-discovered: %q vs %q", audioURL, audioTS.URL)
	}
	if visionURL != visionTS.URL {
		t.Fatalf("vision URL not auto-discovered: %q vs %q", visionURL, visionTS.URL)
	}

	// --- audio path: transcribeViaPlugin must hit the stub ----------
	tmpAudio := filepath.Join(t.TempDir(), "clip.wav")
	if err := os.WriteFile(tmpAudio, []byte("not-real-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript, err := transcribeViaPlugin(audioURL, tmpAudio)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transcript, "hello world") {
		t.Fatalf("audio plugin did not return transcript: %q", transcript)
	}

	// --- vision path: CallVisionPlugin returns enriched VisionResult ---
	tmpImg := filepath.Join(t.TempDir(), "x.png")
	if err := os.WriteFile(tmpImg, []byte("not-real-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	vr, err := CallVisionPlugin(visionURL, tmpImg)
	if err != nil {
		t.Fatal(err)
	}
	if vr.SchemaVersion != 2 || vr.OCRText != "hello" || len(vr.CLIPEmbedding) != 3 {
		t.Fatalf("unexpected vision result: %+v", vr)
	}

	// --- version-management surface: plugin list + info ---------------
	caps := mgr.FindByCapability(gleann.CapAudioTranscription, ".wav")
	if caps == nil || caps.Name != "gleann-plugin-sound" {
		t.Fatalf("audio capability lookup failed: %+v", caps)
	}
	caps = mgr.FindByCapability(gleann.CapVisionExtraction, ".png")
	if caps == nil || caps.Name != "gleann-plugin-vision" {
		t.Fatalf("vision capability lookup failed: %+v", caps)
	}

	// Verify FetchPluginInfo round-trip against the live stub.
	info, err := mgr.FetchPluginInfo(&registry.Plugins[1])
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.1.0" || info.SchemaVersion != 2 {
		t.Fatalf("/info mismatch: %+v", info)
	}

	_ = bytes.Buffer{} // keep import
}
