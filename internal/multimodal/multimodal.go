// Package multimodal provides model-native multimodal processing for gleann.
// Instead of using external plugins for audio/image, it leverages Ollama's
// multimodal models (Gemma4, Qwen3-VL) to describe media content as text,
// which can then be indexed and searched like any other document.
package multimodal

import (
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

// ── Supported media types ─────────────────────────────────────

// MediaType classifies a file into a processing category.
type MediaType int

const (
	MediaTypeUnknown MediaType = iota
	MediaTypeImage
	MediaTypeAudio
	MediaTypeVideo
	MediaTypeDocument // handled by plugins, not multimodal
)

// imageExts maps file extensions to the image media type.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".svg": true,
}

// audioExts maps file extensions to the audio media type.
var audioExts = map[string]bool{
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true,
	".m4a": true, ".aac": true, ".wma": true, ".opus": true,
}

// videoExts maps file extensions to the video media type.
var videoExts = map[string]bool{
	".mp4": true, ".avi": true, ".mkv": true, ".mov": true,
	".webm": true, ".flv": true, ".wmv": true,
}

// DetectMediaType returns the media category for a file path.
func DetectMediaType(path string) MediaType {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case imageExts[ext]:
		return MediaTypeImage
	case audioExts[ext]:
		return MediaTypeAudio
	case videoExts[ext]:
		return MediaTypeVideo
	default:
		return MediaTypeUnknown
	}
}

// IsMultimodal returns true if the file is an audio, image, or video file
// that can be processed by a multimodal model.
func IsMultimodal(path string) bool {
	mt := DetectMediaType(path)
	return mt == MediaTypeImage || mt == MediaTypeAudio || mt == MediaTypeVideo
}

// ── Ollama model capability detection ─────────────────────────

// ModelCapabilities describes what a model can handle.
type ModelCapabilities struct {
	Name       string
	Vision     bool
	Audio      bool
	Multimodal bool
}

// ollamaShowResponse is the subset of /api/show we need.
type ollamaShowResponse struct {
	ModelFile string `json:"modelfile"`
	Template  string `json:"template"`
	Details   struct {
		Families      []string `json:"families"`
		ParameterSize string   `json:"parameter_size"`
	} `json:"details"`
}

// DetectCapabilities queries Ollama for a model's multimodal support.
func DetectCapabilities(ollamaHost, modelName string) ModelCapabilities {
	caps := ModelCapabilities{Name: modelName}
	if ollamaHost == "" || modelName == "" {
		applyModelHeuristics(&caps)
		return caps
	}

	client := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"name": modelName})
	resp, err := client.Post(ollamaHost+"/api/show", "application/json", bytes.NewReader(body))
	if err != nil {
		applyModelHeuristics(&caps)
		return caps
	}
	defer resp.Body.Close()

	var show ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		applyModelHeuristics(&caps)
		return caps
	}

	// Check families for vision/audio capabilities.
	for _, f := range show.Details.Families {
		fl := strings.ToLower(f)
		if strings.Contains(fl, "vision") || strings.Contains(fl, "clip") || strings.Contains(fl, "image") {
			caps.Vision = true
		}
		if strings.Contains(fl, "audio") || strings.Contains(fl, "whisper") || strings.Contains(fl, "sound") {
			caps.Audio = true
		}
	}

	applyModelHeuristics(&caps)
	return caps
}

// applyModelHeuristics sets capabilities based on known model names.
func applyModelHeuristics(caps *ModelCapabilities) {
	nameLower := strings.ToLower(caps.Name)
	multimodalModels := []string{"gemma4", "qwen3-vl", "qwen2.5-vl", "llava", "bakllava", "minicpm-v", "moondream"}
	for _, mm := range multimodalModels {
		if strings.Contains(nameLower, mm) {
			caps.Vision = true
			caps.Multimodal = true
		}
	}
	// Gemma4 specifically supports audio.
	if strings.Contains(nameLower, "gemma4") {
		caps.Audio = true
	}

	if !caps.Multimodal {
		caps.Multimodal = caps.Vision || caps.Audio
	}
}

// ── Processing ────────────────────────────────────────────────

// Processor handles multimodal file processing via Ollama.
type Processor struct {
	OllamaHost string
	Model      string
	Lang       string // "en" (default) or "tr" — Bug #10
}

