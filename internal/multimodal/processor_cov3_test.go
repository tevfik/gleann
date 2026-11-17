package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// breakTempDir points the OS temp directory at a path that does not exist so
// os.MkdirTemp / os.CreateTemp fail.
func breakTempDir(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "no", "such", "tmp")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
}

func TestTryMarkerExtraction_Paths(t *testing.T) {
	pdf := writeTempFile(t, "doc.pdf", "%PDF-1.4 fake")
	tests := []struct {
		name    string
		health  int
		convert func(w http.ResponseWriter, r *http.Request)
		pdfPath string
		want    map[int]string
	}{
		{"markdown published under page 1", 200, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"markdown":"# Title"}`))
		}, pdf, map[int]string{1: "# Title"}},
		{"health not ok", 503, nil, pdf, nil},
		{"convert non-200", 200, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "x", 500)
		}, pdf, nil},
		{"convert invalid json", 200, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("nope"))
		}, pdf, nil},
		{"convert empty markdown", 200, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"markdown":""}`))
		}, pdf, nil},
		{"convert connection dropped", 200, func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		}, pdf, nil},
		{"pdf missing", 200, nil, filepath.Join(t.TempDir(), "missing.pdf"), nil},
		{"pdf is a directory", 200, nil, t.TempDir(), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health":
					w.WriteHeader(tc.health)
				case "/convert":
					if tc.convert == nil {
						t.Error("convert must not be called")
						return
					}
					tc.convert(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("GLEANN_MARKER_PLUGIN_URL", srv.URL+"/")
			got := tryMarkerExtraction(tc.pdfPath)
			if len(got) != len(tc.want) || got[1] != tc.want[1] {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenderTableMarkdown_NoHeaders(t *testing.T) {
	if got := renderTableMarkdown(&Table{Rows: [][]string{{"a"}}}); got != "" {
		t.Errorf("table without headers rendered %q, want empty", got)
	}
}

func TestCollectPageImages_MissingDirAndSubdirs(t *testing.T) {
	if _, err := collectPageImages(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing dir: expected error")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"p-1.PNG", "p-2.jpeg", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := collectPageImages(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want only the two image files (directories and .txt skipped)", got)
	}
}

func TestPDFRendering_TempDirFailure(t *testing.T) {
	breakTempDir(t)
	if _, err := renderPDFPages("any.pdf", 72, 1); err == nil {
		t.Error("renderPDFPages: expected temp dir error")
	}
	if _, err := RenderPDFPageToBase64("any.pdf", 1, 0); err == nil {
		t.Error("RenderPDFPageToBase64: expected temp dir error")
	}
}

func TestExtractFirstFrame_TempFileFailure(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; the temp-file branch is only reached after the ffmpeg check")
	}
	breakTempDir(t)
	if _, err := extractFirstFrame("clip.gif"); err == nil || !strings.Contains(err.Error(), "temp file") {
		t.Errorf("expected temp file error, got %v", err)
	}
}

func TestDetectCapabilities_ShowResponses(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		body       string
		wantVision bool
		wantAudio  bool
	}{
		{"families with audio", "plain", `{"details":{"families":["gemma","whisper"]}}`, false, true},
		{"families with vision", "plain", `{"details":{"families":["clip"]}}`, true, false},
		// Undecodable response falls back to name heuristics (llava => vision).
		{"undecodable falls back to heuristics", "llava:latest", `not json`, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			caps := DetectCapabilities(srv.URL, tc.model)
			if caps.Vision != tc.wantVision || caps.Audio != tc.wantAudio {
				t.Errorf("vision=%v audio=%v, want vision=%v audio=%v", caps.Vision, caps.Audio, tc.wantVision, tc.wantAudio)
			}
		})
	}
}

func TestAutoDetectModel_UndecodableTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	if got := AutoDetectModel(srv.URL); got != "" {
		t.Errorf("AutoDetectModel = %q, want empty", got)
	}
}

