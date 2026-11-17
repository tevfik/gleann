package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
)

func TestHandleDownloadModel(t *testing.T) {
	// 1. Setup a fake HTTP server that serves a dummy "GGUF" file
	dummyContent := []byte("fake-gguf-content-12345")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(dummyContent)))
		w.Write(dummyContent)
	}))
	defer ts.Close()

	// 2. Setup server with Background Manager
	bgManager := background.NewManager(2)
	defer bgManager.Stop()
	srv := &Server{
		bgManager: bgManager,
	}

	// Override HOME to isolate the download directory
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	// Pin the config dir to the default so an outer GLEANN_CONFIG_DIR
	// cannot redirect the download away from tmpHome.
	t.Setenv("GLEANN_CONFIG_DIR", "")
	// The fake model server listens on loopback over plain http.
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")

	// 3. Make POST request to /api/models/download
	reqBody, _ := json.Marshal(DownloadModelRequest{
		URL:      ts.URL + "/test-model.gguf",
		Filename: "test-model.gguf",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/models/download", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleDownloadModel(w, req)

	// 4. Assert response
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "started" {
		t.Errorf("expected status 'started', got %s", resp["status"])
	}
	taskID := resp["task_id"]
	if taskID == "" {
		t.Fatal("expected a task_id in response")
	}

	// 5. Wait for the background task to complete
	timeout := time.After(3 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	taskComplete := false
WaitLoop:
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for download task to complete")
		case <-ticker.C:
			tasks := bgManager.List("")
			for _, task := range tasks {
				if task.ID == taskID && (task.Status == background.StatusCompleted || task.Status == background.StatusFailed) {
					if task.Status == background.StatusFailed {
						t.Fatalf("task failed: %s", task.Error)
					}
					taskComplete = true
					break WaitLoop
				}
			}
		}
	}

	if !taskComplete {
		t.Fatal("task did not complete successfully")
	}

	// 6. Verify the file exists in ~/.gleann/models/
	destPath := filepath.Join(tmpHome, ".gleann", "models", "test-model.gguf")
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("downloaded file not found: %v", err)
	}

	if string(content) != string(dummyContent) {
		t.Errorf("file content mismatch. got %s, want %s", string(content), string(dummyContent))
	}
}

// The list endpoint must read the same models directory as the CLI and the
// downloader (modelcatalog.Dir): GLEANN_CONFIG_DIR moves it, and without the
// variable it stays at ~/.gleann/models.
func TestHandleListLocalModelsHonoursConfigDir(t *testing.T) {
	cfgDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// A model in the config dir, plus entries that must not be listed.
	cfgModels := filepath.Join(cfgDir, "models")
	if err := os.MkdirAll(cfgModels, 0755); err != nil {
		t.Fatalf("create models dir: %v", err)
	}
	os.WriteFile(filepath.Join(cfgModels, "config-model.gguf"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(cfgModels, "notes.txt"), []byte("x"), 0644)
	os.MkdirAll(filepath.Join(cfgModels, "subdir.gguf"), 0755)

	// A model in the default home location, used when GLEANN_CONFIG_DIR is unset.
	homeModels := filepath.Join(home, ".gleann", "models")
	if err := os.MkdirAll(homeModels, 0755); err != nil {
		t.Fatalf("create home models dir: %v", err)
	}
	os.WriteFile(filepath.Join(homeModels, "home-model.gguf"), []byte("x"), 0644)

	s := &Server{}
	listModels := func() []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/models/local", nil)
		w := httptest.NewRecorder()
		s.handleListLocalModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp struct {
			Models []string `json:"models"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp.Models
	}

	// With GLEANN_CONFIG_DIR set only the config dir's GGUF files are listed.
	t.Setenv("GLEANN_CONFIG_DIR", cfgDir)
	got := listModels()
	if len(got) != 1 || got[0] != "config-model.gguf" {
		t.Errorf("GLEANN_CONFIG_DIR set: expected [config-model.gguf], got %v", got)
	}

	// Without it the default ~/.gleann/models location is used again.
	t.Setenv("GLEANN_CONFIG_DIR", "")
	got = listModels()
	if len(got) != 1 || got[0] != "home-model.gguf" {
		t.Errorf("GLEANN_CONFIG_DIR unset: expected [home-model.gguf], got %v", got)
	}
}

// Downloads must land in the same GLEANN_CONFIG_DIR-aware directory the
// list endpoint reads, not in a hard-coded ~/.gleann/models.
func TestHandleDownloadModelHonoursConfigDir(t *testing.T) {
	dummyContent := []byte("fake-gguf-content-config-dir")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(dummyContent)))
		w.Write(dummyContent)
	}))
	defer ts.Close()

	bgManager := background.NewManager(2)
	defer bgManager.Stop()
	srv := &Server{bgManager: bgManager}

	cfgDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", cfgDir)
	// The fake model server listens on loopback over plain http.
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "1")

	reqBody, _ := json.Marshal(DownloadModelRequest{
		URL:      ts.URL + "/test-model.gguf",
		Filename: "test-model.gguf",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/models/download", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleDownloadModel(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	taskID := resp["task_id"]
	if taskID == "" {
		t.Fatal("expected a task_id in response")
	}

	timeout := time.After(3 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
WaitLoop:
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for download task to complete")
		case <-ticker.C:
			for _, task := range bgManager.List("") {
				if task.ID == taskID && (task.Status == background.StatusCompleted || task.Status == background.StatusFailed) {
					if task.Status == background.StatusFailed {
						t.Fatalf("task failed: %s", task.Error)
					}
					break WaitLoop
				}
			}
		}
	}

	destPath := filepath.Join(cfgDir, "models", "test-model.gguf")
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("downloaded file not found in GLEANN_CONFIG_DIR models dir: %v", err)
	}
	if string(content) != string(dummyContent) {
		t.Errorf("file content mismatch. got %s, want %s", string(content), string(dummyContent))
	}
	if _, err := os.Stat(filepath.Join(home, ".gleann", "models", "test-model.gguf")); !os.IsNotExist(err) {
		t.Error("model was also written to the hard-coded ~/.gleann/models location")
	}
}
