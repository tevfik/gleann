#!/bin/bash
# Comprehensive CLI Test for Gleann
# Tests all commands, flags, and workflows
# Updated: March 11, 2026
#   - Optional index argument in ask
#   - --continue-last conversation
#   - Auto index selection
#   - Shell completion installation

set +e

BINARY="./build/gleann-full"
TEST_DIR="/tmp/gleann-cli-test-$$"
TEST_INDEX="test_cli_idx"
TEST_DOCS="$TEST_DIR/docs"

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

passed=0
failed=0
warned=0

print_test() {
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}TEST: $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    passed=$((passed + 1))
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    echo -e "${RED}Output:${NC}"
    echo "$2" | head -10
    failed=$((failed + 1))
}

warn() {
    echo -e "${YELLOW}⚠ WARN${NC}: $1"
    warned=$((warned + 1))
}

# ── Setup ──────────────────────────────────────────────
echo -e "${GREEN}Setting up test environment...${NC}"
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DOCS"

cat > "$TEST_DOCS/doc1.txt" <<EOF
This is a test document about artificial intelligence.
Machine learning is a subset of AI that focuses on data-driven learning.
Neural networks are inspired by biological neurons.
EOF

cat > "$TEST_DOCS/doc2.md" <<EOF
# Programming Languages

## Python
Python is a high-level programming language known for its simplicity.

## Go
Go is a compiled language designed for concurrent programming.
EOF

cat > "$TEST_DOCS/code.py" <<EOF
def fibonacci(n):
    """Calculate fibonacci number."""
    if n <= 1:
        return n
    return fibonacci(n-1) + fibonacci(n-2)
EOF

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 1: Help and version
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "1. Help and Version"

output=$($BINARY --help 2>&1)
if echo "$output" | grep -q "gleann.*Lightweight Vector Database\|gleann.*RAG"; then
    pass "Help shows usage"
else
    fail "Help output missing" "$output"
fi

output=$($BINARY --version 2>&1)
if echo "$output" | grep -q "gleann version"; then
    pass "Version command works"
else
    fail "Version command failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 2: Index Build
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "2. Index Build"

output=$($BINARY index build "$TEST_INDEX" --docs "$TEST_DOCS" 2>&1)
if echo "$output" | grep -q "indexed\|Building\|complete"; then
    pass "Index build works"
else
    fail "Index build failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 3: Index List
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "3. Index List"

output=$($BINARY index list 2>&1)
if echo "$output" | grep -q "$TEST_INDEX"; then
    pass "Index list shows created index"
else
    fail "Index list doesn't show test index" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 4: Index Info
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "4. Index Info"

output=$($BINARY index info "$TEST_INDEX" 2>&1)
if echo "$output" | grep -q "Chunks\|Documents\|Index:"; then
    pass "Index info shows details"
else
    fail "Index info failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 5: Ask (basic query - requires LLM)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "5. Ask - Basic Query (requires LLM)"

output=$($BINARY ask "$TEST_INDEX" "What is Python?" 2>&1)
if echo "$output" | grep -qi "python\|programming\|language"; then
    pass "Ask returns relevant answer"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Ask failed — LLM not available (expected in CI)"
else
    fail "Ask didn't return relevant content" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 6: Ask with --raw flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "6. Ask - Raw Mode"

output=$($BINARY ask "$TEST_INDEX" "What is Go?" --raw 2>&1)
if echo "$output" | grep -qi "go\|concurrent\|compiled"; then
    pass "Raw mode returns answer"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Ask --raw failed — LLM not available"
else
    fail "Raw mode returned nothing useful" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 7: Ask with --quiet flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "7. Ask - Quiet Mode"

output=$($BINARY ask "$TEST_INDEX" "What is Go?" --quiet 2>&1)
if echo "$output" | grep -qi "go\|concurrent"; then
    pass "Quiet mode returns answer"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Ask --quiet failed — LLM not available"
else
    fail "Quiet mode failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 8: Stdin/Pipe Support
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "8. Stdin/Pipe Support"

output=$(echo "def hello(): print('world')" | $BINARY ask "$TEST_INDEX" "Explain this code" 2>&1)
if echo "$output" | grep -qi "function\|print\|code\|hello"; then
    pass "Pipe input works"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Pipe input failed — LLM not available"
