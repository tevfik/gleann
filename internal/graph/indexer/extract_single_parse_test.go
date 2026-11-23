package indexer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// extractFileSeparateParses extracts the code graph of a Go file using the
// pre-G1 approach: separate parser.ParseFile passes for chunking, declaration
// anchors, CALLS queries, and IMPLEMENTS edges, and per-symbol strings.Split
// for source hints.
func extractFileSeparateParses(module, root, absPath, source string) FileGraph {
	langEnum := chunking.DetectLanguage(absPath)
	lang := string(langEnum)
	relPath := relPathOf(root, absPath)

	chunker := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig())

	// Separate parse 1: ASTChunker using full parser.ParseFile with comments
	fsetChunk := token.NewFileSet()
	fileChunk, errChunk := parser.ParseFile(fsetChunk, absPath, source, parser.ParseComments)
	var chunks []chunking.CodeChunk
	if errChunk != nil {
		chunks = chunker.ChunkCode(source, absPath)
	} else {
		chunks = chunker.ChunkGoFile(fsetChunk, fileChunk, source, absPath)
	}
	chunks = mergeSplitChunks(chunks)

	// Separate parse 2: declaration anchors
	fsetAnchors := token.NewFileSet()
	fileAnchors, errAnchors := parser.ParseFile(fsetAnchors, relPath, source, parser.ParseComments)
	var anchors map[string]goAnchor
	if errAnchors == nil {
		anchors = goDeclAnchorsOf(&goSyntax{fset: fsetAnchors, file: fileAnchors, err: nil})
	}

	rationaleByLine := extractRationale(source)
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
		// Pre-G1 detectSourceHint splits source on every symbol
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
		if a, ok := anchors[ch.Name]; ok {
			sym.Line = a.line
			sym.EndLine = a.endLine
			sym.StartCol = a.col
			sym.ByteOffset = a.offset
		}
		fg.Symbols = append(fg.Symbols, sym)
		fg.Declares = append(fg.Declares, gtypes.EdgeDeclares{FilePath: relPath, SymbolFQN: fqn})
	}

	// Separate parse 3: collectGoCallQueries
	fsetCalls := token.NewFileSet()
	fileCalls, errCalls := parser.ParseFile(fsetCalls, relPath, source, 0)
	synCalls := &goSyntax{fset: fsetCalls, file: fileCalls, err: errCalls}
	nodes, edges, nodeErr := collectGoCallQueriesOf(synCalls, module, relPath, chunks)
	if nodeErr == nil {
		fg.Symbols = append(fg.Symbols, nodes...)
		fg.Calls = append(fg.Calls, edges...)
	}

	// Separate parse 4: collectGoImplementsEdges
	fsetImpl := token.NewFileSet()
	fileImpl, errImpl := parser.ParseFile(fsetImpl, relPath, source, 0)
	synImpl := &goSyntax{fset: fsetImpl, file: fileImpl, err: errImpl}
	implEdges, refEdges, extraSyms := collectGoImplementsEdgesOf(synImpl, module, relPath, chunks)
	fg.Implements = append(fg.Implements, implEdges...)
	fg.References = append(fg.References, refEdges...)
	fg.Symbols = append(fg.Symbols, extraSyms...)

	for i := range fg.Symbols {
		if !fg.Symbols[i].IsTest {
			fg.Symbols[i].IsTest = isTestSymbol(fg.Symbols[i].File, fg.Symbols[i].Name)
		}
	}
	fg.Symbols = dedupeSymbols(fg.Symbols)

	return fg
}

