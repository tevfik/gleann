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
Extracts content from a file:
- Request: `{"path": "/absolute/path/to/file.pdf"}`
- Response: Structured content with nodes (Document, Section) and edges (HAS_SECTION, HAS_SUBSECTION)

## Creating a Plugin

A plugin can be written in any language. It must:

1. Accept a `--port` flag to set the HTTP listen port
2. Implement the three endpoints above
3. Return structured content following the PluginResult schema

See [gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs) for a reference implementation.