func TestDescriptionPrompt_AllMediaTypesAndLanguages(t *testing.T) {
	tests := []struct {
		lang string
		mt   MediaType
		want string
	}{
		{"en", MediaTypeImage, "Describe this image"},
		{"en", MediaTypeAudio, "Transcribe and describe"},
		{"en", MediaTypeVideo, "Describe this video"},
		{"en", MediaTypeUnknown, "Describe the content of this file"},
		{"tr", MediaTypeImage, "görseli"},
		{"tr", MediaTypeAudio, "ses içeriğini"},
		{"tr", MediaTypeVideo, "video içeriğini"},
		{"tr", MediaTypeUnknown, "dosyanın içeriğini"},
	}
	for _, tc := range tests {
		p := &Processor{Lang: tc.lang}
		got := p.descriptionPrompt(tc.mt, "f.bin")
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "f.bin") {
			t.Errorf("lang=%s mt=%v: prompt %q lacks %q or the filename", tc.lang, tc.mt, got, tc.want)
		}
	}
}

func TestProcessFile_AudioPluginCacheAndFailure(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "")
	var calls atomic.Int32
	var fail atomic.Bool
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "gpu on fire", 500)
			return
		}
		_, _ = w.Write([]byte(`{"markdown":"hello world"}`))
	}))
	defer plugin.Close()
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", plugin.URL)
	p := &Processor{Lang: "en", PluginOnly: true} // no Ollama model
	audio := writeTempFile(t, "talk.mp3", "audio-bytes")

	first := p.ProcessFile(audio)
	if first.Error != nil || first.Description != "hello world" {
		t.Fatalf("first: %+v", first)
	}
	second := p.ProcessFile(audio)
	if second.Error != nil || second.Description != "hello world" {
		t.Fatalf("second: %+v", second)
	}
	if calls.Load() != 1 {
		t.Errorf("plugin called %d times, want 1 (second call served from cache)", calls.Load())
	}

	// A different file that fails at the plugin surfaces a wrapped error.
	fail.Store(true)
	bad := p.ProcessFile(writeTempFile(t, "other.mp3", "different-bytes"))
	if bad.Error == nil || !strings.Contains(bad.Error.Error(), "audio plugin transcription failed") {
		t.Errorf("expected plugin failure error, got %+v", bad)
	}
}

func TestProcessFile_OllamaUndecodableResponse(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<<not json>>"))
	}))
	defer srv.Close()
	p := &Processor{OllamaHost: srv.URL, Model: "llava", Lang: "en"}
	res := p.ProcessFile(writeTempFile(t, "i.png", "png"))
	if res.Error == nil || !strings.Contains(res.Error.Error(), "decode response") {
		t.Errorf("expected decode error, got %+v", res)
	}
}

func TestProcessDirectory_ErrorsAndEmptyDescriptions(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	t.Setenv("GLEANN_MULTIMODAL_WORKERS", "1")

	// Walking a missing directory is an error.
	p := &Processor{OllamaHost: "http://127.0.0.1:1", Model: "llava", Lang: "en"}
	if _, err := p.ProcessDirectory(filepath.Join(t.TempDir(), "missing"), nil, nil); err == nil {
		t.Error("missing directory: expected error")
	}

	// One image gets an empty description (skipped), one fails at Ollama
	// (skipped with a warning), one succeeds.
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			_, _ = w.Write([]byte(`{"message":{"content":""}}`))
		case 2:
			http.Error(w, "nope", 500)
		default:
			_, _ = w.Write([]byte(`{"message":{"content":"a cat"}}`))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p = &Processor{OllamaHost: srv.URL, Model: "llava", Lang: "en"}
	items, err := p.ProcessDirectory(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !strings.Contains(items[0].Text, "a cat") {
		t.Errorf("items = %+v, want exactly the successful description", items)
	}
}
