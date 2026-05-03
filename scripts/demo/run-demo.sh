#!/usr/bin/env bash
# Gleann LinkedIn demo runner — paced, narrated walkthrough.
#
# Usage:
#   scripts/demo/run-demo.sh           # interactive (key-press to advance)
#   scripts/demo/run-demo.sh --auto    # auto-paced (for screen recording)
#
# This script is designed to be recorded with asciinema or charmbracelet/vhs.

set -e

# ───────────────── Configuration ─────────────────
DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCS_DIR="${DEMO_DIR}/sample-docs"
INDEX_NAME="ai-fundamentals"
GLEANN_BIN="${GLEANN_BIN:-gleann}"
AUTO_MODE=0
TYPE_DELAY=0.025   # seconds per character when typing
PAUSE_SHORT=1.2
PAUSE_LONG=2.5

[[ "${1:-}" == "--auto" ]] && AUTO_MODE=1

# ───────────────── ANSI colors ─────────────────
BOLD='\033[1m'
DIM='\033[2m'
CYAN='\033[36m'
GREEN='\033[32m'
YELLOW='\033[33m'
MAGENTA='\033[35m'
RESET='\033[0m'

# ───────────────── Helpers ─────────────────
banner() {
  echo
  echo -e "${MAGENTA}${BOLD}── $1 ──${RESET}"
  echo
}

narrate() {
  echo -e "${DIM}# $1${RESET}"
  sleep "${PAUSE_SHORT}"
}

# Type a command character-by-character, then press Enter and run it
type_run() {
  local cmd="$1"
  echo -ne "${GREEN}\$${RESET} "
  for ((i = 0; i < ${#cmd}; i++)); do
    echo -n "${cmd:$i:1}"
    sleep "${TYPE_DELAY}"
  done
  echo
  if [[ "${AUTO_MODE}" -eq 0 ]]; then
    read -r -p ""  # wait for Enter
  else
    sleep 0.4
  fi
  eval "${cmd}" || true
  sleep "${PAUSE_LONG}"
}

pause_step() {
  if [[ "${AUTO_MODE}" -eq 0 ]]; then
    read -r -p "$(echo -e "${DIM}[press Enter to continue]${RESET}")" _
  else
    sleep "${PAUSE_LONG}"
  fi
}

# ───────────────── Demo flow ─────────────────
clear
cat <<'HEADER'
   ____  _
  / ___|| | ___  __ _ _ __  _ __
 | |  _ | |/ _ \/ _` | '_ \| '_ \
 | |_| || |  __/ (_| | | | | | | |
  \____||_|\___|\__,_|_| |_|_| |_|

  Local-first RAG, code intelligence and long-term memory
  for any AI assistant — over MCP.
HEADER
sleep "${PAUSE_LONG}"

banner "1 · Verify the install"
narrate "Gleann is a single Go binary. No Docker, no Python venv."
type_run "${GLEANN_BIN} --version"
type_run "${GLEANN_BIN} doctor"

banner "2 · The corpus we'll index"
narrate "Four short Markdown notes on AI fundamentals."
type_run "ls -la ${DOCS_DIR}"
type_run "head -n 5 ${DOCS_DIR}/transformers.md"
pause_step

banner "3 · Build the index (chunk + embed locally)"
narrate "One command. Embeddings run on Ollama, no data leaves the machine."
type_run "${GLEANN_BIN} index build ${INDEX_NAME} --docs ${DOCS_DIR}"
type_run "${GLEANN_BIN} index list"

banner "4 · Semantic search"
narrate "No keyword match needed — the query is semantic."
type_run "${GLEANN_BIN} search ${INDEX_NAME} \"how does attention work in transformers\""
pause_step

banner "5 · Hybrid search + reranking"
narrate "Dense vectors fused with BM25, then reranked for precision."
type_run "${GLEANN_BIN} search ${INDEX_NAME} \"why use HNSW for nearest neighbors\" --rerank"
pause_step

banner "6 · RAG question answering"
narrate "Retrieve, augment, generate — with cited sources."
type_run "${GLEANN_BIN} ask ${INDEX_NAME} \"Explain RAG and the role of reranking in one paragraph.\""
pause_step

banner "7 · Long-term memory"
narrate "Persistent facts that survive across sessions, auto-injected into prompts."
type_run "${GLEANN_BIN} memory remember \"This demo indexes AI fundamentals docs for LinkedIn.\""
type_run "${GLEANN_BIN} memory list"

banner "8 · Wire it into your AI editor (MCP)"
narrate "One command writes the MCP config for Claude, Cursor, OpenCode, Codex…"
type_run "${GLEANN_BIN} install --list"
narrate "Sample MCP entry that gets generated:"
cat <<'MCP'
   {
     "mcpServers": {
       "gleann": { "command": "gleann", "args": ["mcp"] }
     }
   }
MCP
sleep "${PAUSE_LONG}"

banner "9 · MCP tools the assistant now sees"
narrate "Gleann exposes search, ask, graph traversal, and memory as MCP tools."
type_run "${GLEANN_BIN} mcp --list-tools 2>/dev/null || ${GLEANN_BIN} mcp tools 2>/dev/null || echo '   gleann_search · gleann_ask · gleann_graph_neighbors · gleann_memory'"

banner "Done"
echo -e "${BOLD}${CYAN}  github.com/tevfik/gleann${RESET}"
echo -e "${DIM}  Local-first knowledge for every AI assistant.${RESET}"
echo
sleep "${PAUSE_LONG}"
