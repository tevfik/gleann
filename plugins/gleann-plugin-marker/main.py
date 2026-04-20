"""gleann-plugin-marker — High-accuracy document extraction using marker-pdf.

Provides PDF/DOCX/PPTX/XLSX → markdown conversion using marker-pdf with
optional LLM-enhanced mode for highest accuracy (tables, forms, math).

Plugin protocol: HTTP server compatible with gleann plugin system.
  GET  /health  → 200 + capabilities JSON
  POST /convert → multipart file upload → PluginResult JSON (nodes + edges)

Usage:
  python main.py --install     # Register with gleann
  python main.py --serve       # Start server (default port 8766)
  python main.py --port 9000   # Custom port
  python main.py --use-llm     # Enable LLM post-processing for max accuracy
"""

import os
import sys
import json
import argparse
import logging
import tempfile

from fastapi import FastAPI, File, UploadFile
from fastapi.responses import JSONResponse
import uvicorn

import section_parser

# Plugin identity
PLUGIN_NAME = "gleann-plugin-marker"
PLUGIN_URL = "http://localhost:8766"
CAPABILITIES = ["document-extraction"]
SUPPORTED_EXTENSIONS = [
    ".pdf", ".docx", ".doc", ".xlsx", ".xls",
    ".pptx", ".ppt", ".epub", ".html", ".htm",
    ".png", ".jpg", ".jpeg", ".tiff", ".bmp",
]

logger = logging.getLogger("gleann-plugin-marker")

app = FastAPI(title="Gleann Marker Plugin")

# Global converter (lazy-initialized)
_converter = None
_use_llm = False
_ollama_model = None


def _get_converter():
    """Lazy-initialize the marker PdfConverter (first call loads models ~3-5s)."""
    global _converter
    if _converter is not None:
        return _converter

    from marker.converters.pdf import PdfConverter
    from marker.models import create_model_dict
    from marker.config.parser import ConfigParser

    config = {"output_format": "markdown"}

    if _use_llm:
        config["use_llm"] = True
        if _ollama_model:
            config["llm_service"] = "marker.services.ollama.OllamaService"
            config["ollama_model"] = _ollama_model
        else:
            # Default: gemini (requires GOOGLE_API_KEY env)
            pass

    config_parser = ConfigParser(config)

    logger.info("Initializing marker PdfConverter (first-time model load)...")
    _converter = PdfConverter(
        config=config_parser.generate_config_dict(),
        artifact_dict=create_model_dict(),
        processor_list=config_parser.get_processors(),
        renderer=config_parser.get_renderer(),
        llm_service=config_parser.get_llm_service() if _use_llm else None,
    )
    logger.info("Marker PdfConverter ready.")
    return _converter


def _is_image_file(ext: str) -> bool:
    return ext in {".png", ".jpg", ".jpeg", ".tiff", ".bmp"}


def _convert_with_marker(file_path: str, filename: str) -> dict:
    """Convert a file using marker-pdf and return markdown + metadata."""
    from marker.output import text_from_rendered

    converter = _get_converter()
    rendered = converter(file_path)
    text, metadata, images = text_from_rendered(rendered)

    page_count = len(metadata.get("page_stats", [])) if metadata else None

    return {
        "markdown": text,
        "page_count": page_count,
        "metadata": metadata,
    }


def _convert_non_pdf(file_path: str, ext: str) -> dict:
    """Convert non-PDF files. marker supports DOCX, PPTX, XLSX, EPUB, HTML, images."""
    # marker-pdf handles various formats when installed with [full]
    return _convert_with_marker(file_path, os.path.basename(file_path))


@app.get("/health")
def health():
    marker_available = False
    try:
        import marker  # noqa: F401
        marker_available = True
    except ImportError:
        pass

    return {
        "status": "ok",
        "plugin": PLUGIN_NAME,
        "capabilities": CAPABILITIES,
        "backends": {
            "marker": marker_available,
            "use_llm": _use_llm,
            "ollama_model": _ollama_model,
        },
    }


