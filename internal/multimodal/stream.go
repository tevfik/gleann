// Package multimodal — streaming VLM (Bug #8).
//
// Adds Processor.ProcessFileStream which mirrors ProcessFile but invokes
// a per-token callback as Ollama produces output. The result still passes
// through the content-hash cache so subsequent calls return instantly via
// a single synthetic on-token chunk.
package multimodal

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TokenCallback receives streamed tokens from the VLM. Errors returned by
// the callback abort the stream.
type TokenCallback func(token string) error

// ProcessFileStream is the streaming sibling of ProcessFile. It returns
// the same ProcessResult, but additionally invokes onToken for every
// chunk so that TUIs and APIs can render incremental output.
//
// On cache hit the cached description is forwarded as a single token so
// callers can treat the streaming path uniformly.
func (p *Processor) ProcessFileStream(path string, onToken TokenCallback) ProcessResult {
	result := ProcessResult{
		FilePath:  path,
		MediaType: DetectMediaType(path),
	}
	if p.Model == "" {
		result.Error = fmt.Errorf("no multimodal model configured")
		return result
	}

	fp, _ := fileFingerprint(path, p.Model, p.Lang)
	if cd, ok := loadCached(fp); ok {
		result.Description = cd.Description
		if onToken != nil {
			_ = onToken(cd.Description)
		}
		return result
	}

	// Video files are not streamable per-token (frame-by-frame analysis
	// would need its own progress callback); delegate to the standard
	// path and emit a single token at the end so callers get a result.
	if result.MediaType == MediaTypeVideo {
		r := p.ProcessFile(path)
		if onToken != nil && r.Error == nil {
			_ = onToken(r.Description)
		}
		return r
	}

	// Animated GIF — extract first frame, then stream as image.
	if result.MediaType == MediaTypeImage && strings.EqualFold(filepath.Ext(path), ".gif") {
		frame, err := extractFirstFrame(path)
		if err != nil {
			result.Error = fmt.Errorf("animated GIF needs ffmpeg: %w", err)
			return result
		}
		defer os.Remove(frame)
		path = frame
	}

	data, err := os.ReadFile(path)
	if err != nil {
		result.Error = fmt.Errorf("read file: %w", err)
		return result
	}
	if len(data) > 50<<20 {
		result.Error = fmt.Errorf("file too large (%d bytes, max 50MB)", len(data))
		return result
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	prompt := p.descriptionPrompt(result.MediaType, filepath.Base(path))

	reqBody := map[string]interface{}{
		"model":  p.Model,
		"stream": true,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt, "images": []string{encoded}},
		},
	}
	jsonBody, _ := json.Marshal(reqBody)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Post(p.OllamaHost+"/api/chat", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		result.Error = fmt.Errorf("ollama stream request: %w", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
		return result
	}

	// Each line in the stream is a JSON object with .message.content.
	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue // skip malformed line
		}
		if chunk.Message.Content != "" {
			sb.WriteString(chunk.Message.Content)
			if onToken != nil {
				if cbErr := onToken(chunk.Message.Content); cbErr != nil {
					result.Error = cbErr
					return result
				}
			}
		}
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		result.Error = fmt.Errorf("stream scan: %w", err)
		return result
	}
	result.Description = sb.String()
	storeCached(fp, cachedDescription{Description: result.Description, MediaType: int(result.MediaType), Model: p.Model, Lang: p.Lang})
	return result
}
