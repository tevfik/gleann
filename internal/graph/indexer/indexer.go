//go:build treesitter

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

	sitter "github.com/smacker/go-tree-sitter"
	"golang.org/x/sync/errgroup"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/modules/chunking"
)

// Indexer walks a codebase and populates a KuzuDB graph with AST relationships.
type Indexer struct {
	db        *kuzu.DB
	chunker   *chunking.ASTChunker
	module    string     // Go module prefix, e.g. "github.com/tevfik/gleann"
	root      string     // absolute root path used to derive relative package paths
	writeMu   sync.Mutex // Ensures only one KuzuDB write transaction occurs at a time
	hashStore *FileHashStore // optional: persists per-file content hashes for incremental skip
	tracker   *ChangeTracker // tracks file mtimes for incremental indexing
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
		tracker: NewChangeTracker(),
	}
}

// WithHashStore attaches a FileHashStore for incremental skip.
// Returns the indexer for chaining. A nil store is a no-op.
func (idx *Indexer) WithHashStore(store *FileHashStore) *Indexer {
	idx.hashStore = store
	return idx
}

// CloseHashStore closes the attached hash store, if any.
func (idx *Indexer) CloseHashStore() error {
	if idx.hashStore == nil {
		return nil
	}
	return idx.hashStore.Close()
}

// IndexFile parses one source file and writes its symbols and edges into KuzuDB
// using a single transaction.
func (idx *Indexer) IndexFile(absPath, source string) error {
	f, syms, decls, calls, impls, refs, err := idx.indexFileOnConn(absPath, source)
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

	// (The rest of the doCopy calls stay the same)
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
	if len(impls) > 0 {
		if err := doCopy("IMPLEMENTS", func(p string) error { return kuzu.WriteImplementsCSV(p, impls) }); err != nil {
			return err
		}
	}
	if len(refs) > 0 {
		if err := doCopy("REFERENCES", func(p string) error { return kuzu.WriteReferencesCSV(p, refs) }); err != nil {
			return err
		}
	}

	// Mark file as clean after successful indexing
	idx.tracker.MarkClean(absPath)

	return nil
}

