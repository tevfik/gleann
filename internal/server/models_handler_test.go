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
