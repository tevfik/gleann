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
// module is the Go module name (see DetectGoModule), root the absolute
// codebase root used to derive relative paths, absPath the file location and
// source its content. Go declaration anchors (start/end line, start column,
// byte offset) are resolved from the AST; symbols without an AST anchor keep
// the chunker's line range with zero column/offset.
func ExtractFile(module, root, absPath, source string) FileGraph {
	return extractFile(module, root, absPath, source, nil)
}

// ExtractFileExact is ExtractFile with exact go/types resolution for files the
// resolver type-checked (WP-G3). Files absent from the resolver (type errors,
// build-tag-excluded, cgo-only, test files, non-Go) fall back to the syntactic
// resolver, so the result is always a complete FileGraph. A nil resolver is
// equivalent to ExtractFile.
func ExtractFileExact(module, root, absPath, source string, r *ExactResolver) FileGraph {
	return extractFile(module, root, absPath, source, r)
}

func extractFile(module, root, absPath, source string, r *ExactResolver) FileGraph {
	module = strings.TrimSuffix(module, "/")
	root = filepath.Clean(root)
	langEnum := chunking.DetectLanguage(absPath)
	lang := string(langEnum)
	relPath := relPathOf(root, absPath)

	chunker := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig())
	// Non-Go tree-sitter languages are parsed once for declarations and CALLS
	// (treesitter builds); everything else uses the chunker alone.
	chunks, tsNodes, tsEdges, tsOK := chunkWithCalls(chunker, module, relPath, absPath, source, langEnum)
	if !tsOK {
		chunks = chunker.ChunkCode(source, absPath)
	}

	rationaleByLine := extractRationale(source)

	var anchors map[string]goAnchor
	if langEnum == chunking.LangGo {
		anchors = goDeclAnchors(relPath, source)
	}

	fg := FileGraph{File: gtypes.FileNode{Path: relPath, Lang: lang}}
	for _, ch := range chunks {
		if ch.Name == "" || ch.NodeType == "preamble" {
			continue
		}
		fqn := buildFQNOf(module, relPath, ch.Name)
		doc := attachRationale(rationaleByLine, ch.StartLine, ch.EndLine)

		hint := ch.NodeType
		if hint == "" {
			hint = symbolHintFromKind(ch.Name, ch.NodeType)
		}
		hint = detectSourceHint(langEnum, source, ch.StartLine, hint)
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
		if a, ok := lookupAnchor(anchors, ch.Name); ok {
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
			nodes, edges := collectGoCallQueriesExact(module, relPath, source, chunks, r.files[relPath])
			fg.Symbols = append(fg.Symbols, nodes...)
			fg.Calls = append(fg.Calls, edges...)
		} else {
			nodes, edges, nodeErr := collectGoCallQueries(module, relPath, source, chunks)
			if nodeErr != nil {
				fmt.Fprintf(os.Stderr, "warn: call extraction failed for %s: %v\n", relPath, nodeErr)
			} else {
				fg.Symbols = append(fg.Symbols, nodes...)
				fg.Calls = append(fg.Calls, edges...)
			}
		}

		implEdges, refEdges, extraSyms := collectGoImplementsEdges(module, relPath, source, chunks)
		fg.Implements = append(fg.Implements, implEdges...)
		fg.References = append(fg.References, refEdges...)
		fg.Symbols = append(fg.Symbols, extraSyms...)
		// Method-set IMPLEMENTS (exact) in addition to the embedding edges.
		fg.Implements = dedupeImplements(append(fg.Implements, r.ImplementsFor(relPath)...))
	}

	if tsOK {
		for _, n := range tsNodes {
			if n.Kind == "script" {
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

// goDeclAnchors maps chunk symbol names to their declaration positions.
// Naming mirrors the chunker's chunkGo: functions as Name / Receiver.Name,
// type/var/const groups after their first spec.
func goDeclAnchors(relPath, source string) map[string]goAnchor {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, 0)
	if err != nil {
		return nil
	}
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

// lookupAnchor resolves a chunk name to its declaration anchor, falling back
// to the original declaration for chunker-split parts ("Foo_part2" → "Foo").
func lookupAnchor(anchors map[string]goAnchor, name string) (goAnchor, bool) {
	if a, ok := anchors[name]; ok {
		return a, true
	}
	if i := strings.LastIndex(name, "_part"); i > 0 {
		if a, ok := anchors[name[:i]]; ok {
			return a, true
		}
	}
	return goAnchor{}, false
}
