// Package indexer provides an AST-based graph indexer that extracts code symbols
// (functions, methods, types, structs, interfaces, consts, vars) and their
// relationships (DECLARES, CALLS, IMPLEMENTS, REFERENCES) from source files
// and persists them into the KuzuDB graph database.
//
// It reuses the existing internal/chunking AST parser for symbol extraction.
// For Go files it additionally extracts CALLS relationships using go/ast.
// All writes for a single file are committed in a single KuzuDB transaction.
package indexer

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/modules/chunking"
)

// Indexer walks a codebase and populates a KuzuDB graph with AST relationships.
type Indexer struct {
	db      *kuzu.DB
	chunker *chunking.ASTChunker
	module  string     // Go module prefix, e.g. "github.com/tevfik/gleann"
	root    string     // absolute root path used to derive relative package paths
	writeMu sync.Mutex // Ensures only one KuzuDB write transaction occurs at a time
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

// IndexFile parses one source file and writes its symbols and edges into KuzuDB
// using a single transaction.
func (idx *Indexer) IndexFile(absPath, source string) error {
	f, syms, decls, calls, err := idx.indexFileOnConn(absPath, source)
	if err != nil {
		return err
	}

	uniqueSymbols := make([]kuzu.SymbolNode, 0, len(syms))
	seenSymbols := make(map[string]bool)
	for _, sym := range syms {
		if !seenSymbols[sym.FQN] {
			seenSymbols[sym.FQN] = true
			uniqueSymbols = append(uniqueSymbols, sym)
		}
	}
	syms = uniqueSymbols

	uniqueDeclares := make([]kuzu.EdgeDeclares, 0, len(decls))
	seenDeclares := make(map[string]bool)
	for _, d := range decls {
		key := d.FilePath + "->" + d.SymbolFQN
		if !seenDeclares[key] {
			seenDeclares[key] = true
			uniqueDeclares = append(uniqueDeclares, d)
		}
	}
	decls = uniqueDeclares

	uniqueCalls := make([]kuzu.EdgeCalls, 0, len(calls))
	seenCalls := make(map[string]bool)
	for _, c := range calls {
		key := c.CallerFQN + "->" + c.CalleeFQN
		if !seenCalls[key] {
			seenCalls[key] = true
			uniqueCalls = append(uniqueCalls, c)
		}
	}
	calls = uniqueCalls

	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	// 1. Delete old file node + symbols for this file
	if err := kuzu.ExecTxOn(idx.db.Conn(), kuzu.DeleteFileQueries(f.Path)); err != nil {
		return fmt.Errorf("delete old: %w", err)
	}

	doCopy := func(tableName string, writeFunc func(p string) error) error {
		tmp, err := os.CreateTemp("", "kuzu_"+tableName+"_*.csv")
		if err != nil {
			return err
		}
		csvPath := tmp.Name()
		tmp.Close()
		defer os.Remove(csvPath)

		if err := writeFunc(csvPath); err != nil {
			return fmt.Errorf("write %s: %w", tableName, err)
		}
		if err := kuzu.ExecCopyCSV(idx.db.Conn(), tableName, csvPath); err != nil {
			return fmt.Errorf("copy %s: %w", tableName, err)
		}
		return nil
	}

	if f != nil {
		if err := doCopy("CodeFile", func(p string) error { return kuzu.WriteFileNodesCSV(p, []kuzu.FileNode{*f}) }); err != nil {
			return err
		}
	}
	if len(syms) > 0 {
		if err := doCopy("Symbol", func(p string) error { return kuzu.WriteSymbolNodesCSV(p, syms) }); err != nil {
			return err
		}
	}
	if len(decls) > 0 {
		if err := doCopy("DECLARES", func(p string) error { return kuzu.WriteDeclaresCSV(p, decls) }); err != nil {
			return err
		}
	}
	if len(calls) > 0 {
		if err := doCopy("CALLS", func(p string) error { return kuzu.WriteCallsCSV(p, calls) }); err != nil {
			return err
		}
	}

	return nil
}

// indexFileOnConn is the core implementation for parallel parsing.
// It extracts all File, Symbol, Declares and Calls structs and returns them.
func (idx *Indexer) indexFileOnConn(absPath, source string) (file *kuzu.FileNode, symbols []kuzu.SymbolNode, declares []kuzu.EdgeDeclares, calls []kuzu.EdgeCalls, err error) {
	lang := string(chunking.DetectLanguage(absPath))
	relPath := idx.relPath(absPath)

	fileNode := &kuzu.FileNode{Path: relPath, Lang: lang}

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
		symbols = append(symbols, sym)
		declares = append(declares, kuzu.EdgeDeclares{FilePath: relPath, SymbolFQN: fqn})
	}

	// Collect CALLS queries.
	if strings.HasSuffix(absPath, ".go") {
		nodes, edges, nodeErr := collectGoCallQueries(idx, relPath, source, chunks)
		if nodeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: call extraction failed for %s: %v\n", relPath, nodeErr)
		} else {
			symbols = append(symbols, nodes...)
			calls = append(calls, edges...)
		}
	} else {
		nodes, edges, nodeErr := collectTSCallQueries(idx, absPath, relPath, source, chunks)
		if nodeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: ts call extraction failed for %s: %v\n", relPath, nodeErr)
		} else {
			symbols = append(symbols, nodes...)
			calls = append(calls, edges...)
		}
	}

	return fileNode, symbols, declares, calls, nil
}

