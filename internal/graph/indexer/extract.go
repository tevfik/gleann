package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// FileGraph is the per-file extraction result shared by the graph sinks.
// Symbols contains both declarations (File != "") and callee/type stubs
// (File == ""); Declares only references declarations. For Go files the
// CALLS / IMPLEMENTS / REFERENCES edges are AST-resolved; other tree-sitter
// languages contribute CALLS edges in treesitter builds and declarations only
// otherwise.
type FileGraph struct {
	File       gtypes.FileNode
	Symbols    []gtypes.SymbolNode
	Declares   []gtypes.EdgeDeclares
	Calls      []gtypes.EdgeCalls
	Implements []gtypes.EdgeImplements
	References []gtypes.EdgeReferences
}

// ExtractFile extracts symbols and relationships from one source file. It works
// in every build including CGO_ENABLED=0 (go/ast for Go, the chunker's regex
// fallback for other languages); with -tags treesitter the other languages are
// parsed by tree-sitter and also yield CALLS edges. A call is charged to the
// innermost function/method/class containing it; module-level calls are
// charged to a "<script>" symbol anchored at line 1 and spanning the file.
//
// module is the fallback Go module name (see DetectGoModule), used only for
// files no module of modules owns; root the absolute codebase root used to
// derive relative paths, absPath the file location and source its content. Go
// declaration anchors (start/end line, start column, byte offset) are resolved
// from the AST; symbols without an AST anchor keep the chunker's line range
// with zero column/offset.
func ExtractFile(module, root, absPath, source string) FileGraph {
	return extractFileWithModules(module, root, absPath, source, nil, nil)
}

// ExtractFileInModules is ExtractFile with the Go modules discovered under
// root: a file of a nested submodule or go.work workspace is prefixed with its
// own module path, not with the fallback module plus the nested directory.
func ExtractFileInModules(modules *ModuleIndex, module, root, absPath, source string) FileGraph {
	return extractFileWithModules(module, root, absPath, source, nil, modules)
}

// ExtractFileExact is ExtractFile with exact go/types resolution for files the
// resolver type-checked (WP-G3). Files absent from the resolver (type errors,
// build-tag-excluded, test-only packages or broken tests, non-Go) fall back to
// the syntactic resolver, so the result is always a complete FileGraph. A nil
// resolver is equivalent to ExtractFile.
func ExtractFileExact(module, root, absPath, source string, r *ExactResolver) FileGraph {
	return extractFileWithModules(module, root, absPath, source, r, r.Modules())
}

// ExtractFileExactInModules is ExtractFileExact with the module index passed
// explicitly, for a caller that resolves modules even when no exact resolver is
// available (GLEANN_GRAPH_EXACT=0).
func ExtractFileExactInModules(modules *ModuleIndex, module, root, absPath, source string, r *ExactResolver) FileGraph {
	return extractFileWithModules(module, root, absPath, source, r, modules)
}

// extractFile keeps the historical entry point for a single-module tree.
func extractFile(module, root, absPath, source string, r *ExactResolver) FileGraph {
	return extractFileWithModules(module, root, absPath, source, r, nil)
}

