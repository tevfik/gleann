#!/bin/sh
# gleann installer — Linux, macOS, Windows (Git Bash / WSL)
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/tevfik/gleann/main/scripts/install.sh | sh
#   wget -qO- https://raw.githubusercontent.com/tevfik/gleann/main/scripts/install.sh | sh
#
# Options (via environment variables):
#   GLEANN_VERSION=v1.2.3   Install a specific version (default: latest)
#   GLEANN_INSTALL_DIR=...  Install directory (default: ~/.local/bin)
#   GLEANN_FULL=1           Install full build (with tree-sitter)
#
set -e

REPO="tevfik/gleann"
GITHUB_API="https://api.github.com"
GITHUB_RELEASES="https://github.com/${REPO}/releases"

# ── Helpers ──────────────────────────────────────────────────────────────────

log()   { printf "  \033[1;32m✓\033[0m %s\n" "$1"; }
warn()  { printf "  \033[1;33m⚠\033[0m %s\n" "$1"; }
fail()  { printf "  \033[1;31m✗\033[0m %s\n" "$1"; exit 1; }
info()  { printf "  \033[1;34mℹ\033[0m %s\n" "$1"; }

# ── Detect platform ─────────────────────────────────────────────────────────

detect_os() {
    case "$(uname -s)" in
        Linux*)   echo "linux"   ;;
        Darwin*)  echo "darwin"  ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *)        fail "Unsupported OS: $(uname -s). Supported: Linux, macOS, Windows (via WSL/Git Bash)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "amd64"  ;;
        aarch64|arm64)  echo "arm64"  ;;
        armv7*)         echo "armv7"  ;;
        *)              fail "Unsupported architecture: $(uname -m). Supported: amd64, arm64" ;;
    esac
}

# ── Resolve version ─────────────────────────────────────────────────────────

resolve_version() {
    if [ -n "${GLEANN_VERSION}" ]; then
        echo "${GLEANN_VERSION}"
        return
    fi

    # Try GitHub API for latest release tag.
    if command -v curl > /dev/null 2>&1; then
        tag=$(curl -sSf "${GITHUB_API}/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    elif command -v wget > /dev/null 2>&1; then
        tag=$(wget -qO- "${GITHUB_API}/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    fi

    if [ -z "${tag}" ]; then
        fail "Cannot determine latest version. Set GLEANN_VERSION=vX.Y.Z and retry."
    fi

    echo "${tag}"
}

# ── Download helper ──────────────────────────────────────────────────────────

download() {
    url="$1"
    dest="$2"
    if command -v curl > /dev/null 2>&1; then
        curl -sSfL -o "${dest}" "${url}" || fail "Download failed: ${url}"
    elif command -v wget > /dev/null 2>&1; then
        wget -qO "${dest}" "${url}" || fail "Download failed: ${url}"
    else
        fail "Neither curl nor wget found. Install one and retry."
    fi
}

# ── Main ─────────────────────────────────────────────────────────────────────

main() {
    printf "\n\033[1mgleann installer\033[0m\n\n"

    OS=$(detect_os)
    ARCH=$(detect_arch)
    VERSION=$(resolve_version)

    log "Platform: ${OS}/${ARCH}"
    log "Version:  ${VERSION}"

    # Build variant (lite or full).
    VARIANT="gleann"
    if [ "${GLEANN_FULL}" = "1" ]; then
        VARIANT="gleann-full"
    fi

    # Construct download URL.
    EXT=""
    if [ "${OS}" = "windows" ]; then
        EXT=".exe"
    fi
    ASSET="${VARIANT}-${OS}-${ARCH}${EXT}"
    URL="${GITHUB_RELEASES}/download/${VERSION}/${ASSET}"

    # Install directory.
    INSTALL_DIR="${GLEANN_INSTALL_DIR:-${HOME}/.local/bin}"
    mkdir -p "${INSTALL_DIR}"

    DEST="${INSTALL_DIR}/gleann${EXT}"
    TMPFILE=$(mktemp)
    trap 'rm -f "${TMPFILE}"' EXIT

    info "Downloading ${URL}..."
    download "${URL}" "${TMPFILE}"

    mv "${TMPFILE}" "${DEST}"
    chmod +x "${DEST}"
    log "Installed to ${DEST}"

    # Check PATH.
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            warn "${INSTALL_DIR} is not in your PATH"
            printf "\n  Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):\n"
            printf "    export PATH=\"%s:\$PATH\"\n\n" "${INSTALL_DIR}"
            ;;
    esac

    # Verify.
    if command -v gleann > /dev/null 2>&1; then
        log "Verified: $(gleann version 2>/dev/null || echo 'gleann installed')"
    else
        info "Restart your shell or run: export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi

    printf "\n\033[1mNext steps:\033[0m\n"
    printf "  gleann setup --bootstrap    # Quick auto-configuration\n"
    printf "  gleann doctor               # Check system health\n"
    printf "  gleann setup                # Interactive wizard\n"
    printf "\n  Docs: https://github.com/${REPO}#readme\n\n"
}

main
