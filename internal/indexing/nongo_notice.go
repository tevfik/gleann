//go:build treesitter

package indexing

import "github.com/tevfik/gleann/pkg/gleann"

// NonGoNoticeMessage is empty in a treesitter build because non-Go code files
// have tree-sitter support enabled.
const NonGoNoticeMessage = ""

// NonGoBuildNotice returns a warning when non-Go code files were extracted without tree-sitter.
// In a treesitter build, tree-sitter is available so this is always empty.
func NonGoBuildNotice(nonGoCount int) string {
	return ""
}

// NonGoSymbolNotice returns a notice for non-Go symbols on graphs without tree-sitter edges.
// In a treesitter build, this is always empty.
func NonGoSymbolNotice(symbol string, db gleann.GraphDB, dir ...int) string {
	return ""
}

// NonGoFileNotice returns a notice for non-Go files on graphs without tree-sitter edges.
// In a treesitter build, this is always empty.
func NonGoFileNotice(file string, db gleann.GraphDB) string {
	return ""
}
