// Package multimodal — audio plugin client (Bug #1).
//
// Routes audio files to an external transcription service that exposes
// the standard Gleann plugin contract:
//
//	POST /convert (multipart file=<audio>) → { "markdown": "<transcript>" }
//
// This matches the gleann-plugin-sound API so installing the whisper.cpp
// plugin transparently enables high-quality multilingual transcription
// without code changes.
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

// transcribeViaPlugin uploads an audio file to a plugin's /convert
// endpoint and returns the transcript. Timeout is generous because
// transcription is CPU-bound and may take several minutes for long
// recordings.
func transcribeViaPlugin(pluginURL, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open audio: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("copy audio: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close writer: %w", err)
	}

	url := strings.TrimRight(pluginURL, "/") + "/convert"
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("plugin call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("plugin returned %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		SchemaVersion int    `json:"schema_version"`
		Markdown      string `json:"markdown"`
		Text          string `json:"text"`
		Transcript    string `json:"transcript"` // alternate field name
		Language      string `json:"language"`
		Segments      []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// Some plugins return plain text — accept that gracefully.
		return strings.TrimSpace(string(respBody)), nil
	}
	// Prefer markdown (preserves timestamps) → text → transcript → empty.
	if parsed.Markdown != "" {
		return parsed.Markdown, nil
	}
	if parsed.Text != "" {
		return parsed.Text, nil
	}
	if parsed.Transcript != "" {
		return parsed.Transcript, nil
	}
	return strings.TrimSpace(string(respBody)), nil
}