func extractFileWithModules(module, root, absPath, source string, r *ExactResolver, modules *ModuleIndex) FileGraph {
	module = strings.TrimSuffix(module, "/")
	root = filepath.Clean(root)
	langEnum := chunking.DetectLanguage(absPath)
	lang := string(langEnum)
	relPath := relPathOf(root, absPath)

	// fqnModule/fqnRel put the file under its real module: a file of a nested
	// submodule gets that submodule's path, and the part below its own module
	// directory, so buildFQNOf does not repeat the nested directory.
	fqnModule, fqnRel := module, relPath
	if modules != nil {
		if m, ok := modules.ModuleFor(relPath); ok {
			fqnModule, fqnRel = m.Path, m.RelPath(relPath)
		}
	}

	chunker := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig())
	// A Go file is parsed once: the chunker, the declaration anchors and the
	// syntactic CALLS / IMPLEMENTS / REFERENCES collectors all read this one
	// syntax tree (each used to parse the file again for itself).
	var syn *goSyntax
	if langEnum == chunking.LangGo {
		syn = parseGoSyntax(relPath, source)
	}
	// Non-Go tree-sitter languages are parsed once for declarations and CALLS
	// (treesitter builds); everything else uses the chunker alone.
	chunks, tsNodes, tsEdges, tsOK := chunkWithCalls(chunker, fqnModule, fqnRel, absPath, source, langEnum)
	if !tsOK {
		if syn != nil && syn.err == nil {
			chunks = chunker.ChunkGoFile(syn.fset, syn.file, source, absPath)
		} else {
			// Not Go, or Go that does not parse (the chunker then falls back
			// to its line patterns).
			chunks = chunker.ChunkCode(source, absPath)
		}
	}

	// One declaration is one symbol: the pieces of an oversized declaration
	// the chunker split for search become a single logical chunk here.
	chunks = mergeSplitChunks(chunks)

	rationaleByLine := extractRationale(source)

	var anchors map[string]goAnchor
	if syn != nil {
		anchors = goDeclAnchorsOf(syn)
	}
	// The lines of the file, split once for the source hints of all symbols.
	lines := strings.Split(source, "\n")

	fg := FileGraph{File: gtypes.FileNode{Path: relPath, Lang: lang}}
	for _, ch := range chunks {
		if ch.Name == "" || ch.NodeType == "preamble" {
			continue
		}
		fqn := buildFQNOf(fqnModule, fqnRel, ch.Name)
		doc := attachRationale(rationaleByLine, ch.StartLine, ch.EndLine)

		hint := ch.NodeType
		if hint == "" {
			hint = symbolHintFromKind(ch.Name, ch.NodeType)
		}
		hint = detectSourceHintLines(langEnum, lines, ch.StartLine, hint)
		weight := applyHeuristics(langEnum, ch.NodeType, hint)

		sym := gtypes.SymbolNode{
			FQN:     fqn,
			Kind:    ch.NodeType,
			File:    relPath,
			Line:    int64(ch.StartLine),
			EndLine: int64(ch.EndLine),
			Name:    ch.Name,
			Doc:     doc,
			Weight:  weight,
			IsTest:  isTestSymbol(relPath, ch.Name),
		}
		if a, ok := anchors[ch.Name]; ok {
			sym.Line = a.line
			sym.EndLine = a.endLine
			sym.StartCol = a.col
			sym.ByteOffset = a.offset
		}
		fg.Symbols = append(fg.Symbols, sym)
		fg.Declares = append(fg.Declares, gtypes.EdgeDeclares{FilePath: relPath, SymbolFQN: fqn})
	}

	// Collect CALLS / IMPLEMENTS / REFERENCES (Go only in the pure-Go path).
	if langEnum == chunking.LangGo {
		if r.HasFile(relPath) {
			// Exact go/types resolution for this type-checked file.
			nodes, edges := collectGoCallQueriesExact(fqnModule, fqnRel, source, chunks, r.files[relPath])
			fg.Symbols = append(fg.Symbols, nodes...)
			fg.Calls = append(fg.Calls, edges...)
		} else {
			nodes, edges, nodeErr := collectGoCallQueriesOf(syn, fqnModule, fqnRel, chunks)
			if nodeErr != nil {
				fmt.Fprintf(os.Stderr, "warn: call extraction failed for %s: %v\n", relPath, nodeErr)
			} else {
				fg.Symbols = append(fg.Symbols, nodes...)
				fg.Calls = append(fg.Calls, edges...)
			}
		}

		implEdges, refEdges, extraSyms := collectGoImplementsEdgesOf(syn, fqnModule, fqnRel, chunks)
		fg.Implements = append(fg.Implements, implEdges...)
		fg.References = append(fg.References, refEdges...)
		fg.Symbols = append(fg.Symbols, extraSyms...)
		// Method-set IMPLEMENTS (exact) in addition to the embedding edges.
		fg.Implements = dedupeImplements(append(fg.Implements, r.ImplementsFor(relPath)...))
	}

	if tsOK {
		for _, n := range tsNodes {
			// Declaration nodes (the file's synthetic <script> caller and the
			// declarations the Svelte <script> blocks contribute) get a
			// DECLARES edge; callee stubs have no file and do not.
			if n.File != "" {
				fg.Declares = append(fg.Declares, gtypes.EdgeDeclares{FilePath: relPath, SymbolFQN: n.FQN})
			}
		}
		fg.Symbols = append(fg.Symbols, tsNodes...)
		fg.Calls = append(fg.Calls, tsEdges...)
	}

	for i := range fg.Symbols {
		if !fg.Symbols[i].IsTest {
			fg.Symbols[i].IsTest = isTestSymbol(fg.Symbols[i].File, fg.Symbols[i].Name)
		}
	}
	fg.Symbols = dedupeSymbols(fg.Symbols)
	return fg
}

