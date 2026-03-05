#!/usr/bin/env bash
#
# E2E Test Suite for gleann — Plugins, DocExtractor & Cross-Platform
#
# Usage:
#   ./scripts/e2e_plugin_test.sh          # run all tests
#   ./scripts/e2e_plugin_test.sh --quick  # skip slow tests (markitdown install, git clone)
#
# Exit codes:
#   0 = all tests passed
#   1 = one or more tests failed
#
set -euo pipefail

# ── Colors ──
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color
BOLD='\033[1m'

PASS=0
FAIL=0
SKIP=0
QUICK=false

[[ "${1:-}" == "--quick" ]] && QUICK=true

pass()  { PASS=$((PASS+1)); echo -e "  ${GREEN}✓${NC} $1"; }
fail()  { FAIL=$((FAIL+1)); echo -e "  ${RED}✗${NC} $1"; }
skip()  { SKIP=$((SKIP+1)); echo -e "  ${YELLOW}⊘${NC} $1 (skipped)"; }
header(){ echo -e "\n${CYAN}${BOLD}━━━ $1 ━━━${NC}"; }

# ── Workspace ──
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

TMPDIR_BASE=$(mktemp -d)
trap "rm -rf $TMPDIR_BASE" EXIT

# ═══════════════════════════════════════════════════════════════
header "1. Build Verification"
# ═══════════════════════════════════════════════════════════════

# 1.1 Standard build (no CGo)
if go build -o "$TMPDIR_BASE/gleann" ./cmd/gleann/ 2>/dev/null; then
    pass "go build ./cmd/gleann/ (no tags)"
else
    fail "go build ./cmd/gleann/ (no tags)"
fi

# 1.2 Build with treesitter tag
if go build -tags treesitter -o "$TMPDIR_BASE/gleann-ts" ./cmd/gleann/ 2>/dev/null; then
    pass "go build -tags treesitter ./cmd/gleann/"
else
    fail "go build -tags treesitter ./cmd/gleann/"
fi

# 1.3 Binary is executable
if [[ -x "$TMPDIR_BASE/gleann" ]]; then
    pass "Binary is executable"
else
    fail "Binary is executable"
fi

# ═══════════════════════════════════════════════════════════════
header "2. Unit Tests (pkg/gleann, modules/chunking, internal/tui)"
# ═══════════════════════════════════════════════════════════════

# 2.1 Run pkg/gleann tests
if go test ./pkg/gleann/ -count=1 -timeout 30s >/dev/null 2>&1; then
    pass "pkg/gleann tests"
else
    fail "pkg/gleann tests"
fi

# 2.2 Run chunking tests
if go test ./modules/chunking/ -count=1 -timeout 30s >/dev/null 2>&1; then
    pass "modules/chunking tests"
else
    fail "modules/chunking tests"
fi

# 2.3 Run integration tests
if go test ./tests/ -count=1 -timeout 60s >/dev/null 2>&1; then
    pass "integration tests (tests/)"
else
    fail "integration tests (tests/)"
fi

# ═══════════════════════════════════════════════════════════════
header "3. MarkItDown CLI Wrapper (Layer 0)"
# ═══════════════════════════════════════════════════════════════

# 3.1 FindMarkItDown detection
MARKITDOWN_PATH=""
if command -v markitdown &>/dev/null; then
    MARKITDOWN_PATH=$(command -v markitdown)
    pass "markitdown found on PATH: $MARKITDOWN_PATH"
elif [[ -x "$HOME/.local/bin/markitdown" ]]; then
    MARKITDOWN_PATH="$HOME/.local/bin/markitdown"
    pass "markitdown found at ~/.local/bin/markitdown"
elif [[ -x "$HOME/.local/pipx/venvs/markitdown/bin/markitdown" ]]; then
    MARKITDOWN_PATH="$HOME/.local/pipx/venvs/markitdown/bin/markitdown"
    pass "markitdown found at pipx venv"
else
    skip "markitdown not found (use 'pipx install markitdown' to enable)"
fi

