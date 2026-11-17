package indexer

import (
	"path/filepath"
	"strings"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

// isTestSymbol returns true if the symbol is in a test file or is a test function/benchmark.
func isTestSymbol(filePath, name string) bool {
	base := filepath.Base(filePath)
	cleanPath := filepath.ToSlash(filePath)

	// Test directories
	if strings.Contains(cleanPath, "/test/") || strings.Contains(cleanPath, "/tests/") || strings.Contains(cleanPath, "/testing/") {
		return true
	}

	// File naming conventions
	if strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py") ||
		strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.js") ||
		strings.HasSuffix(base, ".test.js") ||
		strings.HasSuffix(base, "_spec.rb") ||
		strings.HasSuffix(base, "Test.java") {
		return true
	}

	// Function naming conventions
	if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Fuzz") || strings.HasPrefix(name, "test_") {
		return true
	}

	return false
}

// dedupeSymbols keeps one node per FQN. A callee stub (no File) must never
// shadow the real declaration, whatever order the workers finished in, so a
// declared symbol replaces an earlier stub (C-03).
func dedupeSymbols(in []gtypes.SymbolNode) []gtypes.SymbolNode {
	pos := make(map[string]int, len(in))
	out := make([]gtypes.SymbolNode, 0, len(in))
	for _, sym := range in {
		if sym.FQN == "" {
			continue
		}
		if i, ok := pos[sym.FQN]; ok {
			if out[i].File == "" && sym.File != "" {
				out[i] = sym
			}
			continue
		}
		pos[sym.FQN] = len(out)
		out = append(out, sym)
	}
	return out
}
