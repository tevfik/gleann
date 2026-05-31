// Package multimodal — vision plugin client (Layer B/C, Bug #4/#7/#11).
//
// This file defines the v2 plugin schema used to enrich an IndexableItem
// with structured signals from a dedicated computer-vision plugin
// (gleann-plugin-vision): a CLIP joint-space embedding, OCR text, an
// object/entity list, and an EXIF map. Each field is optional so older
// plugins that only return markdown continue to work unchanged.
//
// The schema is intentionally JSON-stable to allow third-party plugins
// to implement it without depending on the Go module.
package multimodal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VisionEntity is a single detection from an object/entity recogniser.
type VisionEntity struct {
	Type       string    `json:"type"`           // "Object" | "Person" | "Logo" | "Text"
	Label      string    `json:"label"`          // e.g. "laptop", "stop-sign"
	Confidence float32   `json:"confidence"`     // 0..1
	BBox       []float32 `json:"bbox,omitempty"` // [x, y, w, h] normalised
}

// VisionResult is the parsed /convert response from a v2 vision plugin.
// All fields except SchemaVersion are optional.
type VisionResult struct {
	SchemaVersion int               `json:"schema_version"`
	Markdown      string            `json:"markdown,omitempty"`
	OCRText       string            `json:"ocr_text,omitempty"`
	CLIPEmbedding []float32         `json:"clip_embedding,omitempty"`
	Entities      []VisionEntity    `json:"entities,omitempty"`
	Exif          map[string]string `json:"exif,omitempty"`
	Width         int               `json:"width,omitempty"`
	Height        int               `json:"height,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
}

// VisionPluginURL returns the configured base URL of the vision plugin,
// or empty if disabled. Honours GLEANN_VISION_PLUGIN_URL=off as an
// explicit opt-out so users can keep the plugin installed but bypass it.
func VisionPluginURL() string {
	v := os.Getenv("GLEANN_VISION_PLUGIN_URL")
	if v == "off" {
		return ""
	}
	return v
}

// CallVisionPlugin uploads an image to the vision plugin's /convert
// endpoint and returns the parsed VisionResult. Errors are returned so
// callers can decide whether to fall back to VLM-only mode.
func CallVisionPlugin(pluginURL, path string) (*VisionResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("copy: %w", err)
	}
	_ = writer.Close()

	url := strings.TrimRight(pluginURL, "/") + "/convert"
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vision plugin call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vision plugin %d: %s", resp.StatusCode, string(buf))
	}
	var vr VisionResult
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &vr, nil
}

// VisionPluginHealthy reports whether the vision plugin /health endpoint
// returns 200 within a small timeout. Useful for the TUI status panel
// and ProcessDirectory pre-checks.
func VisionPluginHealthy(pluginURL string) bool {
	if pluginURL == "" {
		return false
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(strings.TrimRight(pluginURL, "/") + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
