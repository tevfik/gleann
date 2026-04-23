# Multimodal Processing

Gleann can detect and use multimodal Ollama models to process images, audio, and video files. When a multimodal model is available, gleann can describe, analyze, and extract information from media files.

## Supported Media Types

| Type | Extensions |
|------|-----------|
| Image | `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`, `.bmp`, `.svg`, `.tiff` |
| Audio | `.mp3`, `.wav`, `.ogg`, `.flac`, `.m4a`, `.aac`, `.wma` |
| Video | `.mp4`, `.avi`, `.mov`, `.mkv`, `.webm`, `.flv`, `.wmv` |

## Supported Models

Gleann auto-detects multimodal capabilities from these model families:

| Model Family | Vision | Audio | Recognized Patterns |
|-------------|:------:|:-----:|---------------------|
| Gemma 4 | ✅ | ✅ | `gemma4*` |
| Qwen3 VL | ✅ | ❌ | `qwen3-vl*`, `qwen2-vl*` |
| LLaVA | ✅ | ❌ | `llava*` |
| BakLLaVA | ✅ | ❌ | `bakllava*` |
| MiniCPM-V | ✅ | ❌ | `minicpm-v*` |
| Moondream | ✅ | ❌ | `moondream*` |

## How It Works

1. **Model Detection**: Gleann queries Ollama's `/api/show` endpoint to check model capabilities, supplemented by heuristic name matching for known model families
2. **Auto-Selection**: `AutoDetectModel()` scans installed Ollama models and picks the first multimodal-capable one
3. **Processing**: Files are base64-encoded and sent to the model via Ollama's `/api/chat` endpoint with a media-type-specific prompt

## Configuration

| Setting | Config Key | Env Var | Default |
|---------|-----------|---------|---------|
| Multimodal model | `multimodal_model` | `GLEANN_MULTIMODAL_MODEL` | auto-detected |

```json
// ~/.gleann/config.json
{
  "multimodal_model": "gemma4"
}
```

Or via environment variable:

```bash
export GLEANN_MULTIMODAL_MODEL=gemma4
```

## API Usage

The multimodal package is currently available as an internal Go API:

```go
import "github.com/tevfik/gleann/internal/multimodal"

// Detect media type
mediaType := multimodal.DetectMediaType("photo.jpg") // => Image

// Check if a model supports multimodal
caps, _ := multimodal.DetectCapabilities("http://localhost:11434", "gemma4")
// caps.Vision == true, caps.Audio == true

// Process a file
proc := multimodal.NewProcessor("http://localhost:11434")
description, err := proc.ProcessFile("screenshot.png")
// description: "The image shows a terminal window with..."
```

## Video Frame Extraction

gleann can extract keyframes from video files and analyze each frame with a multimodal model. Requires `ffmpeg` and `ffprobe`.

```go
import "github.com/tevfik/gleann/internal/multimodal"

cfg := multimodal.DefaultFrameConfig()
cfg.MaxFrames = 8    // Sample up to 8 frames
cfg.Quality = 85     // JPEG quality

proc := multimodal.NewProcessor("http://localhost:11434")
analysis, err := proc.AnalyzeVideo("demo.mp4", cfg)
// analysis.Duration   → video length in seconds
// analysis.Frames     → extracted frame paths + timestamps
// analysis.Descriptions → per-frame VLM descriptions
// analysis.Summary    → combined narrative summary
```

The pipeline:
1. Probes video duration via `ffprobe`
2. Calculates FPS to evenly sample `MaxFrames` frames (capped at 1 fps)
3. Extracts JPEG frames via `ffmpeg`
4. Sends each frame to the multimodal model for description
5. Combines per-frame descriptions into a timestamped summary

## PDF Vision Pipeline

Hybrid PDF processing: marker plugin for text → page rendering → VLM for tables/charts.

```go
cfg := multimodal.DefaultPDFConfig()
cfg.DPI = 150       // Render resolution
cfg.MaxPages = 0    // 0 = all pages
cfg.UseMarker = true // Try gleann-plugin-marker first

proc := multimodal.NewProcessor("http://localhost:11434")
analysis, err := proc.AnalyzePDF("report.pdf", cfg)
// analysis.TotalPages → number of pages
// analysis.Pages[i].Description → VLM description
// analysis.Pages[i].HasTable   → table detected
// analysis.Pages[i].HasChart   → chart/figure detected
// analysis.Pages[i].MarkerText → text from marker plugin
```

**Rendering backends** (at least one required):
- `pdftoppm` (poppler-utils) — preferred
- `mutool` (mupdf-tools) — fallback

The pipeline:
1. If `UseMarker` is true, attempt text extraction via gleann-plugin-marker
2. Render each page to JPEG at configured DPI
3. Send page images to VLM with a structured prompt
4. Detect tables and charts from VLM response
5. Combine marker text + VLM analysis per page

## Audio Input

Audio files can be processed in two ways:

1. **Plugin-based transcription**: `gleann-plugin-sound` uses whisper.cpp for speech-to-text
2. **Model-native**: Multimodal models with audio support (e.g., Gemma 4) process audio directly

In the TUI, use `/audio` to attach audio files (50 MB limit).

## Image Indexing

`ProcessDirectory` scans a directory for multimodal files and generates text descriptions for vector indexing:

```go
proc := multimodal.NewProcessor("http://localhost:11434")
items, err := proc.ProcessDirectory("./assets", nil, func(cur, total int, path string) {
    fmt.Printf("[%d/%d] %s\n", cur, total, path)
})
// items[i].Source      → original file path
// items[i].Text        → "[Image: photo.jpg]\n\nA screenshot showing..."
// items[i].MediaType   → Image, Audio, or Video
```

This enables `gleann build` with `--multimodal-model` to include media descriptions in the vector index alongside text documents.

## Auto-Detection Flow

When no model is explicitly configured:

1. Check `GLEANN_MULTIMODAL_MODEL` env var
2. Query Ollama for installed models
3. Test each model for multimodal capabilities
4. Select the first capable model

If no multimodal model is found, media processing is silently skipped.
