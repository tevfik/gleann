# Gleann Official Demo — VHS Tape
#
# Render:  vhs scripts/demo/demo.tape
# Outputs: scripts/demo/gleann-demo.gif  +  scripts/demo/gleann-demo.mp4

Output scripts/demo/gleann-demo.gif
Output scripts/demo/gleann-demo.mp4

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 20
Set TypingSpeed 25ms
Set PlaybackSpeed 1.0
Set Shell bash

# ── Title ──────────────────────────────────────────────────
Type "clear && echo -e '\033[1;36m⚡ GLEANN — Local AI Intelligence · Codebase Graph · Universal MCP\033[0m'"
Sleep 300ms
Enter
Sleep 1500ms

# ── 1. System Doctor ───────────────────────────────────────
Type "gleann doctor"
Sleep 300ms
Enter
Sleep 2500ms

# ── 2. 1-Click AI Agent Integration ────────────────────────
Type "# Auto-wire Gleann into 17 AI editors and agents over MCP"
Sleep 400ms
Enter
Type "gleann install --list | head -n 14"
Sleep 300ms
Enter
Sleep 3s

# ── 3. Index Code & Technical Docs ─────────────────────────
Type "# Index technical documents & code with AST Call-Graph + Tables"
Sleep 400ms
Enter
Type "gleann index build ai-core --docs scripts/demo/sample-docs/ --instant"
Sleep 300ms
Enter
Sleep 2500ms

# ── 4. Semantic Search ─────────────────────────────────────
Type "# Semantic Search: dense vectors + lexical BM25 retrieval"
Sleep 400ms
Enter
Type "gleann search ai-core 'how does self-attention work in transformers'"
Sleep 300ms
Enter
Sleep 3500ms

# ── 5. Cross-Session Long-Term Memory ──────────────────────
Type "# Persistent Long-Term Memory across terminal restarts"
Sleep 400ms
Enter
Type "gleann memory remember 'Architecture: Core uses in-process EIF BitNet and KùzuDB' --tag architecture"
Sleep 300ms
Enter
Sleep 1500ms

Type "gleann memory search 'BitNet'"
Sleep 300ms
Enter
Sleep 3s

# ── Outro ──────────────────────────────────────────────────
Type "echo -e '\033[1;32m✅ Single binary (~34MB). Zero Python. Local-first RAG. github.com/tevfik/gleann\033[0m'"
Sleep 300ms
Enter
Sleep 3s
