# Gleann LinkedIn Demo — VHS tape (with model setup shown)
#
# Render:  vhs scripts/demo/demo.tape
# Outputs: scripts/demo/gleann-demo.gif  +  scripts/demo/gleann-demo.mp4

Output scripts/demo/gleann-demo.gif
Output scripts/demo/gleann-demo.mp4

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1320
Set Height 780
Set Padding 24
Set TypingSpeed 30ms
Set PlaybackSpeed 1.0
Set Shell bash

# ── Title ──────────────────────────────────────────────────
Type "clear && echo 'GLEANN — Local-first RAG · Code intel · MCP for every AI assistant'"
Sleep 400ms
Enter
Sleep 2s

# ── 1. Version & doctor ────────────────────────────────────
Type "gleann version"
Sleep 400ms
Enter
Sleep 1500ms

Type "gleann doctor"
Sleep 400ms
Enter
Sleep 4s

# ── 2. Model setup ─────────────────────────────────────────
Type "# Models powering this demo (local Ollama, no cloud):"
Sleep 600ms
Enter
Sleep 600ms

Type "gleann config show | jq '{embedding_model, llm_model, rerank_model, backend}'"
Sleep 400ms
Enter
Sleep 4s

Type "ollama list | head -1 && ollama list | grep -E 'gemma3:4b|nomic-embed|Reranker'"
Sleep 400ms
Enter
Sleep 3s

# ── 3. Corpus ──────────────────────────────────────────────
Type "# Corpus: 4 short Markdown notes on AI fundamentals"
Sleep 600ms
Enter
Type "ls scripts/demo/sample-docs/"
Sleep 400ms
Enter
Sleep 2500ms

Type "head -n 6 scripts/demo/sample-docs/transformers.md"
Sleep 400ms
Enter
Sleep 3s

# ── 4. Build index ─────────────────────────────────────────
Type "# Chunk + embed locally — nothing leaves the machine"
Sleep 600ms
Enter
Type "gleann index build ai-fundamentals --docs scripts/demo/sample-docs/"
Sleep 400ms
Enter
Sleep 10s

Type "gleann index list | grep ai-fundamentals"
Sleep 400ms
Enter
Sleep 2500ms

# ── 5. Semantic search ─────────────────────────────────────
Type "# Pure semantic search — no keyword overlap needed"
Sleep 600ms
Enter
Type `gleann search ai-fundamentals "how does attention work in transformers"`
Sleep 400ms
Enter
Sleep 5s

# ── 6. Hybrid + rerank ────────────────────────────────────
Type "# Dense + BM25, then cross-encoder rerank"
Sleep 600ms
Enter
Type `gleann search ai-fundamentals "why HNSW for nearest neighbors" --rerank`
Sleep 400ms
Enter
Sleep 6s

# ── 7. RAG ─────────────────────────────────────────────────
Type "# RAG: retrieve, augment, generate — grounded answer"
Sleep 600ms
Enter
Type `gleann ask ai-fundamentals "Explain RAG and the role of reranking in two sentences."`
Sleep 400ms
Enter
Sleep 20s

# ── 8. Long-term memory ────────────────────────────────────
Type "# Persistent memory — auto-injected into every prompt"
Sleep 600ms
Enter
Type `gleann memory remember "Demo indexes AI fundamentals docs for LinkedIn."`
Sleep 400ms
Enter
Sleep 2s

Type "gleann memory list"
Sleep 400ms
Enter
Sleep 3s

# ── 9. MCP install ─────────────────────────────────────────
Type "# One command wires Gleann into any AI editor over MCP"
Sleep 600ms
Enter
Type "gleann install --list"
Sleep 400ms
Enter
Sleep 4s

Type "cat scripts/demo/mcp-snippet.json"
Sleep 400ms
Enter
Sleep 3s

# ── Outro ──────────────────────────────────────────────────
Type "echo '  github.com/tevfik/gleann  ·  Local-first AI knowledge.'"
Sleep 400ms
Enter
Sleep 3s
