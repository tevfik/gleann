//go:build !treesitter

package indexer

import (
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// chunkWithCalls is the pure-Go build's stub: there is no tree-sitter, so
// non-Go files keep the chunker's regex fallback and contribute declarations
// only (ok is always false).
func chunkWithCalls(chunker *chunking.ASTChunker, module, relPath, absPath, source string, lang chunking.Language) (chunks []chunking.CodeChunk, nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls, ok bool) {
	return nil, nil, nil, false
}
