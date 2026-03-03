// Package analysis provides a public API for gleann's AST-aware code parsing.
//
// This package re-exports the internal chunking engine so that external modules
// (e.g. yaver-go) can use gleann's multi-language AST analysis without
// accessing internal packages.
//
// Usage:
//
//	parser := analysis.NewParser(analysis.DefaultConfig())
//	chunks := parser.Parse(source, "main.py")
//	for _, c := range chunks {
//	    fmt.Printf("%s %s (%s) lines %d-%d\n", c.NodeType, c.Name, c.Language, c.StartLine, c.EndLine)
//	}
package analysis

import (
	"github.com/tevfik/gleann-chunking"
)

// Language represents a supported programming language.
type Language = chunking.Language

// Language constants.
const (
	LangGo         = chunking.LangGo
	LangPython     = chunking.LangPython
	LangJavaScript = chunking.LangJavaScript
	LangTypeScript = chunking.LangTypeScript
	LangJava       = chunking.LangJava
	LangC          = chunking.LangC
	LangCPP        = chunking.LangCPP
	LangRust       = chunking.LangRust
	LangCSharp     = chunking.LangCSharp
	LangUnknown    = chunking.LangUnknown
)

// Symbol represents a parsed code symbol (function, class, method, etc.).
type Symbol struct {
	Name      string            `json:"name"`
	NodeType  string            `json:"node_type"` // "function", "class", "method", "interface", "struct", "block"
	Language  string            `json:"language"`
	FilePath  string            `json:"file_path"`
	StartLine int               `json:"start_line"`
	EndLine   int               `json:"end_line"`
	Text      string            `json:"text"`     // the source code of this symbol
	Scope     string            `json:"scope"`    // parent scope (e.g. "Calculator" for a method)
	Metadata  map[string]string `json:"metadata"` // extra info (parser, parent_scope, etc.)
}

// Config controls the parser behavior.
type Config struct {
	// MaxChunkSize limits the size of returned chunks (chars). Default: 3000.
	MaxChunkSize int

	// ChunkOverlap is the number of overlapping chars at boundaries. Default: 100.
	ChunkOverlap int

	// AddLineNumbers prepends line numbers to source text. Default: false.
	AddLineNumbers bool

	// ChunkExpansion prepends scope context headers (needs tree-sitter). Default: false.
	ChunkExpansion bool
}

// DefaultConfig returns sensible defaults for code analysis.
func DefaultConfig() Config {
	return Config{
		MaxChunkSize:   3000,
		ChunkOverlap:   100,
		AddLineNumbers: false,
		ChunkExpansion: false,
	}
}

// Parser provides AST-aware code parsing using gleann's chunking engine.
type Parser struct {
	chunker *chunking.ASTChunker
}

// NewParser creates a code parser with the given config.
func NewParser(cfg Config) *Parser {
	return &Parser{
		chunker: chunking.NewASTChunker(chunking.ASTChunkerConfig{
			MaxChunkSize:   cfg.MaxChunkSize,
			ChunkOverlap:   cfg.ChunkOverlap,
			AddLineNumbers: cfg.AddLineNumbers,
			ChunkExpansion: cfg.ChunkExpansion,
		}),
	}
}

// Parse splits source code into semantic symbols/chunks.
// filename is used for language detection (e.g. "main.py", "server.go").
func (p *Parser) Parse(source, filename string) []Symbol {
	chunks := p.chunker.ChunkCode(source, filename)
	lang := DetectLanguage(filename)

	symbols := make([]Symbol, 0, len(chunks))
	for _, c := range chunks {
		s := Symbol{
			Name:      c.Name,
			NodeType:  c.NodeType,
			Language:  string(lang),
			FilePath:  filename,
			StartLine: c.StartLine,
			EndLine:   c.EndLine,
			Text:      c.Text,
			Metadata:  make(map[string]string),
		}

		// Extract metadata
		if m := c.Metadata; m != nil {
			if v, ok := m["parent_scope"].(string); ok {
				s.Scope = v
			}
			if v, ok := m["parser"].(string); ok {
				s.Metadata["parser"] = v
			}
		}

		symbols = append(symbols, s)
	}

	return symbols
}

// DetectLanguage returns the language for a filename.
func DetectLanguage(filename string) Language {
	return chunking.DetectLanguage(filename)
}

// IsCodeFile returns true if the file has a recognized code extension.
func IsCodeFile(filename string) bool {
	return chunking.IsCodeSourceFile(filename)
}

// TreeSitterAvailable reports whether tree-sitter support is compiled in.
func TreeSitterAvailable() bool {
	return chunking.TreeSitterAvailable()
}

// ExtractFunctions returns only function/method symbols from a file.
func (p *Parser) ExtractFunctions(source, filename string) []Symbol {
	all := p.Parse(source, filename)
	var funcs []Symbol
	for _, s := range all {
		if s.NodeType == "function" || s.NodeType == "method" {
			funcs = append(funcs, s)
		}
	}
	return funcs
}

// ExtractClasses returns only class/struct/interface symbols from a file.
func (p *Parser) ExtractClasses(source, filename string) []Symbol {
	all := p.Parse(source, filename)
	var classes []Symbol
	for _, s := range all {
		if s.NodeType == "class" || s.NodeType == "struct" || s.NodeType == "interface" {
			classes = append(classes, s)
		}
	}
	return classes
}

// ExtractImports returns only import/preamble symbols from a file.
func (p *Parser) ExtractImports(source, filename string) []Symbol {
	all := p.Parse(source, filename)
	var imports []Symbol
	for _, s := range all {
		if s.NodeType == "preamble" || s.NodeType == "import" {
			imports = append(imports, s)
		}
	}
	return imports
}