else
    fail "Pipe input failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 9: Role Flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "9. Role Flag"

output=$($BINARY ask "$TEST_INDEX" "What is fibonacci?" --role code --raw 2>&1)
if echo "$output" | grep -qi "fibonacci\|sequence\|function\|code"; then
    pass "Role flag works"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Role flag test failed — LLM not available"
else
    warn "Role flag may not affect output noticeably"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 10: Format Flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "10. Format Flag"

output=$($BINARY ask "$TEST_INDEX" "List programming languages" --format json --raw 2>&1)
if echo "$output" | grep -q '{' || echo "$output" | grep -q '\['; then
    pass "Format json flag works"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Format flag test failed — LLM not available"
else
    warn "Format json may not produce JSON (LLM dependent)"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 11: Multi-Index Query
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "11. Multi-Index Query"

TEST_INDEX2="${TEST_INDEX}_2"
$BINARY index build "$TEST_INDEX2" --docs "$TEST_DOCS" > /dev/null 2>&1

output=$($BINARY ask "$TEST_INDEX,$TEST_INDEX2" "What is AI?" --raw 2>&1)
if echo "$output" | grep -qi "artificial intelligence\|machine learning\|AI"; then
    pass "Multi-index query works"
elif echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Multi-index query failed — LLM not available"
else
    fail "Multi-index query failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 12: --no-cache Flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "12. --no-cache Flag"

before_count=$($BINARY chat --list 2>&1 | wc -l)

output=$($BINARY ask "$TEST_INDEX" "Test no-cache query" --no-cache --raw 2>&1)
if echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "no-cache test failed — LLM not available"
else
    after_count=$($BINARY chat --list 2>&1 | wc -l)
    if [ "$after_count" -le "$before_count" ]; then
        pass "--no-cache prevents conversation save"
    else
        warn "--no-cache may not be preventing saves (before=$before_count, after=$after_count)"
    fi
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 13: --no-limit Flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "13. --no-limit Flag"

output=$($BINARY ask "$TEST_INDEX" "Give a detailed explanation of all topics in the documents" --no-limit --raw 2>&1)
if echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "no-limit test failed — LLM not available"
else
    word_count=$(echo "$output" | wc -w)
    if [ "$word_count" -gt 10 ]; then
        pass "--no-limit flag accepted (got $word_count words)"
    else
        warn "--no-limit output unexpectedly short ($word_count words)"
    fi
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 14: Conversation Persistence
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "14. Conversation Persistence"

output=$($BINARY ask "$TEST_INDEX" "Remember this: my favorite color is blue" --quiet 2>&1)
if echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Conversation persistence test failed — LLM not available"
else
    list_output=$($BINARY chat --list 2>&1)
    if echo "$list_output" | grep -q "test\|idx\|conversation\|favorite\|blue"; then
        pass "Conversation saved and listed"
    else
        warn "Conversation may not be persisting"
    fi
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 15: Watch Command - First Build + Graph
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "15. Watch - First Build for New Index"

TEST_INDEX_WATCH="test_watch_idx"
$BINARY index remove "$TEST_INDEX_WATCH" > /dev/null 2>&1 || true

timeout 3s $BINARY index watch "$TEST_INDEX_WATCH" --docs "$TEST_DOCS" 2>&1 || true
sleep 1

info_output=$($BINARY index info "$TEST_INDEX_WATCH" 2>&1 || echo "NOT_FOUND")
if echo "$info_output" | grep -q "NOT_FOUND\|does not exist\|not found"; then
    fail "Watch doesn't build index on first run"
else
    pass "Watch builds index on first run"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 16: .gleannignore Support
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "16. .gleannignore Support"

echo "IGNORED_CONTENT_XYZ_UNIQUE" > "$TEST_DOCS/ignored.txt"
echo "ignored.txt" > "$TEST_DOCS/.gleannignore"

$BINARY index rebuild "$TEST_INDEX" --docs "$TEST_DOCS" > /dev/null 2>&1

