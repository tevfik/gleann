// Package indexer provides an AST-based graph indexer that extracts code symbols
// (functions, methods, types, structs, interfaces, consts, vars) and their
// relationships (DECLARES, CALLS, IMPLEMENTS, REFERENCES) from source files
// and persists them into the KuzuDB graph database.
//
// It reuses the existing internal/chunking AST parser for symbol extraction.
// For Go files it additionally extracts CALLS relationships using go/ast.
package indexer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/internal/graph/kuzu"
)

// Indexer walks a codebase and populates a KuzuDB graph with AST relationships.
type Indexer struct {
	db      *kuzu.DB
	chunker *chunking.ASTChunker
	module  string // Go module prefix, e.g. "github.com/tevfik/gleann"
	root    string // absolute root path used to derive relative package paths
}

// New creates a new Indexer.
//
//   - db:     open KuzuDB instance
//   - module: Go module name from go.mod (used to build FQNs)
//   - root:   root directory of the codebase
func New(db *kuzu.DB, module, root string) *Indexer {
	cfg := chunking.DefaultASTChunkerConfig()
	return &Indexer{
		db:      db,
		chunker: chunking.NewASTChunker(cfg),
		module:  strings.TrimSuffix(module, "/"),
		root:    filepath.Clean(root),
	}
}

// IndexFile parses one source file and writes its symbols and edges into KuzuDB.
//
// Steps:
//  1. Detect language from filename.
//  2. Extract semantic chunks via ASTChunker (each chunk ≈ one symbol).
//  3. UpsertFile + UpsertSymbol for every chunk.
//  4. AddDeclares for every chunk.
//  5. For Go files: extract CALLS relationships with go/ast.
func (idx *Indexer) IndexFile(absPath, source string) error {
	lang := string(chunking.DetectLanguage(absPath))
	relPath := idx.relPath(absPath)

	// Upsert the file node.
	if err := idx.db.UpsertFile(relPath, lang); err != nil {
		return fmt.Errorf("upsert file %s: %w", relPath, err)
	}

	// Chunk to get symbols.
	chunks := idx.chunker.ChunkCode(source, absPath)
	for _, ch := range chunks {
		if ch.Name == "" || ch.NodeType == "preamble" {
			continue
		}

		fqn := idx.buildFQN(relPath, ch.Name)

		sym := kuzu.SymbolNode{
			FQN:  fqn,
			Kind: ch.NodeType,
			File: relPath,
			Line: int64(ch.StartLine),
			Name: ch.Name,
		}
		if err := idx.db.UpsertSymbol(sym); err != nil {
			return fmt.Errorf("upsert symbol %s: %w", fqn, err)
		}
		if err := idx.db.AddDeclares(relPath, fqn); err != nil {
			return fmt.Errorf("add declares %s: %w", fqn, err)
		}
	}

	// Extract CALLS relationships for Go files.
	if strings.HasSuffix(absPath, ".go") {
		if err := extractGoCallEdges(idx, relPath, source, chunks); err != nil {
			// Non-fatal: log and continue.
			fmt.Fprintf(os.Stderr, "warn: call extraction failed for %s: %v\n", relPath, err)
		}
	} else {
		if err := extractTSCallEdges(idx, absPath, relPath, source, chunks); err != nil {
			fmt.Fprintf(os.Stderr, "warn: ts call extraction failed for %s: %v\n", relPath, err)
		}
	}

	return nil
}

// IndexDir recursively indexes all supported source files under root.
func (idx *Indexer) IndexDir(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden and vendor directories.
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !chunking.IsCodeSourceFile(path) {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		return idx.IndexFile(path, string(src))
	})
}

// relPath converts an absolute path to a path relative to idx.root.
func (idx *Indexer) relPath(absPath string) string {
	rel, err := filepath.Rel(idx.root, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

// buildFQN constructs a Fully Qualified Name for a symbol.
// Format: <module>/<package>.<SymbolName>
// e.g. "github.com/tevfik/gleann/internal/vault.Store.Put"
func (idx *Indexer) buildFQN(relPath, symbolName string) string {
	// Remove the filename to get the package path segment.
	pkg := filepath.Dir(relPath)
	pkg = strings.ReplaceAll(pkg, string(filepath.Separator), "/")
	if pkg == "." {
		pkg = ""
	}

	var prefix string
	if idx.module != "" {
		if pkg != "" {
			prefix = idx.module + "/" + pkg
		} else {
			prefix = idx.module
		}
	} else {
		prefix = pkg
	}

	if prefix == "" {
		return symbolName
	}
	return prefix + "." + symbolName
}
