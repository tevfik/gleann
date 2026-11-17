//go:build treesitter

package indexer

import (
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// chunkWithCalls chunks a non-Go source file and extracts its CALLS edges from
// ONE tree-sitter parse (the parser goes back to the pool, the tree is closed).
// The chunks are exactly those of ASTChunker.ChunkCode. ok is false when the
// language has no tree-sitter binding or the parse failed; the caller then
// chunks with the regex fallback and the file has declarations only.
//
// nodes holds the callee stubs and, when the file has module-level calls, the
// anchored synthetic "<script>" caller (see collectTSCallsPrecise).
func chunkWithCalls(chunker *chunking.ASTChunker, module, relPath, absPath, source string, lang chunking.Language) (chunks []chunking.CodeChunk, nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls, ok bool) {
	if lang == chunking.LangGo || !chunking.IsTreeSitterLanguage(lang) {
		return nil, nil, nil, false
	}
	parser, tree, src, parsed := chunking.ParseTree(lang, source)
	if !parsed {
		return nil, nil, nil, false
	}
	defer chunking.ReturnParser(lang, parser)
	defer tree.Close()

	chunks = chunker.ChunkCodeWithTree(lang, absPath, source, tree, src)
	if chunks == nil {
		return nil, nil, nil, false
	}
	nodes, edges = collectTSCallsPrecise(module, relPath, lang, tree, src, chunks)
	return chunks, nodes, edges, true
}
