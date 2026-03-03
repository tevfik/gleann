#!/bin/bash
set -eo pipefail

echo "=== Gleann E2E Modularization Test ==="

INDEX_NAME="e2e-index"
TEST_MODE="nomic-embed-text"

echo "1. Cleaning up old test data..."
rm -vf gleann-test
rm -rf ~/.gleann/indexes/${INDEX_NAME}*

echo "2. Building test binary..."
CGO_ENABLED=1 go build -o gleann-test ./cmd/gleann

echo "3. Building index with graph (--graph)..."
./gleann-test build ${INDEX_NAME} --docs pkg/gleann/ --graph --model ${TEST_MODE} --no-mmap

echo "4. Checking Info..."
./gleann-test info ${INDEX_NAME}

echo "5. Testing AST Graph Dependencies of gleann.LeannBuilder.Build..."
./gleann-test graph deps "github.com/tevfik/gleann/pkg/gleann.LeannBuilder.Build" --index ${INDEX_NAME}

echo "6. Testing BM25 / HNSW Search..."
./gleann-test search ${INDEX_NAME} "BuildFromTexts" --model ${TEST_MODE} --no-mmap

echo "=== All Tests Passed ==="