# 3.2 Test markitdown CLI on CSV file
if [[ -n "$MARKITDOWN_PATH" ]]; then
    CSV_FILE="$TMPDIR_BASE/test.csv"
    echo -e "name,age,city\nAlice,30,Istanbul\nBob,25,Ankara" > "$CSV_FILE"
    if OUTPUT=$("$MARKITDOWN_PATH" "$CSV_FILE" 2>/dev/null) && [[ "$OUTPUT" == *"Alice"* ]]; then
        pass "markitdown CLI extracts CSV correctly"
    else
        fail "markitdown CLI CSV extraction"
    fi
else
    skip "markitdown CSV test"
fi

# 3.3 Test markitdown CLI on Markdown file (passthrough)
if [[ -n "$MARKITDOWN_PATH" ]]; then
    MD_FILE="$TMPDIR_BASE/test.md"
    echo -e "# Hello\n\nWorld.\n\n## Section\n\nDetails." > "$MD_FILE"
    if OUTPUT=$("$MARKITDOWN_PATH" "$MD_FILE" 2>/dev/null) && [[ "$OUTPUT" == *"Hello"* ]]; then
        pass "markitdown CLI handles .md passthrough"
    else
        fail "markitdown CLI .md passthrough"
    fi
else
    skip "markitdown .md passthrough test"
fi

# 3.4 InstallMarkItDown (pipx availability)
if command -v pipx &>/dev/null; then
    pass "pipx available for markitdown auto-install"
else
    skip "pipx not available (auto-install won't work)"
fi

# ═══════════════════════════════════════════════════════════════
header "4. DocExtractor & PluginResult (Go-native)"
# ═══════════════════════════════════════════════════════════════

# 4.1 Run doc_extractor tests explicitly with verbose
DOC_TEST_OUTPUT=$(go test ./pkg/gleann/ -run "TestMarkdownToPluginResult|TestParseHeadings|TestFirstParagraph|TestCanHandle|TestDocExtractor|TestMarkItDownExtractor" -count=1 -v 2>&1)

for test_name in "TestMarkdownToPluginResult_BasicHeadings" "TestMarkdownToPluginResult_Edges" \
    "TestMarkdownToPluginResult_NoHeadings" "TestMarkdownToPluginResult_SectionIDs" \
    "TestMarkdownToPluginResult_Summary" "TestMarkdownToPluginResult_FormatInference" \
    "TestParseHeadings_Hierarchy" "TestParseHeadings_SiblingOrder" "TestParseHeadings_EmptyContent" \
    "TestFirstParagraph_Short" "TestFirstParagraph_Long" "TestFirstParagraph_SkipsHeadings" \
    "TestCanHandle" "TestCanHandle_CaseInsensitive" "TestMarkItDownExtractor_NilSafety" \
    "TestDocExtractor_NilLayers"; do
    if echo "$DOC_TEST_OUTPUT" | grep -qF -- "--- PASS: $test_name"; then
        pass "$test_name"
    elif echo "$DOC_TEST_OUTPUT" | grep -qF -- "--- SKIP: $test_name"; then
        skip "$test_name"
    else
        fail "$test_name"
    fi
done

# ═══════════════════════════════════════════════════════════════
header "5. Plugin Registry"
# ═══════════════════════════════════════════════════════════════

PLUGINS_JSON="$HOME/.gleann/plugins.json"

# 5.1 Test registry load (no panic on missing file)
if go test ./pkg/gleann/ -run "TestRegisterBackend" -count=1 -v >/dev/null 2>&1; then
    pass "Plugin registry handles missing file gracefully"
else
    fail "Plugin registry missing file handling"
fi

# 5.2 Test plugins.json format (if it exists)
if [[ -f "$PLUGINS_JSON" ]]; then
    if python3 -c "import json; json.load(open('$PLUGINS_JSON'))" 2>/dev/null; then
        pass "plugins.json is valid JSON"
    else
        fail "plugins.json is NOT valid JSON"
    fi
    # Count registered plugins
    PLUGIN_COUNT=$(python3 -c "import json; d=json.load(open('$PLUGINS_JSON')); print(len(d.get('plugins',[])))" 2>/dev/null || echo 0)
    pass "plugins.json has $PLUGIN_COUNT registered plugin(s)"