// indexFileOnConn is the core implementation for parallel parsing.
// It extracts all File, Symbol, Declares and Calls structs and returns them.
// It also extracts rationale comments (WHY, NOTE, HACK, TODO, IMPORTANT, FIXME)
// and attaches them to nearby symbols' Doc field for design-rationale knowledge.
//
// Performance: for non-Go languages, the source is parsed exactly once with
// tree-sitter and the resulting *sitter.Tree is reused for both symbol
// extraction (via the chunker) and CALLS extraction (via the S-expression
// query). For Go, the native go/ast path is used (parse-once inside the
// chunker) and collectGoCallQueries / collectGoImplementsEdges also re-use
// that same AST node.
func (idx *Indexer) indexFileOnConn(absPath, source string) (file *kuzu.FileNode, symbols []kuzu.SymbolNode, declares []kuzu.EdgeDeclares, calls []kuzu.EdgeCalls, impls []kuzu.EdgeImplements, refs []kuzu.EdgeReferences, err error) {
	langEnum := chunking.DetectLanguage(absPath)
	lang := string(langEnum)
	relPath := idx.relPath(absPath)

	fileNode := &kuzu.FileNode{Path: relPath, Lang: lang}

	// For non-Go languages, acquire the tree-sitter parser + tree ONCE
	// and reuse it for both symbol and call extraction. For Go, the chunker
	// uses go/ast internally and the call/implements extractors share that
	// AST; no tree-sitter parse is needed.
	var tsTree *sitter.Tree
	var tsSource []byte
	if langEnum != chunking.LangGo && chunking.IsTreeSitterLanguage(langEnum) {
		parser, tree, src, ok := chunking.ParseTree(langEnum, source)
		if !ok {
			// Fall through to the legacy path: chunker.ChunkCode will try
			// tree-sitter itself, but the call extractor will see no tree.
			log.Printf("debug: tree-sitter parse unavailable for %s; relying on chunker fallback", absPath)
		} else {
			defer chunking.ReturnParser(langEnum, parser)
			tsTree = tree
			tsSource = src
		}
	}

	// Symbol extraction. For non-Go we wrap the chunker call to feed it
	// the same tree we already parsed; the chunker will recognise the
	// pre-parsed tree via the package-internal entry point.
	var chunks []chunking.CodeChunk
	if tsTree != nil {
		chunks = chunking.ChunkCodeWithTree(langEnum, absPath, source, tsTree, tsSource)
		if chunks == nil {
			chunks = idx.chunker.ChunkCode(source, absPath) // fallback
		}
	} else {
		chunks = idx.chunker.ChunkCode(source, absPath)
	}

	// Extract rationale comments from source (WHY, NOTE, HACK, TODO, IMPORTANT, FIXME).
	rationaleByLine := extractRationale(source)

	for _, ch := range chunks {
		if ch.Name == "" || ch.NodeType == "preamble" {
			continue
		}
		fqn := idx.buildFQN(relPath, ch.Name)

		// Attach rationale comments that fall within or just before (within 3 lines) this symbol's range.
		doc := attachRationale(rationaleByLine, ch.StartLine, ch.EndLine)

		// Language-specific weight: interfaces, decorated functions, and
		// traits get a higher score; ordinary helpers stay at 1.0.
		// The hint is a derived signal: NodeType is the primary key,
		// but the chunker collapses "decorated_definition" wrappers in
		// Python into their inner "function_definition"/"class_definition",
		// so we also probe the source bytes for "@" and language-specific
		// visibility markers ("pub ", "abstract ", "export ") that would
		// otherwise be lost in the symbol-only path.
		hint := ch.NodeType
		if hint == "" {
			hint = symbolHintFromKind(ch.Name, ch.NodeType)
		}
		hint = detectSourceHint(langEnum, source, ch.StartLine, hint)
		weight := applyHeuristics(langEnum, ch.NodeType, hint)

		sym := kuzu.SymbolNode{
			FQN:    fqn,
			Kind:   ch.NodeType,
			File:   relPath,
			Line:   int64(ch.StartLine),
			Name:   ch.Name,
			Doc:    doc,
			Weight: weight,
		}
		symbols = append(symbols, sym)
		declares = append(declares, kuzu.EdgeDeclares{FilePath: relPath, SymbolFQN: fqn})
	}

	// Collect CALLS / IMPLEMENTS / REFERENCES.
	if langEnum == chunking.LangGo {
		nodes, edges, nodeErr := collectGoCallQueries(idx, relPath, source, chunks)
		if nodeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: call extraction failed for %s: %v\n", relPath, nodeErr)
		} else {
			symbols = append(symbols, nodes...)
			calls = append(calls, edges...)
		}

		implEdges, refEdges, extraSyms := collectGoImplementsEdges(idx, relPath, source, chunks)
		impls = append(impls, implEdges...)
		refs = append(refs, refEdges...)
		symbols = append(symbols, extraSyms...)
	} else if tsTree != nil {
		nodes, edges, nodeErr := collectTSCallQueries(idx, langEnum, relPath, tsTree, tsSource, chunks)
		if nodeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: ts call extraction failed for %s: %v\n", relPath, nodeErr)
		} else {
			symbols = append(symbols, nodes...)
			calls = append(calls, edges...)
		}
	}

	return fileNode, symbols, declares, calls, impls, refs, nil
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
		impls    []kuzu.EdgeImplements
		refs     []kuzu.EdgeReferences
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
					f, syms, decls, calls, impls, refs, err := idx.indexFileOnConn(j.path, j.src)
					if err != nil {
						return err
					}
					select {
					case docChan <- docResult{f, syms, decls, calls, impls, refs}:
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
	var allImpls []kuzu.EdgeImplements
	var allRefs []kuzu.EdgeReferences
	docDone := make(chan struct{})

	go func() {
		for res := range docChan {
			if res.file != nil {
				allFiles = append(allFiles, *res.file)
			}
			allSymbols = append(allSymbols, res.symbols...)
			allDeclares = append(allDeclares, res.declares...)
			allCalls = append(allCalls, res.calls...)
			allImpls = append(allImpls, res.impls...)
			allRefs = append(allRefs, res.refs...)
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

			// Incremental check: skip if file hasn't changed since last index.
			if !idx.tracker.IsDirty(path) {
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

	gerr := g.Wait()
	close(docChan)
	<-docDone

	if gerr != nil {
		return gerr
	}

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

	// Deduplicate IMPLEMENTS edges (both endpoints must exist).
	uniqueImpls := make([]kuzu.EdgeImplements, 0, len(allImpls))
	seenImpls := make(map[string]bool)
	for _, i := range allImpls {
		key := i.ImplFQN + "->" + i.IfaceFQN
		if !seenImpls[key] && seenSymbols[i.ImplFQN] && seenSymbols[i.IfaceFQN] {
			seenImpls[key] = true
			uniqueImpls = append(uniqueImpls, i)
		}
	}
	allImpls = uniqueImpls

	// Deduplicate REFERENCES edges (both endpoints must exist).
	uniqueRefs := make([]kuzu.EdgeReferences, 0, len(allRefs))
	seenRefs := make(map[string]bool)
	for _, r := range allRefs {
		key := r.RefererFQN + "->" + r.RefereeFQN
		if !seenRefs[key] && seenSymbols[r.RefererFQN] && seenSymbols[r.RefereeFQN] {
			seenRefs[key] = true
			uniqueRefs = append(uniqueRefs, r)
		}
	}
	allRefs = uniqueRefs

	log.Printf("[INFO] AST Indexing extracted uniquely: %d files, %d symbols, %d declares, %d calls, %d implements, %d references", len(allFiles), len(allSymbols), len(allDeclares), len(allCalls), len(allImpls), len(allRefs))

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
	if len(allImpls) > 0 {
		if err := doCopy("IMPLEMENTS", func(p string) error { return kuzu.WriteImplementsCSV(p, allImpls) }); err != nil {
			return err
		}
	}
	if len(allRefs) > 0 {
		if err := doCopy("REFERENCES", func(p string) error { return kuzu.WriteReferencesCSV(p, allRefs) }); err != nil {
			return err
		}
	}

	txDuration := time.Since(startTx)
	if txDuration > 100*time.Millisecond {
		log.Printf("[SLOW] IndexDir batched db write, tx_duration=%v", txDuration)
	}

	// Full re-index wipes ALL code data; the hash store must be cleared
	// in lockstep so the next incremental run starts from a clean slate.
	if idx.hashStore != nil {
		if err := idx.hashStore.Clear(); err != nil {
			log.Printf("warning: FileHashStore.Clear after IndexDir: %v", err)
		}
	}
	return nil
}

// IndexFiles incrementally re-indexes only the given files. For each file it:
//  1. Consults the FileHashStore (if attached) to skip files whose content
//     has not changed since the last successful write.
//  2. Deletes the old CodeFile + Symbol nodes (and their edges) from KuzuDB.
//  3. Re-parses the file's AST and writes fresh nodes + edges.
//  4. Records the new content hash in the FileHashStore.
//
// This is much faster than IndexDir for large codebases where only a few
// files changed. The caller is responsible for determining which files
// changed (e.g. via vault tracker hashes); IndexFiles then narrows that
// list further by checking actual on-disk content hashes.
func (idx *Indexer) IndexFiles(files []string) error {
	if len(files) == 0 {
		return nil
	}

	// Pre-filter: drop files whose on-disk content hash matches the
	// persisted record. This is the single biggest perf win — an
	// editor save that didn't change anything (whitespace, comment
	// only, no-op touch) becomes a 64KB read + SHA-256 instead of
	// a full parse + DB write.
	type pending struct {
		absPath string
		relPath string
		hash    string
	}
	var toReindex []pending
	skipped := 0
	for _, absPath := range files {
		relPath, err := filepath.Rel(idx.root, absPath)
		if err != nil {
			relPath = absPath
		}
		if idx.hashStore != nil {
			dirty, currentHash, _ := idx.hashStore.IsDirty(relPath, absPath)
			if !dirty {
				skipped++
				continue
			}
			// File missing on disk → drop its stale record and skip.
			if currentHash == "" {
				_ = idx.hashStore.Remove(relPath)
				skipped++
				continue
			}
			toReindex = append(toReindex, pending{absPath, relPath, currentHash})
		} else {
			toReindex = append(toReindex, pending{absPath, relPath, ""})
		}
	}
	if skipped > 0 {
		log.Printf("[INFO] Incremental graph: skipped %d unchanged files (hash match)", skipped)
	}
	if len(toReindex) == 0 {
		return nil
	}

	// Delta step: remove old symbols for changed files before re-indexing.
	for _, p := range toReindex {
		if err := idx.db.RemoveFileSymbols(p.relPath); err != nil {
			log.Printf("warning: RemoveFileSymbols(%s): %v", p.relPath, err)
		}
	}

	type docResult struct {
		file     *kuzu.FileNode
		symbols  []kuzu.SymbolNode
		declares []kuzu.EdgeDeclares
		calls    []kuzu.EdgeCalls
		impls    []kuzu.EdgeImplements
		refs     []kuzu.EdgeReferences
	}

	// Parse all files concurrently.
	results := make([]docResult, len(toReindex))
	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(runtime.NumCPU())

	for i, p := range toReindex {
		i, p := i, p
		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			src, err := os.ReadFile(p.absPath)
			if err != nil {
				return fmt.Errorf("read %s: %w", p.absPath, err)
			}
			f, syms, decls, calls, impls, refs, err := idx.indexFileOnConn(p.absPath, string(src))
			if err != nil {
				return err
			}
			results[i] = docResult{f, syms, decls, calls, impls, refs}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Merge and deduplicate.
	var allFiles []kuzu.FileNode
	var allSymbols []kuzu.SymbolNode
	var allDeclares []kuzu.EdgeDeclares
	var allCalls []kuzu.EdgeCalls
	var allImpls []kuzu.EdgeImplements
	var allRefs []kuzu.EdgeReferences
	for _, res := range results {
		if res.file != nil {
			allFiles = append(allFiles, *res.file)
		}
		allSymbols = append(allSymbols, res.symbols...)
		allDeclares = append(allDeclares, res.declares...)
		allCalls = append(allCalls, res.calls...)
		allImpls = append(allImpls, res.impls...)
		allRefs = append(allRefs, res.refs...)
	}

	// Deduplicate symbols.
	seenSymbols := make(map[string]bool, len(allSymbols))
	uniqueSymbols := make([]kuzu.SymbolNode, 0, len(allSymbols))
	for _, sym := range allSymbols {
		if !seenSymbols[sym.FQN] {
			seenSymbols[sym.FQN] = true
			uniqueSymbols = append(uniqueSymbols, sym)
		}
	}
	allSymbols = uniqueSymbols

	// Deduplicate declares.
	seenDeclares := make(map[string]bool, len(allDeclares))
	uniqueDeclares := make([]kuzu.EdgeDeclares, 0, len(allDeclares))
	for _, d := range allDeclares {
		key := d.FilePath + "->" + d.SymbolFQN
		if !seenDeclares[key] {
			seenDeclares[key] = true
			uniqueDeclares = append(uniqueDeclares, d)
		}
	}
	allDeclares = uniqueDeclares

	// Deduplicate calls — only keep calls where BOTH endpoints exist.
	seenCalls := make(map[string]bool, len(allCalls))
	uniqueCalls := make([]kuzu.EdgeCalls, 0, len(allCalls))
	for _, c := range allCalls {
		key := c.CallerFQN + "->" + c.CalleeFQN
		if !seenCalls[key] && seenSymbols[c.CallerFQN] && seenSymbols[c.CalleeFQN] {
			seenCalls[key] = true
			uniqueCalls = append(uniqueCalls, c)
		}
	}
	allCalls = uniqueCalls

	// Deduplicate IMPLEMENTS edges.
	seenImpls := make(map[string]bool, len(allImpls))
	uniqueImpls := make([]kuzu.EdgeImplements, 0, len(allImpls))
	for _, i := range allImpls {
		key := i.ImplFQN + "->" + i.IfaceFQN
		if !seenImpls[key] && seenSymbols[i.ImplFQN] && seenSymbols[i.IfaceFQN] {
			seenImpls[key] = true
			uniqueImpls = append(uniqueImpls, i)
		}
	}
	allImpls = uniqueImpls

	// Deduplicate REFERENCES edges.
	seenRefs := make(map[string]bool, len(allRefs))
	uniqueRefs := make([]kuzu.EdgeReferences, 0, len(allRefs))
	for _, r := range allRefs {
		key := r.RefererFQN + "->" + r.RefereeFQN
		if !seenRefs[key] && seenSymbols[r.RefererFQN] && seenSymbols[r.RefereeFQN] {
			seenRefs[key] = true
			uniqueRefs = append(uniqueRefs, r)
		}
	}
	allRefs = uniqueRefs

	log.Printf("[INFO] Incremental graph: re-indexing %d files (%d symbols, %d declares, %d calls, %d implements, %d references)",
		len(allFiles), len(allSymbols), len(allDeclares), len(allCalls), len(allImpls), len(allRefs))

	// Write to KuzuDB: delete old data per file, then insert new.
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()
	startTx := time.Now()

	// 1. Delete old data for each changed file.
	var deleteQueries []string
	for _, f := range allFiles {
		deleteQueries = append(deleteQueries, kuzu.DeleteFileQueries(f.Path)...)
	}
	if err := kuzu.ExecTxOn(idx.db.Conn(), deleteQueries); err != nil {
		return fmt.Errorf("delete old file data: %w", err)
	}

	// 2. COPY new data.
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
	if len(allImpls) > 0 {
		if err := doCopy("IMPLEMENTS", func(p string) error { return kuzu.WriteImplementsCSV(p, allImpls) }); err != nil {
			return err
		}
	}
	if len(allRefs) > 0 {
		if err := doCopy("REFERENCES", func(p string) error { return kuzu.WriteReferencesCSV(p, allRefs) }); err != nil {
			return err
		}
	}

	txDuration := time.Since(startTx)
	log.Printf("[INFO] Incremental graph write complete: %d files in %v", len(allFiles), txDuration)

	// Record the new content hashes so the next incremental run can skip
	// these files. Done after a successful tx so a write failure doesn't
	// leave a stale "indexed" record that would mask a real change.
	if idx.hashStore != nil {
		symbolCountByFile := make(map[string]int, len(allFiles))
		for _, s := range allSymbols {
			symbolCountByFile[s.File]++
		}
		for _, p := range toReindex {
			info, statErr := os.Stat(p.absPath)
			size := int64(0)
			if statErr == nil {
				size = info.Size()
			}
			lang := ""
			if p.relPath != "" {
				lang = string(chunking.DetectLanguage(p.absPath))
			}
			if err := idx.hashStore.Mark(p.relPath, p.hash, lang, size, symbolCountByFile[p.relPath]); err != nil {
				log.Printf("warning: FileHashStore.Mark(%s): %v", p.relPath, err)
			}
		}
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

// rationaleComment represents a design rationale comment extracted from source code.
type rationaleComment struct {
	Line    int
	Tag     string // WHY, NOTE, HACK, TODO, IMPORTANT, FIXME
	Content string
}

// rationaleTagPrefixes are the comment tags that indicate design rationale.
var rationaleTagPrefixes = []string{
	"WHY:", "WHY ", "NOTE:", "NOTE ", "HACK:", "HACK ",
	"TODO:", "TODO ", "IMPORTANT:", "IMPORTANT ",
	"FIXME:", "FIXME ", "BUG:", "BUG ",
}

// extractRationale scans source code for rationale comments and returns them keyed by line number.
func extractRationale(source string) map[int]rationaleComment {
	result := make(map[int]rationaleComment)
	lines := strings.Split(source, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Strip comment prefix (// or # or /* or --)
		comment := ""
		if strings.HasPrefix(trimmed, "//") {
			comment = strings.TrimSpace(trimmed[2:])
		} else if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			comment = strings.TrimSpace(trimmed[1:])
		} else if strings.HasPrefix(trimmed, "--") {
			comment = strings.TrimSpace(trimmed[2:])
		}

		if comment == "" {
			continue
		}

		upper := strings.ToUpper(comment)
		for _, prefix := range rationaleTagPrefixes {
			if strings.HasPrefix(upper, prefix) {
				tag := strings.TrimRight(prefix, ": ")
				content := strings.TrimSpace(comment[len(prefix):])
				if len(content) > 0 {
					// Also try extracting from original case
					if len(comment) > len(prefix) {
						content = strings.TrimSpace(comment[len(prefix):])
					}
				}
				result[i+1] = rationaleComment{
					Line:    i + 1,
					Tag:     tag,
					Content: content,
				}
				break
			}
		}
	}
	return result
}

// attachRationale collects rationale comments within or near a symbol's line range
// and returns them as a combined doc string.
func attachRationale(rationale map[int]rationaleComment, startLine, endLine int) string {
	if len(rationale) == 0 {
		return ""
	}

	var parts []string
	// Check 3 lines before the symbol (preamble comments) through end of symbol
	for line := startLine - 3; line <= endLine; line++ {
		if r, ok := rationale[line]; ok {
			parts = append(parts, fmt.Sprintf("[%s] %s", r.Tag, r.Content))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}
