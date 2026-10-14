package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cavaliergopher/grab/v3"
	"github.com/tevfik/gleann/internal/background"
)

type DownloadModelRequest struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

// allowedModelExts are the file extensions a downloaded model may have.
// The web UI downloads GGUF files from HuggingFace; the others cover EIF
// model bundles and raw weight files.
var allowedModelExts = map[string]bool{
	".gguf":        true,
	".eifm":        true,
	".bin":         true,
	".safetensors": true,
}

// validateModelFilename ensures the requested filename is a plain file name
// (no directories, no traversal, not hidden) with a known model extension.
func validateModelFilename(f string) error {
	if f == "" || f == "." || f == ".." {
		return fmt.Errorf("invalid filename %q", f)
	}
	if filepath.Base(f) != f || strings.ContainsAny(f, "/\\\x00") {
		return fmt.Errorf("filename %q must not contain path separators", f)
	}
	if strings.HasPrefix(f, ".") || strings.HasPrefix(f, "-") {
		return fmt.Errorf("filename %q must not start with '.' or '-'", f)
	}
	ext := strings.ToLower(filepath.Ext(f))
	if !allowedModelExts[ext] {
		return fmt.Errorf("filename %q has unsupported extension (allowed: .gguf, .eifm, .bin, .safetensors)", f)
	}
	return nil
}

// modelDownloadAllowPrivate reports whether model downloads may target
// private, loopback or link-local addresses
// (GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE=1). Read at request/dial time.
func modelDownloadAllowPrivate() bool {
	return os.Getenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE") == "1"
}

// validateModelURL enforces the download URL policy:
//   - scheme http or https; https is required unless the host is loopback
//   - private / loopback / link-local destinations are refused unless
//     GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE=1 (checked again at dial time by
//     the guarded transport, which also covers redirects)
func validateModelURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid url: %v", err)
	}
	if err := checkModelURLScheme(u); err != nil {
		return nil, err
	}
	if modelDownloadAllowPrivate() {
		return u, nil
	}
	host := u.Hostname()
	if isLoopbackHost(host) {
		return nil, fmt.Errorf("download from loopback host %q not allowed (set GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE=1 to override)", host)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve host %q: %v", host, err)
	}
	for _, ip := range ips {
		if isRestrictedIP(ip) {
			return nil, fmt.Errorf("host %q resolves to restricted address %s (set GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE=1 to override)", host, ip)
		}
	}
	return u, nil
}

func checkModelURLScheme(u *url.URL) error {
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("url must include a host")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("plain http is only allowed for loopback hosts; use https")
	default:
		return fmt.Errorf("url scheme %q not allowed (use https)", u.Scheme)
	}
}

// modelDownloadClient downloads model files. There is no overall Timeout
// (multi-GB downloads take long; cancellation comes from the server
// lifetime context) but connection setup and response headers are bounded,
// every dial is checked against restricted address ranges, and redirects
// must keep satisfying the scheme policy.
var modelDownloadClient = &http.Client{
	Transport: newGuardedTransport(modelDownloadAllowPrivate, 60*time.Second),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return checkModelURLScheme(req.URL)
	},
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
	if err := validateModelFilename(req.Filename); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := validateModelURL(req.URL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	// Download to a temp name and rename on success so a failed or
	// interrupted transfer never leaves a truncated model under the real
	// name (where it would be picked up as a usable model).
	partPath := destPath + ".part"
	ctx := s.lifetime()

	taskID := s.bgManager.Submit(background.TaskTypeCustom, func(progress func(pct float64, msg string)) error {
		progress(0.01, fmt.Sprintf("Starting download for %s", req.Filename))

		client := grab.NewClient()
		client.HTTPClient = modelDownloadClient
		client.UserAgent = "gleann"
		reqGrab, err := grab.NewRequest(partPath, req.URL)
		if err != nil {
			return fmt.Errorf("failed to create download request: %w", err)
		}
		reqGrab = reqGrab.WithContext(ctx)

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
			os.Remove(partPath)
			return fmt.Errorf("download failed: %w", err)
		}
		if err := os.Rename(partPath, destPath); err != nil {
			os.Remove(partPath)
			return fmt.Errorf("finalize download: %w", err)
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