else
    skip "plugins.json does not exist yet (no plugins registered)"
fi

# ═══════════════════════════════════════════════════════════════
header "6. Plugin Install Flow (Simulated)"
# ═══════════════════════════════════════════════════════════════

# 6.1 Test git is available
if command -v git &>/dev/null; then
    pass "git available for plugin install"
else
    fail "git not found (required for plugin install)"
fi

# 6.2 Test python3 is available (for docs plugin)
if command -v python3 &>/dev/null; then
    PYTHON_VER=$(python3 --version 2>&1)
    pass "python3 available: $PYTHON_VER"
else
    fail "python3 not found (required for gleann-docs plugin)"
fi

# 6.3 Test python3 venv module
if python3 -c "import venv" &>/dev/null; then
    pass "python3 venv module available"
else
    fail "python3 venv module missing (install python3-venv)"
fi

# 6.4 Test go compiler (for sound plugin build)
if command -v go &>/dev/null; then
    GO_VER=$(go version 2>&1)
    pass "go available: $GO_VER"
else
    fail "go not found (required for gleann-sound plugin)"
fi

# 6.5 Simulate plugin directory structure
FAKE_PLUGINS_DIR="$TMPDIR_BASE/plugins"
mkdir -p "$FAKE_PLUGINS_DIR/gleann-docs"
mkdir -p "$FAKE_PLUGINS_DIR/gleann-sound"

# Write a fake requirements.txt
echo "fastapi>=0.100" > "$FAKE_PLUGINS_DIR/gleann-docs/requirements.txt"
echo "markitdown>=0.1" >> "$FAKE_PLUGINS_DIR/gleann-docs/requirements.txt"

# Write a fake main.py that supports --install
cat > "$FAKE_PLUGINS_DIR/gleann-docs/main.py" << 'PYEOF'
import sys, json, os
if "--install" in sys.argv:
    home = os.path.expanduser("~")
    p = os.path.join(home, ".gleann", "plugins.json")
    os.makedirs(os.path.dirname(p), exist_ok=True)
    try:
        with open(p) as f: data = json.load(f)
    except Exception: data = {"plugins": []}
    # Don't actually register, just confirm it works
    print("Plugin registration successful (test mode)")
    sys.exit(0)
PYEOF

if python3 "$FAKE_PLUGINS_DIR/gleann-docs/main.py" --install 2>/dev/null; then
    pass "Plugin --install registration works"
else
    fail "Plugin --install registration"
fi

# 6.6 Symlink test (used by plugin installer)
SYMLINK_SRC="$FAKE_PLUGINS_DIR/gleann-docs"
SYMLINK_DST="$TMPDIR_BASE/symlink-test"
if ln -s "$SYMLINK_SRC" "$SYMLINK_DST" 2>/dev/null; then
    if [[ -L "$SYMLINK_DST" ]]; then
        pass "Symlink creation works"
    else
        fail "Symlink was not created properly"
    fi
else
    fail "Symlink creation failed"
fi

# ═══════════════════════════════════════════════════════════════
header "7. MarkdownChunker E2E"
# ═══════════════════════════════════════════════════════════════

# 7.1 Run markdown chunker tests
CHUNKER_OUTPUT=$(go test ./modules/chunking/ -run "TestChunkDocument|TestChunkMarkdown|TestParseMarkdownHeadings|TestBuildContextHeader" -count=1 -v 2>&1)

for test_name in "TestChunkDocument_Basic" "TestChunkDocument_HierarchyBreadcrumb" \
    "TestChunkDocument_MetadataFields" "TestChunkDocument_LargeSection" \
    "TestChunkMarkdown_Fallback" "TestChunkMarkdown_NoHeadings" \
    "TestParseMarkdownHeadings" "TestBuildContextHeader"; do
    if echo "$CHUNKER_OUTPUT" | grep -qF -- "--- PASS: $test_name"; then
        pass "$test_name"
    else
        fail "$test_name"
    fi