// goAnchor is a concrete source declaration anchor (AGENTS.md §2.2).
type goAnchor struct {
	line    int64
	endLine int64
	col     int64
	offset  int64
}

// goSyntax is one parse of a Go source file, shared by everything the
// extractor derives from the syntax tree: the chunks (chunking.ChunkGoFile),
// the declaration anchors and the syntactic CALLS / IMPLEMENTS / REFERENCES
// collectors. err is the parse error; file is then partial or nil and no
// consumer reads it.
type goSyntax struct {
	fset *token.FileSet
	file *ast.File
	err  error
}

// parseGoSyntax parses a Go file for the extractor. Comments are kept because
// the chunker starts a chunk at its doc comment; they add Doc/Comment fields
// and move no declaration position. The identifier resolution of go/parser
// (ast.Ident.Obj, ast.File.Scope) is skipped: no consumer reads it, and it
// reports no error in this mode, so the tree and the error are the ones the
// separate parses produced.
func parseGoSyntax(relPath, source string) *goSyntax {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, parser.ParseComments|parser.SkipObjectResolution)
	return &goSyntax{fset: fset, file: file, err: err}
}

// goDeclAnchors maps chunk symbol names to their declaration positions.
// Naming mirrors the chunker's chunkGo: functions as Name / Receiver.Name,
// type/var/const groups after their first spec.
func goDeclAnchors(relPath, source string) map[string]goAnchor {
	return goDeclAnchorsOf(parseGoSyntax(relPath, source))
}

// goDeclAnchorsOf is goDeclAnchors over a file that is already parsed.
func goDeclAnchorsOf(syn *goSyntax) map[string]goAnchor {
	if syn.err != nil {
		return nil
	}
	fset, file := syn.fset, syn.file
	anchors := make(map[string]goAnchor)
	put := func(name string, pos, end token.Pos) {
		if name == "" {
			return
		}
		if _, exists := anchors[name]; exists {
			return // first declaration wins
		}
		p := fset.Position(pos)
		anchors[name] = goAnchor{
			line:    int64(p.Line),
			endLine: int64(fset.Position(end).Line),
			col:     int64(p.Column),
			offset:  int64(p.Offset),
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = anchorRecvName(d.Recv.List[0].Type) + "." + name
			}
			put(name, d.Pos(), d.End())
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				if len(d.Specs) > 0 {
					if ts, ok := d.Specs[0].(*ast.TypeSpec); ok {
						put(ts.Name.Name, ts.Pos(), ts.End())
					}
				}
			case token.VAR, token.CONST:
				if len(d.Specs) > 0 {
					if vs, ok := d.Specs[0].(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						put(vs.Names[0].Name, d.Pos(), d.End())
					}
				}
			}
		}
	}
	return anchors
}

// anchorRecvName mirrors chunking.exprName so anchor keys match chunk names.
func anchorRecvName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return anchorRecvName(e.X)
	case *ast.SelectorExpr:
		return anchorRecvName(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}

// mergeSplitChunks collapses the pieces of every chunker-split declaration
// into one chunk named after the declaration (CodeChunk.LogicalName), spanning
// the pieces' lines, at the position of its first piece. Chunks that are not
// pieces pass through unchanged, so a declaration that is genuinely named
// "foo_part2" is never mistaken for a piece.
func mergeSplitChunks(chunks []chunking.CodeChunk) []chunking.CodeChunk {
	type key struct{ name, nodeType string }
	first := make(map[key]int)
	out := make([]chunking.CodeChunk, 0, len(chunks))
	for _, ch := range chunks {
		if ch.LogicalName == "" {
			out = append(out, ch)
			continue
		}
		k := key{ch.LogicalName, ch.NodeType}
		if i, ok := first[k]; ok {
			if ch.StartLine < out[i].StartLine {
				out[i].StartLine = ch.StartLine
			}
			if ch.EndLine > out[i].EndLine {
				out[i].EndLine = ch.EndLine
			}
			continue
		}
		first[k] = len(out)
		ch.Name = ch.LogicalName
		ch.LogicalName = ""
		out = append(out, ch)
	}
	return out
}
