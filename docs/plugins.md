# Plugin System

Gleann supports external plugins for parsing complex file formats that Go cannot handle natively (PDFs, Office documents, audio/video, etc.).

## How Plugins Work

Plugins are standalone executables that expose a local HTTP API. Gleann manages their lifecycle:

1. **Discovery**: Plugins are registered in `~/.gleann/plugins.json`
2. **Auto-start**: When Gleann encounters a file extension handled by a plugin, it starts the plugin process
3. **Extraction**: The plugin processes the file and returns structured content (sections, metadata, graph nodes)
4. **Shutdown**: Plugins are stopped when Gleann finishes

## Available Plugins

| Plugin | Formats | Backend | Description |
|--------|---------|---------|-------------|
| [gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs) | PDF, DOCX, XLSX, PPTX, CSV, PNG, JPG | Go + C (`bitllm.c` Docling BitNet 1.58b VLM) | Standalone native document extraction with OCR & OTSL table parsing |
| [gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound) | MP3, WAV, MP4, etc. | whisper.cpp | Audio/video transcription |

`gleann-plugin-marker` (Python, marker-pdf + Surya OCR) is archived and no longer
listed in the catalog; gleann-plugin-docs replaces it. `gleann plugin install marker`
still clones the read-only repository and prints a warning.

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
Returns `200 OK` when the plugin is ready, along with status metadata:
```json
{
  "status": "ok",
  "ready": true,
  "schema_version": 2,
  "version": "1.5.3",
  "timeout": 120
}
```

### `GET /info`
Returns plugin metadata:
```json
{
  "name": "gleann-plugin-docs",
  "version": "1.5.3",
  "schema_version": 2,
  "capabilities": ["document-extraction"],
  "extensions": [".pdf", ".docx", ".xlsx", ".pptx"]
}
```

### `POST /convert`
Extracts structured content from a file uploaded via `multipart/form-data`.

**Request:**
- `Content-Type: multipart/form-data`
- Form field `file`: Binary file upload with filename preserved.

**Response — `PluginResult` schema:**
```json
{
  "nodes": [
    {
      "id": "doc-1",
      "_type": "Document",
      "title": "Quarterly Report Q3 2025",
      "path": "/path/to/report.pdf",
      "page_count": 42,
      "content": "Full extracted text content..."
    },
    {
      "id": "sec-1",
      "_type": "Section",
      "title": "Executive Summary",
      "content": "Section text content...",
      "page": 1,
      "order": 0
    }
  ],
  "edges": [
    {
      "_type": "HAS_SECTION",
      "from": "doc-1",
      "to": "sec-1"
    },
    {
      "_type": "HAS_SUBSECTION",
      "from": "sec-1",
      "to": "sec-2"
    }
  ],
  "markdown": "# Quarterly Report Q3 2025\n\n## Executive Summary\n..."
}
```

**Node types (`_type`):**
| Type | Description |
|------|------------|
| `Document` | Top-level file node. Contains `title`, `path`, `content` |
| `Section` | A heading or logical section. Contains `title`, `content`, `page` |
| `Table` | An extracted table |
| `Image` | An extracted image or figure |

All properties for nodes are stored flat on the node object alongside `_type`.

**Edge schema:**
| Field | Description |
|-------|------------|
| `_type` | Edge relationship type (`HAS_SECTION`, `HAS_SUBSECTION`, `HAS_TABLE`, `HAS_IMAGE`) |
| `from` | Source node ID |
| `to` | Target node ID |

**HTTP Status Codes:**
| Code | Meaning |
|------|---------|
| `200` | Success — returns PluginResult JSON |
| `400` / `422` | Bad request (missing `file` field, unsupported extension, invalid multipart) |
| `500` | Internal extraction error |

**Error response format:**
```json
{
  "error": "conversion failed: reason"
}
```

### `GET /settings` and `PUT /settings` (optional)

A plugin that wants to be configurable from the Web UI (**System → Plugins → Configure**)
describes its own settings form; gleann renders it without knowing anything about the
plugin. Advertise it in `/info` with `"settings": {"path": "/settings", "schema_version": 1}`.

