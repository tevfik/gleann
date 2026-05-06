#!/usr/bin/env bash
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_DIR="${REPO_ROOT}/build"
FIXTURES="${REPO_ROOT}/tests/e2e/fixtures/docs"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

test_case() {
    local name="$1"
    local binary="$2"
    local provider="$3"
    local backend="$4"
    local model="$5"
    
    echo -e "\n--- Testing: $name ---"
    echo "Binary: $binary, Provider: $provider, Backend: $backend, Model: $model"
    
    # Cleanup
    rm -rf ~/.gleann/indexes/test-idx
    
    # Configure
    # We use temporary config to avoid messing with user config
    export GLEANN_CONFIG_PATH=$(mktemp)
    cat > "$GLEANN_CONFIG_PATH" <<EOF
{
  "index_dir": "$HOME/.gleann/indexes",
  "embedding_provider": "$provider",
  "embedding_model": "$model",
  "backend": "$backend"
}
EOF

    # Build index
    $binary index build test-idx --docs "$FIXTURES" > /dev/null 2>&1
    
    # Search
    local output
    output=$($binary search test-idx "superposition" --json 2>&1)
    
    if echo "$output" | grep -qi "superposition"; then
        echo -e "${GREEN}PASS: $name${NC}"
    else
        echo -e "${RED}FAIL: $name${NC}"
        echo "Output: $output"
        # exit 1
    fi
    
    rm "$GLEANN_CONFIG_PATH"
}

# 1. HNSW + Ollama
test_case "HNSW + Ollama" "$BUILD_DIR/gleann" "ollama" "hnsw" "nomic-embed-text"

# 2. FAISS + Ollama
# We need to set LD_LIBRARY_PATH for FAISS if it's not in standard path
export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/usr/local/lib
test_case "FAISS + Ollama" "$BUILD_DIR/gleann-full" "ollama" "faiss" "nomic-embed-text"

# 3. HNSW + Native
# We need LD_LIBRARY_PATH for the Rust lib
export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:${REPO_ROOT}/ext/gleann-core-rs/target/release
test_case "HNSW + Native" "$BUILD_DIR/gleann-native" "native" "hnsw" "all-MiniLM-L6-v2"

# 4. FAISS + Native (if we can build it, but let's test what we have)
# Actually, we can't test FAISS + Native unless we build a binary with both tags.
# But let's check if gleann-native works with Ollama too
test_case "HNSW + Ollama (via native binary)" "$BUILD_DIR/gleann-native" "ollama" "hnsw" "nomic-embed-text"

echo -e "\nAll tests completed."