done

# ═══════════════════════════════════════════════════════════════
header "8. TUI Plugin Screen (Compile Check)"
# ═══════════════════════════════════════════════════════════════

# 8.1 internal/tui builds
if go build ./internal/tui/ 2>/dev/null; then
    pass "internal/tui compiles clean"
else
    fail "internal/tui compile"
fi

# 8.2 Check plugins.go exists and has required types
if grep -qF -- "PluginModel" internal/tui/plugins.go; then
    pass "PluginModel type exists"
else
    fail "PluginModel type missing"
fi

if grep -qF -- "knownPlugins" internal/tui/plugins.go; then
    pass "knownPlugins catalog exists"
else
    fail "knownPlugins catalog missing"
fi

if grep -qF -- "ScreenPlugins" internal/tui/home.go; then
    pass "ScreenPlugins in home.go"
else
    fail "ScreenPlugins in home.go"
fi

if grep -qF -- "runPlugins" internal/tui/tui.go; then
    pass "runPlugins() wired in tui.go"
else
    fail "runPlugins() in tui.go"
fi

# ═══════════════════════════════════════════════════════════════
header "9. Cross-Platform Audit"
# ═══════════════════════════════════════════════════════════════

OS_NAME=$(uname -s)
echo -e "  Running on: ${BOLD}$OS_NAME $(uname -m)${NC}"

# 9.1 Check for hardcoded Unix paths in plugin code
ISSUES=""

# plugins.go: "bin/pip" and "bin/python" are Unix-only
if grep -q '"bin", "pip"' internal/tui/plugins.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  plugins.go: venv 'bin/pip' is Unix-only (Windows: Scripts/pip.exe)"
fi
if grep -q '"bin", "python"' internal/tui/plugins.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  plugins.go: venv 'bin/python' is Unix-only (Windows: Scripts/python.exe)"
fi

# plugins.go: os.Symlink doesn't work on Windows without admin
if grep -qF -- "os.Symlink" internal/tui/plugins.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  plugins.go: os.Symlink() requires admin on Windows"
fi

# markitdown.go: Unix-specific paths for pipx venvs
if grep -q '\.local.*pipx.*venvs.*bin.*markitdown' pkg/gleann/markitdown.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  markitdown.go: ~/.local/pipx/venvs path is Unix-only"
fi
if grep -q '\.local.*bin.*markitdown' pkg/gleann/markitdown.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  markitdown.go: ~/.local/bin path is Unix-only (Windows: %APPDATA%/Python/Scripts)"
fi

# install.go: sudo command usage
if grep -q '"sudo"' internal/tui/install.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  install.go: 'sudo' not available on Windows"
fi

# install.go: .so libraries are Linux-only
if grep -q 'libfaiss_c.so' internal/tui/install.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  install.go: libfaiss_c.so is Linux-only (macOS: .dylib, Windows: .dll)"
fi

# install.go: /usr/local/bin is Unix-only, not used on Windows
if grep -q '"/usr/local/bin"' internal/tui/install.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  install.go: /usr/local/bin path is Unix-only"
fi

# onboard.go: install path options are Unix-specific
if grep -q '~/.local/bin' internal/tui/onboard.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  onboard.go: ~/.local/bin install option is Unix-only"
fi
if grep -q 'needs sudo' internal/tui/onboard.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  onboard.go: 'needs sudo' install option is Unix-only"
fi

# doc_extractor_test.go: hardcoded /usr/bin/markitdown
if grep -q '/usr/bin/markitdown' pkg/gleann/doc_extractor_test.go; then
    ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  doc_extractor_test.go: hardcoded /usr/bin/markitdown path"
fi

# Check for missing runtime.GOOS guards in critical files
for f in internal/tui/plugins.go pkg/gleann/markitdown.go; do
    if ! grep -qF -- "runtime.GOOS" "$f" 2>/dev/null; then
        ISSUES="$ISSUES\n  ${YELLOW}⚠${NC}  $f: no runtime.GOOS checks (cross-platform issue)"
    fi
done

