# Plugin Installation Guide

Step-by-step instructions for installing gleann plugins.

## Available Plugins

| Plugin | Formats | What It Does |
|--------|---------|-------------|
| **[gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs)** | PDF, DOCX, XLSX, PPTX | Extracts text and structure from office documents |
| **[gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound)** | MP3, WAV, MP4, OGG | Transcribes audio/video files using whisper.cpp |

## Method 1: Via TUI (Recommended)

The easiest way — the TUI auto-downloads the correct binary for your platform:

```bash
gleann tui
# Navigate to: Plugins → Install
# Select the plugin → Done
```

## Method 2: Via Setup Wizard

During initial setup, plugins are offered as an option:

```bash
gleann setup
# When prompted: "Manage Plugins?" → Yes
# Select plugins to install
```

## Method 3: Manual Installation

### gleann-plugin-docs

**Prerequisites:** Python 3.9+ (for MarkItDown PDF/DOCX extraction)

```bash
# Download the latest release
# Linux (amd64)
curl -sSL https://github.com/tevfik/gleann-plugin-docs/releases/latest/download/gleann-plugin-docs-linux-amd64 \
  -o ~/.gleann/plugins/gleann-plugin-docs
chmod +x ~/.gleann/plugins/gleann-plugin-docs

# macOS (arm64 / Apple Silicon)
curl -sSL https://github.com/tevfik/gleann-plugin-docs/releases/latest/download/gleann-plugin-docs-darwin-arm64 \
  -o ~/.gleann/plugins/gleann-plugin-docs
chmod +x ~/.gleann/plugins/gleann-plugin-docs

# Windows (amd64)
# Download gleann-plugin-docs-windows-amd64.exe from GitHub Releases
# Place in %USERPROFILE%\.gleann\plugins\
```

**Register the plugin:**
```bash
# Add to plugins.json (create if missing)
cat > ~/.gleann/plugins.json << 'EOF'
[
  {
    "name": "gleann-plugin-docs",
    "path": "~/.gleann/plugins/gleann-plugin-docs",
    "extensions": [".pdf", ".docx", ".xlsx", ".pptx"]
  }
]
EOF
```

**Verify installation:**
```bash
# Start the plugin manually to test
~/.gleann/plugins/gleann-plugin-docs --port 9100 &

# Check health
curl http://localhost:9100/health
# Expected: 200 OK

# Check info
curl http://localhost:9100/info
# Expected: {"name":"gleann-plugin-docs","version":"...","extensions":[".pdf",".docx",...]}

# Stop the test
kill %1
```

### gleann-plugin-sound

**Prerequisites:** None (whisper.cpp is bundled)

```bash
# Download the latest release
# Linux (amd64)
curl -sSL https://github.com/tevfik/gleann-plugin-sound/releases/latest/download/gleann-plugin-sound-linux-amd64 \
  -o ~/.gleann/plugins/gleann-plugin-sound
chmod +x ~/.gleann/plugins/gleann-plugin-sound

# macOS (arm64 / Apple Silicon)
curl -sSL https://github.com/tevfik/gleann-plugin-sound/releases/latest/download/gleann-plugin-sound-darwin-arm64 \
  -o ~/.gleann/plugins/gleann-plugin-sound
chmod +x ~/.gleann/plugins/gleann-plugin-sound
```

**Register the plugin:**
```bash
# Append to plugins.json
cat > ~/.gleann/plugins.json << 'EOF'
[
  {
    "name": "gleann-plugin-docs",
    "path": "~/.gleann/plugins/gleann-plugin-docs",
    "extensions": [".pdf", ".docx", ".xlsx", ".pptx"]
  },
  {
    "name": "gleann-plugin-sound",
    "path": "~/.gleann/plugins/gleann-plugin-sound",
    "extensions": [".mp3", ".wav", ".mp4", ".ogg", ".flac", ".m4a"]
  }
]
EOF
```

## Using Plugins

Once installed, plugins are automatically started when gleann encounters matching file extensions during index builds:

```bash
# Index a folder containing PDFs
gleann index build reports --docs ./reports/
# Output: 📦 Plugin "gleann-plugin-docs" handling 12 PDF files...

# Index audio recordings
gleann index build meetings --docs ./recordings/
# Output: 📦 Plugin "gleann-plugin-sound" transcribing 5 audio files...
```

## Plugin Lifecycle

1. **Discovery**: Gleann reads `~/.gleann/plugins.json` on startup
2. **Auto-start**: When a matching file extension is found, gleann starts the plugin on a random port
3. **Extraction**: Files are sent to the plugin's `/extract` endpoint
4. **Shutdown**: Plugins are stopped when gleann finishes the operation

You don't need to manually start or stop plugins — gleann handles everything automatically.

## Troubleshooting

### "Plugin not found"

```bash
# Check plugin registration
cat ~/.gleann/plugins.json

# Verify the binary exists and is executable
ls -la ~/.gleann/plugins/gleann-plugin-docs
```

### "Plugin fails to extract"

```bash
# Test the plugin directly
~/.gleann/plugins/gleann-plugin-docs --port 9100 &
curl -X POST http://localhost:9100/extract \
  -H "Content-Type: application/json" \
  -d '{"path": "/absolute/path/to/test.pdf"}'
kill %1
```

### "Unsupported file format warning"

If you see warnings about unsupported files during index build, install the appropriate plugin:

```
⚠ Skipped 3 files (unsupported: .pdf, .docx)
  Install gleann-plugin-docs for PDF/DOCX support:
    gleann tui → Plugins → Install
```

## Creating Custom Plugins

Any HTTP server can be a gleann plugin. See the [Plugin API documentation](plugins.md) for the specification.
