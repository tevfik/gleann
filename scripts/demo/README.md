# Gleann — LinkedIn terminal demo

A self-contained, recordable walkthrough of Gleann's main features:
install check → index AI docs → semantic + hybrid search → RAG Q&A →
long-term memory → MCP install for AI editors.

```
scripts/demo/
├── sample-docs/        # 4 short markdown notes on AI fundamentals
│   ├── transformers.md
│   ├── rag.md
│   ├── vector-search.md
│   └── mcp.md
├── run-demo.sh         # paced terminal narrative (interactive or --auto)
├── demo.tape           # VHS script — renders GIF + MP4 directly
└── record.sh           # auto-detects vhs / asciinema and records
```

## Quick start

```bash
# 1. Have gleann + Ollama running, then:
scripts/demo/run-demo.sh           # step through manually (Enter to advance)
scripts/demo/run-demo.sh --auto    # auto-paced for screen capture

# 2. Produce a shareable artifact
scripts/demo/record.sh             # picks vhs > asciinema automatically
scripts/demo/record.sh vhs         # force VHS  → gleann-demo.gif + .mp4
scripts/demo/record.sh asciinema   # force asciinema → gleann-demo.cast
```

## Recommended recorder: VHS (Charm)

VHS is a declarative terminal recorder — fast, reproducible, headless,
outputs MP4 ready for LinkedIn.

```bash
go install github.com/charmbracelet/vhs@latest
ttyd --version    # required runtime; install via: brew install ttyd  /  apt install ttyd
vhs scripts/demo/demo.tape         # → scripts/demo/gleann-demo.{gif,mp4}
```

LinkedIn accepts MP4 up to 10 minutes / 5 GB. The default tape produces a
~60–90 second 1280×760 clip — under 10 MB.

## Alternative: asciinema + agg

```bash
pipx install asciinema
cargo install --git https://github.com/asciinema/agg
scripts/demo/record.sh asciinema
```

Upload the `.cast` to <https://asciinema.org/> and embed the link in your
LinkedIn post, or convert to GIF with `agg`.

## Demo storyline (≈ 90 s)

| # | Step | What it shows |
|---|------|----------------|
| 1 | `gleann --version` · `doctor` | Single binary, system check |
| 2 | `ls sample-docs/`             | The corpus we will index |
| 3 | `index build`                 | Local chunking + embedding via Ollama |
| 4 | `search` semantic query       | Vector retrieval, no keyword match needed |
| 5 | `search --rerank`             | Hybrid retrieval + cross-encoder rerank |
| 6 | `ask`                         | RAG-grounded answer with citations |
| 7 | `memory remember` · `list`    | Persistent long-term memory |
| 8 | `install --list`              | One-command MCP wiring for any editor |
| 9 | MCP JSON snippet              | Drop-in config for Claude / Cursor / Codex / OpenCode |

## Customising

- Swap the corpus by replacing files in `sample-docs/` — any `.md`/`.txt`/`.pdf`.
- Override the binary used: `GLEANN_BIN=./gleann scripts/demo/run-demo.sh`.
- Tweak pacing: edit `TYPE_DELAY`, `PAUSE_SHORT`, `PAUSE_LONG` in `run-demo.sh`,
  or `Set TypingSpeed` / `Sleep` lines in `demo.tape`.

## Troubleshooting

- **"index already exists"** → `gleann index remove ai-fundamentals` and re-run.
- **Ollama not reachable** → `ollama serve &` and `ollama pull bge-m3`.
- **VHS errors about `ttyd`** → install `ttyd` (Homebrew / apt). VHS depends on it.
