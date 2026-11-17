package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginCallsSendBearerToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"markdown":"hello"}`))
	}))
	defer srv.Close()
	f := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GLEANN_AUDIO_PLUGIN_TOKEN", "tok-a")
	if _, err := transcribeViaPlugin(srv.URL, f); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok-a" {
		t.Fatalf("audio Authorization = %q", auth)
	}

	auth = ""
	t.Setenv("GLEANN_VISION_PLUGIN_TOKEN", "tok-v")
	_, _ = CallVisionPlugin(srv.URL, f)
	if auth != "Bearer tok-v" {
		t.Fatalf("vision Authorization = %q", auth)
	}

	auth = ""
	t.Setenv("GLEANN_AUDIO_PLUGIN_TOKEN", "")
	if _, err := transcribeViaPlugin(srv.URL, f); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("no token configured, yet Authorization = %q", auth)
	}
}