// TestExtractGoSingleParseEqualsSeparateParses verifies that G1's single-parse
// extraction path produces identical symbols, anchors, hints, call queries,
// implements edges, and references to the pre-G1 multi-parse path.
func TestExtractGoSingleParseEqualsSeparateParses(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "functions and methods",
			source: `package sample

// Add adds two integers.
func Add(a, b int) int {
	return a + b
}

type Calculator struct {
	base int
}

// Compute executes a calculation.
func (c *Calculator) Compute(x int) int {
	return c.base + Add(x, 1)
}

func helper(val string) bool {
	return len(val) > 0
}
`,
		},
		{
			name: "interfaces and implementations",
			source: `package sample

import "io"

type Greeter interface {
	Greet(name string) string
	io.Closer
}

type EnglishGreeter struct{}

func (e EnglishGreeter) Greet(name string) string {
	return "Hello " + name
}

func (e EnglishGreeter) Close() error {
	return nil
}
`,
		},
		{
			name: "cross calls and references",
			source: `package sample

import (
	"fmt"
	"strings"
)

type Worker struct{}

func (w *Worker) Process(items []string) string {
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "item: %s\n", item)
	}
	return b.String()
}
`,
		},
		{
			name: "syntax error fallback",
			source: `package sample

func Broken( {
	missing paren
}
`,
		},
		{
			name: "empty file",
			source: `package sample
`,
		},
	}

	root := "/repo"
	module := "example.com/sample"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relPath := filepath.Join("pkg", tc.name+".go")
			absPath := filepath.Join(root, relPath)

			got := extractFile(module, root, absPath, tc.source, nil)
			want := extractFileSeparateParses(module, root, absPath, tc.source)

			if len(got.Symbols) != len(want.Symbols) {
				t.Fatalf("symbols count mismatch: got %d, want %d", len(got.Symbols), len(want.Symbols))
			}
			for i := range got.Symbols {
				if !reflect.DeepEqual(got.Symbols[i], want.Symbols[i]) {
					t.Errorf("symbol %d mismatch:\n got  %+v\n want %+v", i, got.Symbols[i], want.Symbols[i])
				}
			}

			if len(got.Calls) != len(want.Calls) {
				t.Fatalf("calls count mismatch: got %d, want %d", len(got.Calls), len(want.Calls))
			}
			for i := range got.Calls {
				if !reflect.DeepEqual(got.Calls[i], want.Calls[i]) {
					t.Errorf("call edge %d mismatch:\n got  %+v\n want %+v", i, got.Calls[i], want.Calls[i])
				}
			}

			if len(got.Implements) != len(want.Implements) {
				t.Fatalf("implements count mismatch: got %d, want %d", len(got.Implements), len(want.Implements))
			}
			for i := range got.Implements {
				if !reflect.DeepEqual(got.Implements[i], want.Implements[i]) {
					t.Errorf("implements edge %d mismatch:\n got  %+v\n want %+v", i, got.Implements[i], want.Implements[i])
				}
			}

			if len(got.References) != len(want.References) {
				t.Fatalf("references count mismatch: got %d, want %d", len(got.References), len(want.References))
			}
			for i := range got.References {
				if !reflect.DeepEqual(got.References[i], want.References[i]) {
					t.Errorf("reference edge %d mismatch:\n got  %+v\n want %+v", i, got.References[i], want.References[i])
				}
			}

			if len(got.Declares) != len(want.Declares) {
				t.Fatalf("declares count mismatch: got %d, want %d", len(got.Declares), len(want.Declares))
			}
			for i := range got.Declares {
				if !reflect.DeepEqual(got.Declares[i], want.Declares[i]) {
					t.Errorf("declares edge %d mismatch:\n got  %+v\n want %+v", i, got.Declares[i], want.Declares[i])
				}
			}
		})
	}

	// Also test real Go source files from this package
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("listing package sources: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		t.Run("pkgfile/"+f, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("reading %s: %v", f, err)
			}
			source := string(data)
			absPath := filepath.Join(root, "internal", "graph", "indexer", f)

			got := extractFile("github.com/tevfik/gleann", root, absPath, source, nil)
			want := extractFileSeparateParses("github.com/tevfik/gleann", root, absPath, source)

			if len(got.Symbols) != len(want.Symbols) {
				t.Fatalf("%s symbols count mismatch: got %d, want %d", f, len(got.Symbols), len(want.Symbols))
			}
			for i := range got.Symbols {
				if !reflect.DeepEqual(got.Symbols[i], want.Symbols[i]) {
					t.Errorf("%s symbol %d mismatch:\n got  %+v\n want %+v", f, i, got.Symbols[i], want.Symbols[i])
				}
			}
			if len(got.Calls) != len(want.Calls) {
				t.Fatalf("%s calls count mismatch: got %d, want %d", f, len(got.Calls), len(want.Calls))
			}
			for i := range got.Calls {
				if !reflect.DeepEqual(got.Calls[i], want.Calls[i]) {
					t.Errorf("%s call edge %d mismatch:\n got  %+v\n want %+v", f, i, got.Calls[i], want.Calls[i])
				}
			}
			if len(got.Implements) != len(want.Implements) {
				t.Fatalf("%s implements count mismatch: got %d, want %d", f, len(got.Implements), len(want.Implements))
			}
			for i := range got.Implements {
				if !reflect.DeepEqual(got.Implements[i], want.Implements[i]) {
					t.Errorf("%s implements edge %d mismatch:\n got  %+v\n want %+v", f, i, got.Implements[i], want.Implements[i])
				}
			}
			if len(got.References) != len(want.References) {
				t.Fatalf("%s references count mismatch: got %d, want %d", f, len(got.References), len(want.References))
			}
			for i := range got.References {
				if !reflect.DeepEqual(got.References[i], want.References[i]) {
					t.Errorf("%s reference edge %d mismatch:\n got  %+v\n want %+v", f, i, got.References[i], want.References[i])
				}
			}
			if len(got.Declares) != len(want.Declares) {
				t.Fatalf("%s declares count mismatch: got %d, want %d", f, len(got.Declares), len(want.Declares))
			}
			for i := range got.Declares {
				if !reflect.DeepEqual(got.Declares[i], want.Declares[i]) {
					t.Errorf("%s declares edge %d mismatch:\n got  %+v\n want %+v", f, i, got.Declares[i], want.Declares[i])
				}
			}
		})
	}
}
