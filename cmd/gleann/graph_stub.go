//go:build !treesitter

package main

import (
	"fmt"
	"os"
)

// buildGraphIndex is a no-op stub when built without CGo/tree-sitter support.
func buildGraphIndex(name, docsDir, indexDir string) {
	fmt.Fprintln(os.Stderr, "⚠️  Graph indexing requires CGo (build with -tags treesitter).")
}

// cmdGraph is a stub when built without CGo/tree-sitter support.
func cmdGraph(args []string) {
	fmt.Fprintln(os.Stderr, "⚠️  Graph commands require CGo (build with -tags treesitter).")
	fmt.Fprintln(os.Stderr, "   Use the gleann-full binary or rebuild with: go build -tags treesitter ./cmd/gleann")
	os.Exit(1)
}
