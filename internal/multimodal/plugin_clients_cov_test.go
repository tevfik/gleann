package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempFile creates a small file for upload tests.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// closedServerURL returns the URL of a server that has already been shut
// down, so connecting to it fails.
func closedServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

func TestTranscribeViaPlugin_ResponseShapes(t *testing.T) {
	audio := writeTempFile(t, "a.mp3", "audio-bytes")
	tests := []struct {
		name string
		body string
		want string
	}{
		{"markdown preferred", `{"markdown":"md","text":"t","transcript":"tr"}`, "md"},
		{"text fallback", `{"text":"plain text","transcript":"tr"}`, "plain text"},
		{"transcript fallback", `{"transcript":"alt name"}`, "alt name"},
		{"json without text returns raw body", `{"language":"en"}`, `{"language":"en"}`},
		{"non-json body is trimmed text", "  just words \n", "just words"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			got, err := transcribeViaPlugin(srv.URL+"/", audio)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTranscribeViaPlugin_SendsBearerToken(t *testing.T) {
	t.Setenv("GLEANN_AUDIO_PLUGIN_TOKEN", "s3cret")
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()
	if _, err := transcribeViaPlugin(srv.URL, writeTempFile(t, "a.wav", "x")); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer s3cret" || path != "/convert" {
		t.Errorf("auth=%q path=%q", auth, path)
	}
}

func TestTranscribeViaPlugin_Errors(t *testing.T) {
	audio := writeTempFile(t, "a.mp3", "audio-bytes")
	status500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer status500.Close()

	tests := []struct {
		name, url, path, wantErr string
	}{
		{"missing file", "http://127.0.0.1:1", filepath.Join(t.TempDir(), "nope.mp3"), "open audio"},
		{"path is a directory", "http://127.0.0.1:1", t.TempDir(), "copy audio"},
		{"invalid url", "http://127.0.0.1:1\x7f", audio, ""},
		{"server down", closedServerURL(t), audio, "plugin call"},
		{"non-200 status", status500.URL, audio, "plugin returned 500"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := transcribeViaPlugin(tc.url, tc.path)
			if err == nil {
				t.Fatalf("expected error, got %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCallVisionPlugin_Errors(t *testing.T) {
	img := writeTempFile(t, "i.png", "png-bytes")
	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer badJSON.Close()
	status503 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer status503.Close()

	tests := []struct {
		name, url, path, wantErr string
	}{
		{"missing file", "http://127.0.0.1:1", filepath.Join(t.TempDir(), "nope.png"), "open"},
		{"path is a directory", "http://127.0.0.1:1", t.TempDir(), "copy"},
		{"invalid url", "http://127.0.0.1:1\x7f", img, ""},
		{"server down", closedServerURL(t), img, "vision plugin call"},
		{"non-200 status", status503.URL, img, "vision plugin 503: busy"},
		{"undecodable body", badJSON.URL, img, "decode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vr, err := CallVisionPlugin(tc.url, tc.path)
			if err == nil {
				t.Fatalf("expected error, got %+v", vr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCallVisionPlugin_SendsBearerToken(t *testing.T) {
	t.Setenv("GLEANN_VISION_PLUGIN_TOKEN", "tok")
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"schema_version":2,"width":3}`))
	}))
	defer srv.Close()
	vr, err := CallVisionPlugin(srv.URL, writeTempFile(t, "i.png", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" || vr.Width != 3 {
		t.Errorf("auth=%q result=%+v", auth, vr)
	}
}

func TestCache_NoHomeDirDisablesCache(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", "")
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("home directory resolvable without HOME on this platform")
	}
	if got := cacheRoot(); got != "" {
		t.Errorf("cacheRoot = %q, want empty", got)
	}
	if _, ok := loadCached("abc"); ok {
		t.Error("loadCached hit without a cache root")
	}
	storeCached("abc", cachedDescription{Description: "x"}) // must not panic or write
}

func TestCache_UnreadableEntryIsMiss(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", dir)
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "")
	// A directory where the entry file should be: ReadFile fails with an
	// error other than "not exist".
	if err := os.Mkdir(filepath.Join(dir, "fp1.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := cacheMisses.Load()
	if _, ok := loadCached("fp1"); ok {
		t.Fatal("unreadable entry reported as hit")
	}
	// A corrupt entry is also a miss.
	if err := os.WriteFile(filepath.Join(dir, "fp2.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadCached("fp2"); ok {
		t.Fatal("corrupt entry reported as hit")
	}
	if got := cacheMisses.Load() - before; got != 2 {
		t.Errorf("misses delta = %d, want 2", got)
	}
}

func TestFileFingerprint_Errors(t *testing.T) {
	if _, err := fileFingerprint(filepath.Join(t.TempDir(), "missing"), "m", "en"); err == nil {
		t.Error("missing file: expected error")
	}
	if _, err := fileFingerprint(t.TempDir(), "m", "en"); err == nil {
		t.Error("directory: expected read error")
	}
}
