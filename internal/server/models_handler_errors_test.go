package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
)

func TestValidateModelFilename(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		wantErr string // substring; empty = valid
	}{
		{"plain gguf", "model.gguf", ""},
		{"upper-case ext", "Model.GGUF", ""},
		{"safetensors", "w.safetensors", ""},
		{"empty", "", "invalid filename"},
		{"dot", ".", "invalid filename"},
		{"dotdot", "..", "invalid filename"},
		{"subdir", "sub/model.gguf", "path separators"},
		{"backslash", `sub\model.gguf`, "path separators"},
		{"hidden", ".model.gguf", "must not start"},
		{"option-like", "-model.gguf", "must not start"},
		{"bad ext", "model.exe", "unsupported extension"},
		{"no ext", "model", "unsupported extension"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateModelFilename(tc.file)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("got %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateModelURL(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	tests := []struct {
		name, url, wantErr string
	}{
		{"unparsable", "http://[::1", "invalid url"},
		{"no host", "https:///x.gguf", "must include a host"},
		{"ftp", "ftp://example.com/x.gguf", "not allowed"},
		{"http public", "http://example.com/x.gguf", "plain http is only allowed for loopback"},
		{"loopback refused", "http://127.0.0.1:1/x.gguf", "loopback host"},
		{"unresolvable", "https://no-such-host.invalid/x.gguf", "cannot resolve"},
		{"private literal", "https://10.0.0.5/x.gguf", "restricted address"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateModelURL(tc.url)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("got %v, want substring %q", err, tc.wantErr)
			}
		})
	}

	t.Run("public literal accepted", func(t *testing.T) {
		u, err := validateModelURL("https://8.8.8.8/x.gguf")
		if err != nil || u.Host != "8.8.8.8" {
			t.Errorf("got %v, %v", u, err)
		}
	})
	t.Run("opt-out allows loopback http", func(t *testing.T) {
		t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")
		if _, err := validateModelURL("http://127.0.0.1:1/x.gguf"); err != nil {
			t.Errorf("opt-out: %v", err)
		}
	})
}

func TestModelDownloadClient_RedirectPolicy(t *testing.T) {
	check := modelDownloadClient.CheckRedirect
	mustReq := func(u string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if err := check(mustReq("https://example.com/x"), nil); err != nil {
		t.Errorf("https redirect refused: %v", err)
	}
	if err := check(mustReq("http://example.com/x"), nil); err == nil {
		t.Error("redirect to plain http on a public host must be refused")
	}
	via := make([]*http.Request, 10)
	if err := check(mustReq("https://example.com/x"), via); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Errorf("redirect cap: %v", err)
	}
}

func TestHandleDownloadModel_RequestErrors(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	bg := background.NewManager(1)
	defer bg.Stop()
	s := &Server{bgManager: bg}

	tests := []struct {
		name, method, body string
		want               int
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"bad json", http.MethodPost, "{", http.StatusBadRequest},
		{"missing fields", http.MethodPost, `{"url":"https://example.com/m.gguf"}`, http.StatusBadRequest},
		{"bad filename", http.MethodPost, `{"url":"https://example.com/m.gguf","filename":"../m.gguf"}`, http.StatusBadRequest},
		{"bad url", http.MethodPost, `{"url":"ftp://example.com/m.gguf","filename":"m.gguf"}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.handleDownloadModel(w, httptest.NewRequest(tc.method, "/api/models/download", strings.NewReader(tc.body)))
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}

	t.Run("no background manager", func(t *testing.T) {
		w := httptest.NewRecorder()
		(&Server{}).handleDownloadModel(w, httptest.NewRequest(http.MethodPost, "/api/models/download",
			strings.NewReader(`{"url":"https://example.com/m.gguf","filename":"m.gguf"}`)))
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "not initialized") {
			t.Errorf("got %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("models dir cannot be created", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GLEANN_CONFIG_DIR", blocker)
		w := httptest.NewRecorder()
		s.handleDownloadModel(w, httptest.NewRequest(http.MethodPost, "/api/models/download",
			strings.NewReader(`{"url":"https://example.com/m.gguf","filename":"m.gguf"}`)))
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "models directory") {
			t.Errorf("got %d %s", w.Code, w.Body.String())
		}
	})
}

func TestHandleListLocalModels_WrongMethod(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).handleListLocalModels(w, httptest.NewRequest(http.MethodPost, "/api/models/local", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", w.Code)
	}
}

// waitTask polls the manager until the task reaches a terminal state or
// the predicate matches.
func waitTask(t *testing.T, bg *background.Manager, id string, until func(*background.Task) bool) *background.Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if task := bg.Get(id); task != nil && until(task) {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach the expected state", id)
	return nil
}

func terminal(task *background.Task) bool {
	return task.Status == background.StatusCompleted || task.Status == background.StatusFailed
}

func startDownload(t *testing.T, s *Server, url, file string) string {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleDownloadModel(w, httptest.NewRequest(http.MethodPost, "/api/models/download",
		strings.NewReader(`{"url":"`+url+`","filename":"`+file+`"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("start download: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	i := strings.Index(body, `"task_id":"`)
	if i < 0 {
		t.Fatalf("no task id in %s", body)
	}
	body = body[i+len(`"task_id":"`):]
	return body[:strings.Index(body, `"`)]
}

func isolateModelsDir(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("GLEANN_CONFIG_DIR", cfg)
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")
	return filepath.Join(cfg, "models")
}

func TestHandleDownloadModel_ServerErrorLeavesNoFile(t *testing.T) {
	models := isolateModelsDir(t)
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	bg := background.NewManager(1)
	defer bg.Stop()
	s := &Server{bgManager: bg}

	id := startDownload(t, s, ts.URL+"/m.gguf", "m.gguf")
	task := waitTask(t, bg, id, terminal)
	if task.Status != background.StatusFailed || !strings.Contains(task.Error, "download failed") {
		t.Errorf("task = %+v", task)
	}
	for _, f := range []string{"m.gguf", "m.gguf.part"} {
		if _, err := os.Stat(filepath.Join(models, f)); !os.IsNotExist(err) {
			t.Errorf("%s must not exist after a failed download (err=%v)", f, err)
		}
	}
}

func TestHandleDownloadModel_ReportsProgressAndFailsOnFinalize(t *testing.T) {
	models := isolateModelsDir(t)
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("12345"))
		w.(http.Flusher).Flush()
		<-release // hold the transfer open until the test saw a progress report
		_, _ = w.Write([]byte("67890"))
	}))
	defer ts.Close()
	bg := background.NewManager(1)
	defer bg.Stop()
	s := &Server{bgManager: bg}

	// A directory squatting on the destination makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(models, "m.gguf"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := startDownload(t, s, ts.URL+"/m.gguf", "m.gguf")
	waitTask(t, bg, id, func(task *background.Task) bool { return strings.HasPrefix(task.Message, "Downloading m.gguf:") })
	close(release)

	task := waitTask(t, bg, id, terminal)
	if task.Status != background.StatusFailed || !strings.Contains(task.Error, "finalize download") {
		t.Errorf("task = %+v", task)
	}
	if _, err := os.Stat(filepath.Join(models, "m.gguf.part")); !os.IsNotExist(err) {
		t.Errorf("part file must be cleaned up (err=%v)", err)
	}
}