// NewProcessor creates a multimodal processor.
// If model is empty, it tries to auto-detect from GLEANN_MULTIMODAL_MODEL env.
// Language defaults to GLEANN_MULTIMODAL_LANG ("en" if unset).
func NewProcessor(ollamaHost, model string) *Processor {
	if model == "" {
		model = os.Getenv("GLEANN_MULTIMODAL_MODEL")
	}
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}
	lang := strings.ToLower(os.Getenv("GLEANN_MULTIMODAL_LANG"))
	if lang != "tr" {
		lang = "en"
	}
	return &Processor{OllamaHost: ollamaHost, Model: model, Lang: lang}
}

// ProcessResult holds the output of multimodal processing.
type ProcessResult struct {
	FilePath    string
	MediaType   MediaType
	Description string // Text description of the media content.
	Error       error
}

// ProcessFile sends a file to the multimodal model and returns a text description.
func (p *Processor) ProcessFile(path string) ProcessResult {
	result := ProcessResult{
		FilePath:  path,
		MediaType: DetectMediaType(path),
	}

	if p.Model == "" {
		result.Error = fmt.Errorf("no multimodal model configured; set GLEANN_MULTIMODAL_MODEL or pass --multimodal-model")
		return result
	}

	// Bug #3: content-hash cache. We compute the fingerprint once (per file
	// + model + prompt-version + lang) and short-circuit on hit. Videos and
	// GIFs are intentionally fingerprinted on the source path so that the
	// derived frames re-use the same cache slot across runs.
	fp, _ := fileFingerprint(path, p.Model, p.Lang)
	if cd, ok := loadCached(fp); ok {
		result.Description = cd.Description
		return result
	}

	// Bug #1: audio files are routed to a dedicated transcription plugin
	// (e.g. gleann-plugin-sound running whisper.cpp) when GLEANN_AUDIO_PLUGIN_URL
	// is configured. The fallback (Ollama VLM) only works for the small set
	// of audio-capable vision models (Gemma 4) and produces unusable
	// transcripts for the rest.
	if result.MediaType == MediaTypeAudio {
		if url := os.Getenv("GLEANN_AUDIO_PLUGIN_URL"); url != "" {
			if text, err := transcribeViaPlugin(url, path); err == nil {
				result.Description = text
				storeCached(fp, cachedDescription{Description: text, MediaType: int(result.MediaType), Model: p.Model, Lang: p.Lang})
				return result
			} else {
				result.Error = fmt.Errorf("audio plugin transcription failed: %w", err)
				return result
			}
		}
		// No plugin: emit a clear error rather than blindly POSTing a base64
		// audio payload to a non-audio-capable vision model (which would
		// silently return rubbish).
		caps := ModelCapabilities{Name: p.Model}
		applyModelHeuristics(&caps)
		if !caps.Audio {
			result.Error = fmt.Errorf("audio file %q requires either GLEANN_AUDIO_PLUGIN_URL (whisper plugin) or an audio-capable model (e.g. gemma4)", filepath.Base(path))
			return result
		}
	}

	// Video files: use ffmpeg frame extraction pipeline if available.
	if result.MediaType == MediaTypeVideo {
		analysis, err := p.AnalyzeVideo(path, DefaultFrameConfig())
		if err == nil {
			result.Description = analysis.Summary
			CleanupFrames(analysis.Frames)
			storeCached(fp, cachedDescription{Description: result.Description, MediaType: int(result.MediaType), Model: p.Model, Lang: p.Lang})
			return result
		}
		// ffmpeg not available — return a clean error instead of sending raw
		// video bytes to a vision-only model (which would return Ollama 500
		// "unknown format" and waste a 120s timeout).
		result.Error = fmt.Errorf("video indexing requires ffmpeg (%w); install ffmpeg or pass --skip-multimodal to ignore videos", err)
		return result
	}

	// Animated GIFs are technically MediaTypeImage but Ollama's vision
	// models accept only static frames. Try to extract the first frame via
	// ffmpeg; if ffmpeg is missing, return a graceful error.
	if result.MediaType == MediaTypeImage && strings.EqualFold(filepath.Ext(path), ".gif") {
		if frame, err := extractFirstFrame(path); err == nil {
			defer os.Remove(frame)
			path = frame // process the single-frame PNG below
		} else {
			result.Error = fmt.Errorf("animated GIF indexing requires ffmpeg (%w); install ffmpeg or convert to PNG/JPG", err)
			return result
		}
	}

	// Read file and base64 encode it.
	data, err := os.ReadFile(path)
	if err != nil {
		result.Error = fmt.Errorf("read file: %w", err)
		return result
	}

	// Size limit: 50MB.
	if len(data) > 50<<20 {
		result.Error = fmt.Errorf("file too large (%d bytes, max 50MB)", len(data))
		return result
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	prompt := p.descriptionPrompt(result.MediaType, filepath.Base(path))

	// Call Ollama /api/chat with image/audio data.
	reqBody := map[string]interface{}{
		"model":  p.Model,
		"stream": false,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": prompt,
				"images":  []string{encoded},
			},
		},
	}

	jsonBody, _ := json.Marshal(reqBody)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(p.OllamaHost+"/api/chat", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		result.Error = fmt.Errorf("ollama request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
		return result
	}

	var chatResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		result.Error = fmt.Errorf("decode response: %w", err)
		return result
	}

	result.Description = chatResp.Message.Content
	storeCached(fp, cachedDescription{Description: result.Description, MediaType: int(result.MediaType), Model: p.Model, Lang: p.Lang})
	return result
}

