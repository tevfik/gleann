//go:build !treesitter

// Stub implementation when tree-sitter is not available.
// All non-Go languages fall back to regex-based boundary detection.
//
// To enable tree-sitter: go build -tags treesitter
package chunking

// treeSitterAvailable reports whether tree-sitter support is compiled in.
const treeSitterAvailable = false

// treeSitterChunk is a no-op stub when tree-sitter is not compiled in.
// Returns nil, causing the caller to fall back to regex patterns.
func treeSitterChunk(source, filename string, lang Language, config ASTChunkerConfig) []CodeChunk {
	return nil
}
