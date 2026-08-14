package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cavaliergopher/grab/v3"
	"github.com/tevfik/gleann/internal/background"
)

type DownloadModelRequest struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

// handleDownloadModel initiates a background download of a GGUF model into ~/.gleann/models/
func (s *Server) handleDownloadModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DownloadModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.URL == "" || req.Filename == "" {
		http.Error(w, "URL and filename are required", http.StatusBadRequest)
		return
	}

	if s.bgManager == nil {
		http.Error(w, "Background task manager is not initialized", http.StatusInternalServerError)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Failed to get user home directory", http.StatusInternalServerError)
		return
	}

	modelsDir := filepath.Join(home, ".gleann", "models")
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		http.Error(w, "Failed to create models directory", http.StatusInternalServerError)
		return
	}

	destPath := filepath.Join(modelsDir, req.Filename)

	// Check if already exists
	if stat, err := os.Stat(destPath); err == nil && stat.Size() > 0 {
		// Just a naive check, if it exists we might still want to resume or it's done
	}

	taskID := s.bgManager.Submit(background.TaskTypeCustom, func(progress func(pct float64, msg string)) error {
		progress(0.01, fmt.Sprintf("Starting download for %s", req.Filename))

		client := grab.NewClient()
		reqGrab, err := grab.NewRequest(destPath, req.URL)
		if err != nil {
			return fmt.Errorf("failed to create download request: %w", err)
		}

		// Start download
		resp := client.Do(reqGrab)

		// Monitor progress
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

	Loop:
		for {
			select {
			case <-ticker.C:
				pct := resp.Progress()
				msg := fmt.Sprintf("Downloading %s: %.1f%% (%.2f MB / %.2f MB)",
					req.Filename,
					pct*100,
					float64(resp.BytesComplete())/1024/1024,
					float64(resp.Size())/1024/1024,
				)
				progress(pct, msg)
			case <-resp.Done:
				break Loop
			}
		}

		if err := resp.Err(); err != nil {
			return fmt.Errorf("download failed: %w", err)
		}

		progress(1.0, fmt.Sprintf("Download complete: %s", req.Filename))
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"task_id": taskID,
		"status":  "started",
	})
}

// handleListLocalModels returns a list of local GGUF models in ~/.gleann/models/
func (s *Server) handleListLocalModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Failed to get user home directory", http.StatusInternalServerError)
		return
	}

	modelsDir := filepath.Join(home, ".gleann", "models")

	var models []string
	entries, err := os.ReadDir(modelsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".gguf" {
				models = append(models, entry.Name())
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"models": models,
	})
}