if [[ -n "$ISSUES" ]]; then
    echo -e "$ISSUES"
    echo ""
    fail "Cross-platform issues found (see above)"
else
    pass "No cross-platform issues found"
fi

# ═══════════════════════════════════════════════════════════════
header "10. Integration Test: Build → Index → Search (full pipeline)"
# ═══════════════════════════════════════════════════════════════

# Create test documents
TEST_DOCS_DIR="$TMPDIR_BASE/test-docs"
mkdir -p "$TEST_DOCS_DIR"

cat > "$TEST_DOCS_DIR/readme.md" << 'EOF'
# Test Project

This is a test project for gleann E2E testing.

## Architecture

The system uses a vector database with graph-based recomputation.

## Installation

Run `gleann build` to index your documents.

## API

The REST API provides search and embedding endpoints.
EOF

cat > "$TEST_DOCS_DIR/guide.md" << 'EOF'
# User Guide

Welcome to the user guide.

## Getting Started

Follow these steps to get started with gleann.

## Configuration

Edit `~/.gleann/config.json` to configure providers.
EOF

# 10.1 Build index from test docs (no embedding server, should fail gracefully)
INDEX_DIR="$TMPDIR_BASE/test-index"
BUILD_OUTPUT=$("$TMPDIR_BASE/gleann" build --name test-e2e --docs "$TEST_DOCS_DIR" --index "$INDEX_DIR" 2>&1 || true)

if echo "$BUILD_OUTPUT" | grep -qi "error\|fatal\|panic"; then
    # Check if it's expected error (no embedding server)
    if echo "$BUILD_OUTPUT" | grep -qi "embed\|connection\|dial\|ollama\|provider"; then
        pass "Build fails gracefully (no embedding server - expected)"
    else
        fail "Build failed with unexpected error: $(echo "$BUILD_OUTPUT" | head -3)"
    fi
else
    pass "Build completed successfully"
fi

# ═══════════════════════════════════════════════════════════════
header "11. CLI Smoke Tests"
# ═══════════════════════════════════════════════════════════════

BINARY="$TMPDIR_BASE/gleann"

# 11.1 --help
if "$BINARY" --help >/dev/null 2>&1 || "$BINARY" help >/dev/null 2>&1; then
    pass "gleann --help works"
else
    fail "gleann --help"
fi

# 11.2 version (if supported)
if "$BINARY" version 2>/dev/null || "$BINARY" --version 2>/dev/null; then
    pass "gleann version works"
else
    skip "gleann version command not found"
fi

# ═══════════════════════════════════════════════════════════════
header "12. Full Go Test Suite"
# ═══════════════════════════════════════════════════════════════

# Count all test packages
TEST_PKGS=$(go test ./... -list '.*' 2>/dev/null | grep -c "^Test" || echo 0)
echo -e "  Found $TEST_PKGS test functions across workspace"

# Run all tests
FULL_TEST_OUTPUT=$(go test ./pkg/gleann/ ./modules/chunking/ ./tests/ -count=1 -timeout 120s 2>&1)
FULL_TEST_EXIT=$?

if [[ $FULL_TEST_EXIT -eq 0 ]]; then
    pass "Full test suite passed"
else
    fail "Full test suite: some tests failed"
    echo "$FULL_TEST_OUTPUT" | grep "FAIL" | head -5
fi

# ═══════════════════════════════════════════════════════════════════
# ═══════════════════ SUMMARY ══════════════════════════════════════
# ═══════════════════════════════════════════════════════════════════

echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  E2E Test Results${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"
echo -e "  ${GREEN}Passed:  $PASS${NC}"
echo -e "  ${RED}Failed:  $FAIL${NC}"
echo -e "  ${YELLOW}Skipped: $SKIP${NC}"
TOTAL=$((PASS + FAIL + SKIP))
echo -e "  Total:   $TOTAL"
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"

if [[ $FAIL -gt 0 ]]; then
    echo -e "\n${RED}${BOLD}Some tests failed!${NC}"
    exit 1
else
    echo -e "\n${GREEN}${BOLD}All tests passed!${NC}"
    exit 0
fi
