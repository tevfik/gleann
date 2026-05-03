#!/usr/bin/env bash
# Wrapper that records the gleann demo with whichever recorder is available.
#
#   scripts/demo/record.sh                  # auto-detect (vhs > asciinema)
#   scripts/demo/record.sh vhs              # force VHS  (→ gif + mp4)
#   scripts/demo/record.sh asciinema        # force asciinema (→ .cast)

set -e
DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"
TARGET="${1:-auto}"

have() { command -v "$1" >/dev/null 2>&1; }

record_vhs() {
  echo "▶ Rendering with VHS — output: scripts/demo/gleann-demo.{gif,mp4}"
  cd "${ROOT_DIR}"
  vhs "${DEMO_DIR}/demo.tape"
}

record_asciinema() {
  local cast="${DEMO_DIR}/gleann-demo.cast"
  echo "▶ Recording with asciinema — output: ${cast}"
  asciinema rec --overwrite \
    --title "Gleann — Local-first RAG over MCP" \
    --command "bash ${DEMO_DIR}/run-demo.sh --auto" \
    "${cast}"
  if have agg; then
    echo "▶ Converting cast → gif via agg"
    agg --theme monokai "${cast}" "${DEMO_DIR}/gleann-demo.gif"
  else
    echo "ℹ install 'agg' to convert .cast → gif:  cargo install --git https://github.com/asciinema/agg"
  fi
}

case "${TARGET}" in
  vhs)        have vhs        || { echo "✖ vhs not installed: go install github.com/charmbracelet/vhs@latest"; exit 1; }; record_vhs ;;
  asciinema)  have asciinema  || { echo "✖ asciinema not installed: pipx install asciinema"; exit 1; }; record_asciinema ;;
  auto)
    if   have vhs;       then record_vhs
    elif have asciinema; then record_asciinema
    else
      cat <<EOF
✖ No terminal recorder found. Install one of:

  VHS (recommended — produces GIF + MP4 directly):
    go install github.com/charmbracelet/vhs@latest

  asciinema (records .cast, optionally convert to gif with agg):
    pipx install asciinema
    cargo install --git https://github.com/asciinema/agg
EOF
      exit 1
    fi
    ;;
  *) echo "Unknown target: ${TARGET}"; exit 1 ;;
esac