// IndexDir recursively indexes all supported source files under root.
// It processes files concurrently using a worker pool of runtime.NumCPU() goroutines.
// AST Parsing is highly parallelized, but database write execution is done together in one massive transaction at the end.
func (idx *Indexer) IndexDir(root string) error {
	type job struct{ path, src string }
	jobs := make(chan job, 64)
	type docResult struct {
		file     *kuzu.FileNode
		symbols  []kuzu.SymbolNode
		declares []kuzu.EdgeDeclares
		calls    []kuzu.EdgeCalls
	}
	docChan := make(chan docResult, 64)

	g, ctx := errgroup.WithContext(context.Background())

	for range runtime.NumCPU() {
		g.Go(func() error {
			for j := range jobs {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					f, syms, decls, calls, err := idx.indexFileOnConn(j.path, j.src)
					if err != nil {
						return err
					}
					select {
					case docChan <- docResult{f, syms, decls, calls}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			return nil
		})
	}

	// Goroutine to collect all generated nodes and edges.
	var allFiles []kuzu.FileNode
	var allSymbols []kuzu.SymbolNode
	var allDeclares []kuzu.EdgeDeclares
	var allCalls []kuzu.EdgeCalls
	docDone := make(chan struct{})

	go func() {
		for res := range docChan {
			if res.file != nil {
				allFiles = append(allFiles, *res.file)
			}
			allSymbols = append(allSymbols, res.symbols...)
			allDeclares = append(allDeclares, res.declares...)
			allCalls = append(allCalls, res.calls...)
		}
		close(docDone)
	}()

	g.Go(func() error {
		defer close(jobs)
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
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
			select {
			case jobs <- job{path, string(src)}:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
	})

	if err := g.Wait(); err != nil {
		return err
	}
	close(docChan)
	<-docDone

	// --- Deduplicate Data to Prevent KuzuDB "primary key / relationship exists" constraints ---
	uniqueFiles := make([]kuzu.FileNode, 0, len(allFiles))
	seenFiles := make(map[string]bool)
	for _, f := range allFiles {
		if !seenFiles[f.Path] {
			seenFiles[f.Path] = true
			uniqueFiles = append(uniqueFiles, f)
		}
	}
	allFiles = uniqueFiles

	uniqueSymbols := make([]kuzu.SymbolNode, 0, len(allSymbols))
	seenSymbols := make(map[string]bool)
	for _, sym := range allSymbols {
		if !seenSymbols[sym.FQN] {
			seenSymbols[sym.FQN] = true
			uniqueSymbols = append(uniqueSymbols, sym)
		}
	}
	allSymbols = uniqueSymbols

	uniqueDeclares := make([]kuzu.EdgeDeclares, 0, len(allDeclares))
	seenDeclares := make(map[string]bool)
	for _, d := range allDeclares {
		key := d.FilePath + "->" + d.SymbolFQN
		if !seenDeclares[key] {
			seenDeclares[key] = true
			uniqueDeclares = append(uniqueDeclares, d)
		}
	}
	allDeclares = uniqueDeclares

	uniqueCalls := make([]kuzu.EdgeCalls, 0, len(allCalls))
	seenCalls := make(map[string]bool)
	for _, c := range allCalls {
		key := c.CallerFQN + "->" + c.CalleeFQN
		// Only keep calls where BOTH endpoints exist in our symbol table.
		// Cross-package / stdlib calls would violate the FK constraint.
		if !seenCalls[key] && seenSymbols[c.CallerFQN] && seenSymbols[c.CalleeFQN] {
			seenCalls[key] = true
			uniqueCalls = append(uniqueCalls, c)
		}
	}
	allCalls = uniqueCalls

	log.Printf("[INFO] AST Indexing extracted uniquely: %d files, %d symbols, %d declares, %d calls", len(allFiles), len(allSymbols), len(allDeclares), len(allCalls))

	// Serialize writes via mutex to prevent KuzuDB concurrent transaction errors
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()
	startTx := time.Now()

	// 1. Delete ALL prior code data (CodeFile + Symbol nodes and edges).
	// Full re-index: wipe everything to avoid stale callee-stub duplicates.
	if err := kuzu.ExecTxOn(idx.db.Conn(), kuzu.DeleteAllCodeData()); err != nil {
		return fmt.Errorf("delete old data: %w", err)
	}

	// Helper to create a temp file, write data, copy to KuzuDB, and delete.
	doCopy := func(tableName string, writeFunc func(p string) error) error {
		tmp, err := os.CreateTemp("", "kuzu_"+tableName+"_*.csv")
		if err != nil {
			return err
		}
		csvPath := tmp.Name()
		tmp.Close()
		defer os.Remove(csvPath)

		if err := writeFunc(csvPath); err != nil {
			return fmt.Errorf("write %s: %w", tableName, err)
		}
		if err := kuzu.ExecCopyCSV(idx.db.Conn(), tableName, csvPath); err != nil {
			return fmt.Errorf("copy %s: %w", tableName, err)
		}
		return nil
	}

	// 2. COPY Nodes
	if len(allFiles) > 0 {
		if err := doCopy("CodeFile", func(p string) error { return kuzu.WriteFileNodesCSV(p, allFiles) }); err != nil {
			return err
		}
	}
	if len(allSymbols) > 0 {
		if err := doCopy("Symbol", func(p string) error { return kuzu.WriteSymbolNodesCSV(p, allSymbols) }); err != nil {
			return err
		}
	}

	// 3. COPY Edges
	if len(allDeclares) > 0 {
		if err := doCopy("DECLARES", func(p string) error { return kuzu.WriteDeclaresCSV(p, allDeclares) }); err != nil {
			return err
		}
	}
	if len(allCalls) > 0 {
		if err := doCopy("CALLS", func(p string) error { return kuzu.WriteCallsCSV(p, allCalls) }); err != nil {
			return err
		}
	}

	txDuration := time.Since(startTx)
	if txDuration > 100*time.Millisecond {
		log.Printf("[SLOW] IndexDir batched db write, tx_duration=%v", txDuration)
	}
	return nil
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
func (idx *Indexer) buildFQN(relPath, symbolName string) string {
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

// DetectGoModule reads the module name from go.mod in the given directory.
// Falls back to filepath.Base(dir) if go.mod is absent or unreadable.
func DetectGoModule(dir string) string {
	goModPath := filepath.Join(dir, "go.mod")
	f, err := os.Open(goModPath)
	if err != nil {
		return filepath.Base(dir)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return filepath.Base(dir)
}