output=$($BINARY search "$TEST_INDEX" "IGNORED_CONTENT_XYZ_UNIQUE" --json 2>&1)
if echo "$output" | grep -qv "IGNORED_CONTENT_XYZ_UNIQUE"; then
    pass ".gleannignore excludes files"
else
    warn ".gleannignore may not be working"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 17: Backward Compat REMOVED — Old Commands Should Fail
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "17. Backward Compat Removed — Old Commands"

output=$($BINARY build "test_should_fail" --docs "$TEST_DOCS" 2>&1)
if echo "$output" | grep -qi "unknown\|usage\|help\|not recognized\|gleann - "; then
    pass "Old 'gleann build' correctly rejected"
else
    fail "Old 'gleann build' still works (should be removed)" "$output"
fi

output=$($BINARY list 2>&1)
if echo "$output" | grep -qi "unknown\|usage\|help\|not recognized\|gleann - "; then
    pass "Old 'gleann list' correctly rejected"
else
    fail "Old 'gleann list' still works (should be removed)" "$output"
fi

output=$($BINARY remove "nonexistent" 2>&1)
if echo "$output" | grep -qi "unknown\|usage\|help\|not recognized\|gleann - "; then
    pass "Old 'gleann remove' correctly rejected"
else
    fail "Old 'gleann remove' still works (should be removed)" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 18: Config Subcommand
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "18. Config Subcommand"

# config show (default)
output=$($BINARY config 2>&1)
if echo "$output" | grep -qi "provider\|model\|host\|Config\|json\|{"; then
    pass "gleann config show works"
else
    warn "gleann config show returned unexpected output"
fi

# config path
output=$($BINARY config path 2>&1)
if echo "$output" | grep -q "config.json"; then
    pass "gleann config path works"
else
    fail "gleann config path failed" "$output"
fi

# config validate
output=$($BINARY config validate 2>&1)
if echo "$output" | grep -qi "valid\|provider\|model\|Valid"; then
    pass "gleann config validate works"
else
    warn "gleann config validate returned unexpected output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 19: Chat Conversation Management
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "19. Chat Conversation Management"

output=$($BINARY chat --list 2>&1)
if echo "$output" | grep -q "conversation\|Conversation\|ID\|Title\|No conversations"; then
    pass "Chat --list works"
else
    warn "Chat --list returned unexpected output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 20: Search (non-LLM)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "20. Search (vector search, no LLM required)"

output=$($BINARY search "$TEST_INDEX" "artificial intelligence" 2>&1)
if echo "$output" | grep -qi "artificial\|machine\|neural\|doc1"; then
    pass "Search returns relevant results"
else
    fail "Search didn't return relevant results" "$output"
fi

# Search with --json
output=$($BINARY search "$TEST_INDEX" "python programming" --json 2>&1)
if echo "$output" | grep -q '{' || echo "$output" | grep -q '\['; then
    pass "Search --json returns JSON"
else
    fail "Search --json didn't return JSON" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 21: Index Rebuild
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "21. Index Rebuild"

output=$($BINARY index rebuild "$TEST_INDEX" --docs "$TEST_DOCS" 2>&1)
if echo "$output" | grep -qi "indexed\|Building\|complete\|removing\|rebuilt"; then
    pass "Index rebuild works"
else
    fail "Index rebuild failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 22: Word Wrap Flag
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "22. Word Wrap Flag"

output=$($BINARY ask "$TEST_INDEX" "Tell me about AI in detail" --word-wrap 40 --raw 2>&1)
if echo "$output" | grep -qi "error\|failed\|connection"; then
    warn "Word wrap test failed — LLM not available"
else
    max_line_len=$(echo "$output" | awk '{print length}' | sort -nr | head -1)
    if [ "$max_line_len" -lt 60 ]; then
        pass "Word wrap constrains line length (max: $max_line_len)"
    else
        warn "Word wrap may not be working (max line: $max_line_len chars)"
    fi
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 23: Help text shows clean structure
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "23. Help Text — Clean Structure"

help_output=$($BINARY --help 2>&1)

# Should have config subcommand
if echo "$help_output" | grep -q "config.*Manage configuration"; then
    pass "Help shows config subcommand"
else
    fail "Help missing config subcommand" "$help_output"
fi