// CanProcess returns true if the processor is configured and the file is multimodal.
func (p *Processor) CanProcess(path string) bool {
	return p.Model != "" && IsMultimodal(path)
}

// descriptionPrompt is a back-compat wrapper used by older tests and any
// external callers. Defaults to English; new code should prefer the
// method on *Processor which honours the configured locale.
func descriptionPrompt(mt MediaType, filename string) string {
	return (&Processor{Lang: "en"}).descriptionPrompt(mt, filename)
}

// descriptionPrompt returns an appropriate prompt for the media type.
// Bug #10: respect Processor.Lang to issue Turkish prompts when requested.
func (p *Processor) descriptionPrompt(mt MediaType, filename string) string {
	if p.Lang == "tr" {
		switch mt {
		case MediaTypeImage:
			return fmt.Sprintf("Bu görseli ayrıntılı olarak Türkçe açıkla. "+
				"Gördüğün metinleri, diyagramları, grafikleri ve görsel "+
				"öğeleri belirt. Dosya adı: %s", filename)
		case MediaTypeAudio:
			return fmt.Sprintf("Bu ses içeriğini Türkçe transkript ederek açıkla. "+
				"Konuşma, müzik veya çevre seslerini ayırt et. Dosya adı: %s", filename)
		case MediaTypeVideo:
			return fmt.Sprintf("Bu video içeriğini Türkçe açıkla. Görsel öğeleri, "+
				"varsa metin/konuşmayı ve konuyu özetle. Dosya adı: %s", filename)
		default:
			return fmt.Sprintf("Bu dosyanın içeriğini Türkçe açıkla. Dosya adı: %s", filename)
		}
	}
	switch mt {
	case MediaTypeImage:
		return fmt.Sprintf("Describe this image in detail. Include any text, diagrams, charts, or visual elements you can identify. Filename: %s", filename)
	case MediaTypeAudio:
		return fmt.Sprintf("Transcribe and describe this audio content. Include any speech, music, or sounds. Filename: %s", filename)
	case MediaTypeVideo:
		return fmt.Sprintf("Describe this video content. Include visual elements, any text or speech, and the overall topic. Filename: %s", filename)
	default:
		return fmt.Sprintf("Describe the content of this file. Filename: %s", filename)
	}
}

// AutoDetectModel queries Ollama for available multimodal models and returns the best one.
func AutoDetectModel(ollamaHost string) string {
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ollamaHost + "/api/tags")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return ""
	}

	// Preference order for multimodal models.
	preferred := []string{"gemma4", "qwen3-vl", "qwen2.5-vl", "llava", "minicpm-v", "moondream"}
	for _, prefix := range preferred {
		for _, m := range tags.Models {
			if strings.HasPrefix(strings.ToLower(m.Name), prefix) {
				return m.Name
			}
		}
	}
	return ""
}
