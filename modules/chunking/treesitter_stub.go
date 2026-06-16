//go:build !treesitter

// Stub implementation when tree-sitter is not available.
// All non-Go languages fall back to regex-based boundary detection.
//
// To enable tree-sitter: go build -tags treesitter
package chunking

// treeSitterAvailable reports whether tree-sitter support is compiled in.
const treeSitterAvailable = false

// IsTreeSitterLanguage always returns false in the stub build. There is
// no tree-sitter grammar available, so callers must rely on other
// strategies (go/ast for Go, regex for everything else).
func IsTreeSitterLanguage(lang Language) bool {
	return false
}

// ParseTree is a no-op stub when tree-sitter is not compiled in.
// It always returns ok=false, signalling the caller to use a fallback path.
func ParseTree(lang Language, source string) (parser interface{}, tree interface{}, sourceBytes []byte, ok bool) {
	return nil, nil, nil, false
}

// GetOrCompileQuery is a no-op stub; returns nil because no tree-sitter
// query can be compiled without the runtime.
func GetOrCompileQuery(lang Language, body string) interface{} {
	return nil
}

// ChunkCodeWithTree is a no-op stub: there is no tree to consume, so the
// caller must fall back to ASTChunker.ChunkCode.
func ChunkCodeWithTree(lang Language, filename, source string, tree interface{}, sourceBytes []byte) []CodeChunk {
	return nil
}

// treeSitterChunk is a no-op stub when tree-sitter is not compiled in.
// Returns nil, causing the caller to fall back to regex patterns.
func treeSitterChunk(source, filename string, lang Language, config ASTChunkerConfig) []CodeChunk {
	return nil
}