@app.post("/convert")
async def convert_document(file: UploadFile = File(...)):
    """Convert an uploaded document to structured graph-ready JSON.

    Returns JSON with:
      - nodes: List of graph nodes (Document + Section)
      - edges: List of graph edges (HAS_SECTION, HAS_SUBSECTION)
      - markdown: Raw markdown content
      - backend: "marker" or "marker+llm"
    """
    ext = os.path.splitext(file.filename)[1].lower()
    if ext not in SUPPORTED_EXTENSIONS:
        return JSONResponse(
            status_code=400,
            content={"error": f"Unsupported extension: {ext}"},
        )

    tmp = None
    try:
        tmp = tempfile.NamedTemporaryFile(delete=False, suffix=ext)
        content = await file.read()
        tmp.write(content)
        tmp.close()

        # Convert using marker
        result = _convert_with_marker(tmp.name, file.filename)
        markdown = result["markdown"]
        page_count = result.get("page_count")
        backend_name = "marker+llm" if _use_llm else "marker"

        if not markdown or not markdown.strip():
            return JSONResponse(
                status_code=422,
                content={"error": "Marker produced empty output for this file"},
            )

        # Parse markdown into graph-ready nodes and edges
        doc_format = ext.lstrip(".")
        parsed = section_parser.parse_document(
            markdown=markdown,
            source_path=file.filename,
            doc_format=doc_format,
            page_count=page_count,
        )
        response = parsed.to_dict()
        response["markdown"] = markdown
        response["backend"] = backend_name

        return response

    except Exception as e:
        logger.exception("Conversion failed")
        return JSONResponse(status_code=500, content={"error": str(e)})
    finally:
        if tmp and os.path.exists(tmp.name):
            os.unlink(tmp.name)


def install_plugin():
    """Register this plugin in ~/.gleann/plugins.json."""
    home = os.path.expanduser("~")
    plugins_file = os.path.join(home, ".gleann", "plugins.json")

    registry = {"plugins": []}
    if os.path.exists(plugins_file):
        try:
            with open(plugins_file, "r") as f:
                content = f.read()
                if content.strip():
                    registry = json.loads(content)
        except Exception as e:
            print(f"Error reading {plugins_file}: {e}")
            registry = {"plugins": []}

    # Remove old entry if exists
    registry["plugins"] = [
        p for p in registry.get("plugins", [])
        if p.get("name") != PLUGIN_NAME and p.get("url") != PLUGIN_URL
    ]

    # Build command
    cmd = [sys.executable, os.path.abspath(__file__), "--serve", "--port", str(args.port)]
    if args.use_llm:
        cmd.append("--use-llm")
    if args.ollama_model:
        cmd.extend(["--ollama-model", args.ollama_model])

    plugin_entry = {
        "name": PLUGIN_NAME,
        "url": PLUGIN_URL,
        "command": cmd,
        "capabilities": CAPABILITIES,
        "extensions": SUPPORTED_EXTENSIONS,
        "timeout": 300,  # marker is slower than docling, needs more time
    }
    registry["plugins"].append(plugin_entry)

    os.makedirs(os.path.dirname(plugins_file), exist_ok=True)
    with open(plugins_file, "w") as f:
        json.dump(registry, f, indent=2)

    print(f"✅ Plugin '{PLUGIN_NAME}' registered to {plugins_file}")
    print(f"   Capabilities: {CAPABILITIES}")
    print(f"   Extensions: {len(SUPPORTED_EXTENSIONS)}")
    print(f"   LLM mode: {'enabled' if args.use_llm else 'disabled'}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Gleann Marker Plugin")
    parser.add_argument("--install", action="store_true", help="Register with Gleann")
    parser.add_argument("--serve", action="store_true", help="Start the FastAPI server")
    parser.add_argument("--port", type=int, default=8766, help="Port (default: 8766)")
    parser.add_argument("--use-llm", action="store_true",
                        help="Enable LLM post-processing for highest accuracy")
    parser.add_argument("--ollama-model", type=str, default=None,
                        help="Ollama model for LLM mode (e.g. gemma2)")
    args = parser.parse_args()

    _use_llm = args.use_llm
    _ollama_model = args.ollama_model

    if args.install:
        install_plugin()

    if args.serve or not sys.argv[1:]:
        print(f"Starting {PLUGIN_NAME} on port {args.port}...")
        if _use_llm:
            print(f"   LLM mode: ON (model: {_ollama_model or 'gemini-2.0-flash'})")
        uvicorn.run(app, host="127.0.0.1", port=args.port)