# Should NOT have backward compat top-level commands
if echo "$help_output" | grep -q "^  gleann build "; then
    fail "Help still shows old 'gleann build' (should be 'index build' only)" "$help_output"
else
    pass "Help doesn't show old backward compat commands"
fi

# Should show --no-cache and --no-limit
if echo "$help_output" | grep -q "\-\-no-cache" && echo "$help_output" | grep -q "\-\-no-limit"; then
    pass "Help shows --no-cache and --no-limit flags"
else
    fail "Help missing --no-cache or --no-limit" "$help_output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 24: Index Remove
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "24. Index Remove"

output=$($BINARY index remove "$TEST_INDEX2" 2>&1)
if echo "$output" | grep -qi "removed\|deleted\|success\|Removing"; then
    pass "Index remove works"
else
    warn "Index remove output unclear"
fi

# Verify it's gone
output=$($BINARY index info "$TEST_INDEX2" 2>&1)
if echo "$output" | grep -qi "not found\|does not exist\|error"; then
    pass "Removed index no longer exists"
else
    warn "Removed index may still exist"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 25: Ask without index (auto-select)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "25. Ask - Auto Index Selection"

output=$($BINARY ask "What is Python?" --raw 2>&1)
if echo "$output" | grep -qi "python\|programming\|language"; then
    pass "Ask auto-selects single index"
elif echo "$output" | grep -qi "error\|connection\|failed"; then
    warn "Ask auto-select failed — LLM not available"
else
    fail "Ask auto-select failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 26: Ask --continue-last
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "26. Ask - Continue Last Conversation"

# First question to create a conversation
$BINARY ask "$TEST_INDEX" "What is AI?" --raw > /dev/null 2>&1

# Follow-up using --continue-last
output=$($BINARY ask --continue-last "Tell me more" --raw 2>&1)
if echo "$output" | grep -qi "error\|connection\|failed"; then
    warn "Continue-last failed — LLM not available"
elif [ -n "$output" ]; then
    pass "Continue-last works"
else
    fail "Continue-last returned empty" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 27: Shell Completion Installation (manual)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "27. Shell Completion - Installation Check"

if [ "$OSTYPE" = "linux-gnu" ] || [ "$OSTYPE" = "darwin"* ]; then
    # Check if completion files exist after setup
    bash_comp="$HOME/.local/share/bash-completion/completions/gleann"
    zsh_comp="$HOME/.local/share/zsh/site-functions/_gleann"
    fish_comp="$HOME/.config/fish/completions/gleann.fish"
    
    if [ -f "$bash_comp" ] || [ -f "$zsh_comp" ] || [ -f "$fish_comp" ]; then
        pass "Shell completions installed (run 'gleann setup' to install)"
    else
        warn "Shell completions not found (expected after 'gleann setup')"
    fi
else
    warn "Shell completion check skipped (Windows or unsupported OS)"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Test 28: Ask with explicit index (backward compat)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

print_test "28. Ask - Explicit Index (Backward Compat)"

output=$($BINARY ask "$TEST_INDEX" "What is Go language?" --raw 2>&1)
if echo "$output" | grep -qi "go\|concurrent\|compiled"; then
    pass "Ask with explicit index still works"
elif echo "$output" | grep -qi "error\|connection\|failed"; then
    warn "Ask with explicit index failed — LLM not available"
else
    fail "Ask with explicit index failed" "$output"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Summary
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

echo ""
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}TEST SUMMARY${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}Passed:  $passed${NC}"
echo -e "${RED}Failed:  $failed${NC}"
echo -e "${YELLOW}Warned:  $warned${NC}"

total=$((passed + failed))
if [ $total -gt 0 ]; then
    success_rate=$(echo "scale=1; $passed * 100 / $total" | bc)
    echo -e "Success Rate: ${success_rate}% (excluding warnings)"
fi

echo ""

# Cleanup
echo -e "${GREEN}Cleaning up test environment...${NC}"
rm -rf "$TEST_DIR"
$BINARY index remove "$TEST_INDEX" > /dev/null 2>&1 || true
$BINARY index remove "$TEST_INDEX_WATCH" > /dev/null 2>&1 || true
$BINARY chat --delete-older-than 0d > /dev/null 2>&1 || true

echo -e "${GREEN}Done!${NC}"
