//go:build !treesitter

package indexing

import (
	"fmt"

	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// NonGoNoticeMessage is the message explaining that non-Go files lack tree-sitter edges.
const NonGoNoticeMessage = "note: non-Go code was extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"

// NonGoBuildNotice returns a warning when non-Go code files were extracted without tree-sitter.
func NonGoBuildNotice(nonGoCount int) string {
	if nonGoCount <= 0 {
		return ""
	}
	if nonGoCount == 1 {
		return "note: 1 non-Go code file extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"
	}
	return fmt.Sprintf("note: %d non-Go code files extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)", nonGoCount)
}

// NonGoSymbolNotice returns a notice when symbol belongs to a non-Go file and the graph lacks tree-sitter edges.
func NonGoSymbolNotice(symbol string, db gleann.GraphDB, dir ...int) string {
	if db == nil || symbol == "" {
		return ""
	}
	d := gleann.DirectionCallers
	if len(dir) > 0 {
		d = dir[0]
	}
	if unwrap, ok := db.(interface{ Unwrap() gleann.GraphDB }); ok {
		db = unwrap.Unwrap()
	}
	var files []string
	var err error
	if resolver, ok := db.(interface {
		ResolveSymbolFiles(string, int) ([]string, error)
	}); ok {
		files, err = resolver.ResolveSymbolFiles(symbol, d)
	} else if resolver, ok := db.(interface {
		ResolveSymbolFiles(string) ([]string, error)
	}); ok {
		files, err = resolver.ResolveSymbolFiles(symbol)
	}
	if err == nil && len(files) > 0 {
		allGo := true
		hasNonGo := false
		for _, file := range files {
			lang := chunking.DetectLanguage(file)
			if lang != chunking.LangGo && lang != chunking.LangUnknown {
				hasNonGo = true
				allGo = false
			}
		}
		if allGo {
			return ""
		}
		if hasNonGo {
			for _, file := range files {
				if notice := NonGoFileNotice(file, db); notice != "" {
					return notice
				}
			}
		}
		return ""
	}

	syms, err := db.SymbolSearch(symbol)
	if err != nil || len(syms) == 0 {
		return ""
	}
	file := syms[0].File
	for _, s := range syms {
		if s.FQN == symbol || s.Name == symbol {
			file = s.File
			break
		}
	}
	return NonGoFileNotice(file, db)
}

// NonGoFileNotice returns a notice when file is a non-Go code file and the graph lacks tree-sitter edges.
func NonGoFileNotice(file string, db gleann.GraphDB) string {
	if file == "" {
		return ""
	}
	lang := chunking.DetectLanguage(file)
	if lang == chunking.LangGo || lang == chunking.LangUnknown {
		return ""
	}
	if hasTreeSitterEdges(db) {
		return ""
	}
	return NonGoNoticeMessage
}

func hasTreeSitterEdges(db gleann.GraphDB) bool {
	if unwrap, ok := db.(interface{ Unwrap() gleann.GraphDB }); ok {
		db = unwrap.Unwrap()
	}
	if checker, ok := db.(interface{ HasTreeSitterEdges() bool }); ok {
		return checker.HasTreeSitterEdges()
	}
	return false
}
