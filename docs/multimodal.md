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

## Auto-Detection Flow

When no model is explicitly configured:

1. Check `GLEANN_MULTIMODAL_MODEL` env var
2. Query Ollama for installed models
3. Test each model for multimodal capabilities
4. Select the first capable model

If no multimodal model is found, media processing is silently skipped.
