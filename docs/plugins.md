# Plugin System

Gleann supports external plugins for parsing complex file formats that Go cannot handle natively (PDFs, Office documents, audio/video, etc.).

## How Plugins Work

Plugins are standalone executables that expose a local HTTP API. Gleann manages their lifecycle:

1. **Discovery**: Plugins are registered in `~/.gleann/plugins.json`
2. **Auto-start**: When Gleann encounters a file extension handled by a plugin, it starts the plugin process
3. **Extraction**: The plugin processes the file and returns structured content (sections, metadata, graph nodes)
4. **Shutdown**: Plugins are stopped when Gleann finishes

## Available Plugins

| Plugin | Formats | Description |
|--------|---------|-------------|
| [gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs) | PDF, DOCX, XLSX, PPTX | Document extraction via MarkItDown |
| [gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound) | MP3, WAV, MP4, etc. | Audio/video transcription via whisper.cpp |

## Installation

### Via Setup Wizard

```bash
gleann setup
# Select "Manage Plugins" during setup
```

### Via TUI

```bash
gleann tui
# Navigate to Plugins tab
```

The TUI plugin manager downloads the correct binary for your platform and registers it automatically.

## Plugin API

Plugins must implement this HTTP API:

### `GET /health`
Returns `200 OK` when the plugin is ready.

### `GET /info`
Returns plugin metadata:
```json
{
  "name": "gleann-plugin-docs",
  "version": "1.0.0",
  "extensions": [".pdf", ".docx", ".xlsx", ".pptx"]
}
```

### `POST /extract`
Extracts content from a file.

**Request:**
```json
{
  "path": "/absolute/path/to/file.pdf"
}
```

**Response — `PluginResult` schema:**
```json
{
  "nodes": [
    {
      "id": "doc-1",
      "label": "Document",
      "properties": {
        "title": "Quarterly Report Q3 2025",
        "source": "/path/to/report.pdf",
        "page_count": 42,
        "content": "Full extracted text content..."
      }
    },
    {
      "id": "sec-1",
      "label": "Section",
      "properties": {
        "title": "Executive Summary",
        "content": "Section text content...",
        "page": 1,
        "order": 0
      }
    }
  ],
  "edges": [
    {
      "source": "doc-1",
      "target": "sec-1",
      "label": "HAS_SECTION"
    },
    {
      "source": "sec-1",
      "target": "sec-2",
      "label": "HAS_SUBSECTION"
    }
  ],
  "chunks": [
    {
      "text": "Chunk of text suitable for embedding...",
      "metadata": {
        "source": "/path/to/report.pdf",
        "section": "Executive Summary",
        "page": 1
      }
    }
  ]
}
```

**Node labels:**
| Label | Description |
|-------|------------|
| `Document` | Top-level file node. Must have `title`, `source`, `content` properties |
| `Section` | A heading or logical section. Must have `title`, `content` |
| `Table` | An extracted table. `content` holds rendered text |
| `Image` | An extracted image. `content` holds OCR text or caption |

**Edge labels:**
| Label | Description |
|-------|------------|
| `HAS_SECTION` | Document → Section |
| `HAS_SUBSECTION` | Section → Section (nested hierarchy) |
| `HAS_TABLE` | Section → Table |
| `HAS_IMAGE` | Section → Image |

**HTTP Status Codes:**
| Code | Meaning |
|------|---------|
| `200` | Success — returns PluginResult JSON |
| `400` | Bad request (missing `path` field, invalid JSON) |
| `404` | File not found at the given `path` |
| `415` | Unsupported file type |
| `500` | Internal extraction error |

**Error response format:**
```json
{
  "error": "file not found: /path/to/missing.pdf"
}
```

## Creating a Plugin

A plugin can be written in any language. It must:

1. Accept a `--port` flag to set the HTTP listen port
2. Implement the three endpoints above (`/health`, `/info`, `/extract`)
3. Return structured content following the `PluginResult` schema
4. Exit cleanly when the parent process sends SIGTERM

### Minimal Example (Python)

```python
from flask import Flask, request, jsonify
import sys

app = Flask(__name__)

@app.route("/health")
def health():
    return "OK"

@app.route("/info")
def info():
    return jsonify({
        "name": "my-plugin",
        "version": "0.1.0",
        "extensions": [".xyz"]
    })

@app.route("/extract", methods=["POST"])
def extract():
    path = request.json.get("path")
    if not path:
        return jsonify({"error": "missing 'path' field"}), 400
    # ... extract content from the file ...
    return jsonify({
        "nodes": [{"id": "doc-1", "label": "Document", "properties": {"title": path, "source": path, "content": "..."}}],
        "edges": [],
        "chunks": [{"text": "...", "metadata": {"source": path}}]
    })

if __name__ == "__main__":
    port = int(sys.argv[sys.argv.index("--port") + 1]) if "--port" in sys.argv else 9200
    app.run(port=port)
```

### Debugging Your Plugin

```bash
# Start the plugin manually
./my-plugin --port 9200

# In another terminal, check health
curl http://localhost:9200/health

# Test extraction
curl -X POST http://localhost:9200/extract \
  -H "Content-Type: application/json" \
  -d '{"path": "/path/to/test-file.xyz"}'

# Check gleann's plugin log during index build
GLEANN_LOG_LEVEL=debug gleann index build test --docs ./
```

### Reference Implementations

- [gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs) — PDF/DOCX (Go + Python MarkItDown)
- [gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound) — Audio transcription (Go + whisper.cpp)

## Further Reading

- [Plugin Installation Guide](plugin-install-guide.md) — Step-by-step install instructions
- [Troubleshooting — Plugins](troubleshooting.md#plugins) — Common plugin issues