```
GET /settings
→ {"schema_version":1,"title":"…","restart_note":"…","fields":[
    {"key":"language","label":"Language","type":"select","group":"Recognition",
     "help":"…","options":[{"value":"tr","label":"Türkçe"}],"value":"tr","default":""}]}

PUT /settings   Content-Type: application/json
{"values":{"language":"en"}}
→ 200 {"saved":["language"],"restart_required":true}
→ 422 {"error":"invalid settings; nothing was saved","errors":{"hotkey":"use keys joined by +"}}
```

Field `type` is `select` (value must be one of `options`), `text`, `path`
(free text; `options` are suggestions) or `number` (`min`/`max`). The Web UI sends only
the fields the user changed, and a request that fails validation must change nothing.
`PUT` must insist on `Content-Type: application/json` and, off loopback, an access token.

gleann exposes this as `GET|PUT /api/plugins/{name}/settings` and starts the plugin first
if it is registered but not running. A plugin without `/settings` answers `501`, and the
Web UI shows its terminal command (`settings_cmd`) instead. `gleann-plugin-sound` is the
reference implementation (`internal/httpserver/settings.go`).

## Creating a Plugin

A plugin can be written in any language. It must:

1. Accept a `--port` flag to set the HTTP listen port
2. Implement the three endpoints above (`/health`, `/info`, `/convert`)
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

@app.route("/convert", methods=["POST"])
def convert():
    if "file" not in request.files:
        return jsonify({"error": "missing 'file' field"}), 400
    file = request.files["file"]
    filename = file.filename
    # ... extract content from the file ...
    return jsonify({
        "nodes": [{"id": "doc-1", "_type": "Document", "title": filename, "path": filename, "content": "..."}],
        "edges": [],
        "markdown": "# Extracted content..."
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
curl -X POST http://localhost:9200/convert \
  -F "file=@/path/to/test-file.xyz"

# Check gleann's plugin log during index build
GLEANN_LOG_LEVEL=debug gleann index build test --docs ./
```

### Reference Implementations

- [gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs) — PDF/DOCX/Office (Go + C: in-process Docling BitNet VLM + OTSL table parser, fast pdftotext hybrid)
- [gleann-plugin-marker](https://github.com/tevfik/gleann-plugin-marker) — archived; High-accuracy PDF/DOCX/images (Python: marker-pdf + Surya OCR)
- [gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound) — Audio transcription (Go + whisper.cpp)

## Extraction Backend Benchmark

For a comprehensive comparison of all 4 extraction backends (go-native, markitdown-cli, plugin-docs, plugin-marker (archived)) across 6 file formats with 12+ metrics, see the standalone benchmark document:

**[PLUGIN_BENCHMARK.md](PLUGIN_BENCHMARK.md)**

### Quick Summary

| Backend | Layer | Avg Latency | Sections | Formats | Dependencies |
|---------|:-----:|:-----------:|:--------:|:-------:|-------------|
| **go-native** | -1 | **<1ms** | 5.7 | 6 | Zero (always available) |
| **plugin-docs** | 1a | <50ms (text) / ~1s (VLM) | 5.0 | 6+ | Standalone Go + C runtime (zero Python) |
| **markitdown-cli** | 0 | 396ms | 0.0 | 2* | Python markitdown |
| **plugin-marker** | 1b | 1026ms | 9.0 | 4 | Python marker-pdf + surya OCR |

\* markitdown-cli requires optional deps for DOCX/XLSX/PPTX (`pip install markitdown[all]`)

### Running the Benchmark

```bash
# Go test (recommended — tests all 4 backends including go-native)
go test ./tests/benchmarks/ -run TestPluginBenchmark -v -timeout 300s

# Or via the shell script (3 backends, auto-starts plugins)
./tests/e2e/plugin_benchmark.sh
```

## Further Reading

- [Plugin Installation Guide](plugin-install-guide.md) — Step-by-step install instructions
- [Troubleshooting — Plugins](troubleshooting.md#plugin-issues) — Common plugin issues
