# gleann-plugin-marker

High-accuracy document extraction plugin for [gleann](https://github.com/tevfik/gleann) using [marker-pdf](https://github.com/VikParuchuri/marker).

## Why marker?

| Metric | marker | docling | mathpix (cloud) |
|--------|--------|---------|-----------------|
| Heuristic accuracy | **95.67%** | 86.71% | 86.43% |
| LLM judge score | **4.24** | 3.70 | 4.16 |
| Forms accuracy | **88.0%** | 68.4% | 64.8% |
| Table TEDS (w/ LLM) | **0.907** | — | — |
| Throughput (H100) | 25 pages/s | ~10 pages/s | cloud |

*Benchmarks from [marker README](https://github.com/VikParuchuri/marker#benchmarks) on Common Crawl dataset.*

## Supported Formats

PDF, DOCX, DOC, XLSX, XLS, PPTX, PPT, EPUB, HTML, PNG, JPG, JPEG, TIFF, BMP

## Installation

```bash
# Install the plugin
cd plugins/gleann-plugin-marker
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt

# Register with gleann
python main.py --install

# For GPU acceleration (recommended):
pip install torch torchvision --index-url https://download.pytorch.org/whl/cu121
```

## Usage

```bash
# Start the server (default port 8766)
python main.py --serve

# With LLM enhancement for highest accuracy
python main.py --serve --use-llm

# With local Ollama model
python main.py --serve --use-llm --ollama-model gemma2

# Custom port
python main.py --serve --port 9000
```

## LLM Mode

When `--use-llm` is enabled, marker uses an LLM to:
- Merge tables across pages
- Handle inline math properly
- Format complex tables
- Extract values from forms

Supported backends:
- **Gemini** (default): Set `GOOGLE_API_KEY` environment variable
- **Ollama** (local): `--ollama-model gemma2`
- **Claude**: Set `ANTHROPIC_API_KEY`
- **OpenAI**: Set `OPENAI_API_KEY`

## Protocol

Compatible with gleann's plugin HTTP protocol:
- `GET /health` → Plugin status and capabilities
- `POST /convert` → Multipart file upload → JSON with nodes, edges, markdown

## Architecture

```
gleann → POST /convert (multipart file)
           ↓
     marker PdfConverter
     (surya OCR + layout detection + texify)
           ↓
     Optional LLM post-processing
           ↓
     section_parser.parse_document()
           ↓
     PluginResult JSON (nodes + edges + markdown)
```
