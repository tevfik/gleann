// Package chunking provides AST-aware text chunking for code files.
// It uses Go's native go/ast parser for Go files and regex-based boundary
// detection for Python, JavaScript, TypeScript, Java, C/C++, and Rust.
//
// AST-aware chunking ensures semantic units (functions, classes, methods)
// are not split mid-definition, producing higher quality chunks for code RAG.
package chunking

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Language represents a supported programming language.
type Language string

const (
	LangGo         Language = "go"
	LangPython     Language = "python"
	LangJavaScript Language = "javascript"
	LangTypeScript Language = "typescript"
	LangJava       Language = "java"
	LangC          Language = "c"
	LangCPP        Language = "cpp"
	LangRust       Language = "rust"
	LangCSharp     Language = "csharp"
	LangRuby       Language = "ruby"
	LangPHP        Language = "php"
	LangKotlin     Language = "kotlin"
	LangScala      Language = "scala"
	LangSwift      Language = "swift"
	LangLua        Language = "lua"
	LangElixir     Language = "elixir"
	LangZig        Language = "zig"
	LangPowerShell Language = "powershell"
	LangJulia      Language = "julia"
	LangObjectiveC Language = "objc"
	LangVue        Language = "vue"
	LangSvelte     Language = "svelte"
	LangUnknown    Language = "unknown"
)

// extensionMap maps file extensions to languages.
var extensionMap = map[string]Language{
	".go":     LangGo,
	".py":     LangPython,
	".js":     LangJavaScript,
	".jsx":    LangJavaScript,
	".ts":     LangTypeScript,
	".tsx":    LangTypeScript,
	".java":   LangJava,
	".c":      LangC,
	".h":      LangC,
	".cpp":    LangCPP,
	".cc":     LangCPP,
	".cxx":    LangCPP,
	".hpp":    LangCPP,
	".rs":     LangRust,
	".cs":     LangCSharp,
	".rb":     LangRuby,
	".php":    LangPHP,
	".kt":     LangKotlin,
	".kts":    LangKotlin,
	".scala":  LangScala,
	".sc":     LangScala,
	".swift":  LangSwift,
	".lua":    LangLua,
	".ex":     LangElixir,
	".exs":    LangElixir,
	".zig":    LangZig,
	".ps1":    LangPowerShell,
	".psm1":   LangPowerShell,
	".jl":     LangJulia,
	".m":      LangObjectiveC,
	".mm":     LangObjectiveC,
	".vue":    LangVue,
	".svelte": LangSvelte,
}

// CodeChunk represents a semantic code chunk with metadata.
type CodeChunk struct {
	Text          string         `json:"text"`
	Metadata      map[string]any `json:"metadata"`
	StartLine     int            `json:"start_line"`
	EndLine       int            `json:"end_line"`
	NodeType      string         `json:"node_type"` // "function", "class", "method", "block", etc.
	Name          string         `json:"name"`      // e.g. function/class name
	OutboundCalls []string       `json:"outbound_calls,omitempty"`
}

// ASTChunkerConfig holds configuration for the AST chunker.
type ASTChunkerConfig struct {
	// MaxChunkSize is the maximum number of characters per chunk.
	MaxChunkSize int

	// ChunkOverlap is the number of overlapping characters at boundaries.
	ChunkOverlap int

	// AddLineNumbers prepends line numbers to each line of the chunk.
	AddLineNumbers bool

	// ChunkExpansion prepends parent scope context as a header comment to each chunk.
	// E.g. "// File: api.py | Scope: Calculator > add"
	// This improves embedding quality by giving LLMs semantic context.
	// Only effective when tree-sitter is enabled (-tags treesitter).
	ChunkExpansion bool
}

// DefaultASTChunkerConfig returns reasonable defaults.
func DefaultASTChunkerConfig() ASTChunkerConfig {
	return ASTChunkerConfig{
		MaxChunkSize:   1500,
		ChunkOverlap:   100,
		AddLineNumbers: true,
		ChunkExpansion: false,
	}
}

// TreeSitterAvailable reports whether tree-sitter support is compiled in.
// Use this to check at runtime: if chunking.TreeSitterAvailable() { ... }
func TreeSitterAvailable() bool {
	return treeSitterAvailable
}

// ASTChunker splits code into semantic chunks using AST analysis.
type ASTChunker struct {
	config ASTChunkerConfig
}

// NewASTChunker creates a new AST-aware chunker.
func NewASTChunker(config ASTChunkerConfig) *ASTChunker {
	return &ASTChunker{config: config}
}

// DetectLanguage determines the programming language from the filename.
func DetectLanguage(filename string) Language {
	ext := strings.ToLower(filepath.Ext(filename))
	if lang, ok := extensionMap[ext]; ok {
		return lang
	}
	return LangUnknown
}

// IsCodeSourceFile returns true if the filename has a known code extension
// with full AST/regex support (subset of IsCodeFile).
func IsCodeSourceFile(filename string) bool {
	return DetectLanguage(filename) != LangUnknown
}

// ChunkCode splits source code into semantic chunks.
// It uses Go's native AST parser for Go files, and regex-based boundary
// detection for other supported languages. For unknown languages, it
// falls back to sliding window chunking.
func (c *ASTChunker) ChunkCode(source, filename string) []CodeChunk {
	lang := DetectLanguage(filename)

	var chunks []CodeChunk
	switch lang {
	case LangGo:
		// Go always uses native go/ast (no tree-sitter needed).
		chunks = c.chunkGo(source, filename)
	case LangPython, LangJavaScript, LangTypeScript, LangJava, LangCSharp, LangC, LangCPP, LangRust, LangRuby, LangPHP, LangKotlin, LangScala, LangSwift, LangLua, LangElixir, LangZig, LangPowerShell, LangJulia, LangObjectiveC, LangVue, LangSvelte:
		// Try tree-sitter first (if compiled with -tags treesitter).
		chunks = treeSitterChunk(source, filename, lang, c.config)
		if chunks == nil {
			// Fall back to regex-based boundary detection.
			chunks = c.chunkByRegex(source, filename, lang)
		}
	default:
		chunks = c.chunkSlidingWindow(source, filename, lang)
	}

	// Post-process: split oversized chunks and add line numbers.
	var result []CodeChunk
	for _, chunk := range chunks {
		if len(chunk.Text) > c.config.MaxChunkSize {
			split := c.splitOversizedChunk(chunk)
			result = append(result, split...)
		} else {
			result = append(result, chunk)
		}
	}

	if c.config.AddLineNumbers {
		for i := range result {
			result[i].Text = addLineNumbers(result[i].Text, result[i].StartLine)
		}
	}

	return result
}

// chunkGo uses Go's native AST parser for precise function/type boundaries.
func (c *ASTChunker) chunkGo(source, filename string) []CodeChunk {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, parser.ParseComments)
	if err != nil {
		// Fall back to regex if AST parse fails.
		return c.chunkByPattern(source, filename, goPatterns(), LangGo)
	}

	var chunks []CodeChunk
	lines := strings.Split(source, "\n")

	// Extract package + imports as a preamble chunk.
	preambleEnd := 0
	if file.Name != nil {
		preambleEnd = fset.Position(file.Name.End()).Line
	}
	for _, imp := range file.Imports {
		end := fset.Position(imp.End()).Line
		if end > preambleEnd {
			preambleEnd = end
		}
	}
	// Check for import block close paren.
	for _, decl := range file.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.IMPORT {
			end := fset.Position(genDecl.End()).Line
			if end > preambleEnd {
				preambleEnd = end
			}
		}
	}

	if preambleEnd > 0 {
		text := joinLines(lines, 0, preambleEnd)
		if strings.TrimSpace(text) != "" {
			chunks = append(chunks, CodeChunk{
				Text:      text,
				StartLine: 1,
				EndLine:   preambleEnd,
				NodeType:  "preamble",
				Name:      "imports",
				Metadata: map[string]any{
					"language":  "go",
					"file_path": filename,
					"node_type": "preamble",
				},
			})
		}
	}

	// Extract each top-level declaration.
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			start := fset.Position(d.Pos()).Line
			end := fset.Position(d.End()).Line

			name := d.Name.Name
			nodeType := "function"
			if d.Recv != nil {
				nodeType = "method"
				if len(d.Recv.List) > 0 {
					recvType := exprName(d.Recv.List[0].Type)
					name = recvType + "." + name
				}
			}

			// Include preceding comment group.
			if d.Doc != nil {
				docStart := fset.Position(d.Doc.Pos()).Line
				if docStart < start {
					start = docStart
				}
			}

			text := joinLines(lines, start-1, end)
			chunks = append(chunks, CodeChunk{
				Text:      text,
				StartLine: start,
				EndLine:   end,
				NodeType:  nodeType,
				Name:      name,
				Metadata: map[string]any{
					"language":  "go",
					"file_path": filename,
					"node_type": nodeType,
					"name":      name,
				},
			})

		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue // Already in preamble.
			}
			start := fset.Position(d.Pos()).Line
			end := fset.Position(d.End()).Line

			if d.Doc != nil {
				docStart := fset.Position(d.Doc.Pos()).Line
				if docStart < start {
					start = docStart
				}
			}

			nodeType := "type"
			name := ""
			if d.Tok == token.TYPE && len(d.Specs) > 0 {
				if ts, ok := d.Specs[0].(*ast.TypeSpec); ok {
					name = ts.Name.Name
					if _, ok := ts.Type.(*ast.InterfaceType); ok {
						nodeType = "interface"
					} else if _, ok := ts.Type.(*ast.StructType); ok {
						nodeType = "struct"
					}
				}
			} else if d.Tok == token.VAR || d.Tok == token.CONST {
				nodeType = strings.ToLower(d.Tok.String())
				if len(d.Specs) > 0 {
					if vs, ok := d.Specs[0].(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						name = vs.Names[0].Name
					}
				}
			}

			text := joinLines(lines, start-1, end)
			chunks = append(chunks, CodeChunk{
				Text:      text,
				StartLine: start,
				EndLine:   end,
				NodeType:  nodeType,
				Name:      name,
				Metadata: map[string]any{
					"language":  "go",
					"file_path": filename,
					"node_type": nodeType,
					"name":      name,
				},
			})
		}
	}

	if len(chunks) == 0 {
		// No declarations found; use entire file.
		return c.chunkSlidingWindow(source, filename, LangGo)
	}

	return chunks
}

// boundaryPattern defines a regex pattern for detecting code boundaries.
type boundaryPattern struct {
	Pattern  *regexp.Regexp
	NodeType string
}

// boundary represents a detected code boundary at a specific line.
type boundary struct {
	line     int
	nodeType string
	name     string
}

// pythonPatterns returns patterns for Python code boundaries.
func pythonPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^def\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^    def\s+(\w+)`), "method"},
		{regexp.MustCompile(`(?m)^async\s+def\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^    async\s+def\s+(\w+)`), "method"},
	}
}

// jstsPatterns returns patterns for JavaScript/TypeScript boundaries.
func jstsPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^(?:export\s+)?class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?const\s+(\w+)\s*=\s*(?:async\s+)?\(`), "function"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?interface\s+(\w+)`), "interface"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?type\s+(\w+)`), "type"},
	}
}

// javaPatterns returns patterns for Java/C# boundaries.
func javaPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^(?:public|private|protected)?\s*(?:static\s+)?class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^(?:public|private|protected)?\s*(?:static\s+)?interface\s+(\w+)`), "interface"},
		{regexp.MustCompile(`(?m)^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:\w+\s+)+(\w+)\s*\(`), "method"},
	}
}

// cPatterns returns patterns for C/C++ boundaries.
func cPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^(?:class|struct)\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^(?:static\s+)?(?:inline\s+)?(?:\w+[\s*]+)+(\w+)\s*\([^)]*\)\s*\{`), "function"},
		{regexp.MustCompile(`(?m)^namespace\s+(\w+)`), "namespace"},
		{regexp.MustCompile(`(?m)^#define\s+(\w+)`), "macro"},
	}
}

// rustPatterns returns patterns for Rust boundaries.
func rustPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^pub\s+(?:async\s+)?fn\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^fn\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^(?:pub\s+)?struct\s+(\w+)`), "struct"},
		{regexp.MustCompile(`(?m)^(?:pub\s+)?enum\s+(\w+)`), "enum"},
		{regexp.MustCompile(`(?m)^(?:pub\s+)?trait\s+(\w+)`), "trait"},
		{regexp.MustCompile(`(?m)^impl(?:<[^>]+>)?\s+(\w+)`), "impl"},
		{regexp.MustCompile(`(?m)^(?:pub\s+)?mod\s+(\w+)`), "module"},
	}
}

// goPatterns returns fallback regex patterns for Go (when AST parse fails).
func goPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^func\s+\((\w+)\s+[^)]+\)\s+(\w+)\s*\(`), "method"},
		{regexp.MustCompile(`(?m)^func\s+(\w+)\s*\(`), "function"},
		{regexp.MustCompile(`(?m)^type\s+(\w+)\s+struct`), "struct"},
		{regexp.MustCompile(`(?m)^type\s+(\w+)\s+interface`), "interface"},
	}
}

// rubyElixirPatterns returns fallback regex patterns for Ruby/Elixir.
func rubyElixirPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^\s*def\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^\s*defp?\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^\s*class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^\s*module\s+(\w+)`), "module"},
		{regexp.MustCompile(`(?m)^\s*defmodule\s+(\w+)`), "module"},
		{regexp.MustCompile(`(?m)^\s*defstruct\b`), "struct"},
	}
}

// phpPatterns returns fallback regex patterns for PHP.
func phpPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^\s*(?:public|private|protected|static)?\s*function\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^\s*class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^\s*interface\s+(\w+)`), "interface"},
		{regexp.MustCompile(`(?m)^\s*trait\s+(\w+)`), "trait"},
	}
}

// swiftPatterns returns fallback regex patterns for Swift.
func swiftPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open)?\s*func\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open)?\s*class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open)?\s*struct\s+(\w+)`), "struct"},
		{regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open)?\s*protocol\s+(\w+)`), "interface"},
		{regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open)?\s*enum\s+(\w+)`), "enum"},
	}
}

// luaPatterns returns fallback regex patterns for Lua.
func luaPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^\s*function\s+(\w[\w.]*)\s*\(`), "function"},
		{regexp.MustCompile(`(?m)^\s*local\s+function\s+(\w+)\s*\(`), "function"},
		{regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*function\s*\(`), "function"},
	}
}

// powershellPatterns returns fallback regex patterns for PowerShell.
func powershellPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?mi)^\s*function\s+(\w[\w-]*)`), "function"},
		{regexp.MustCompile(`(?mi)^\s*class\s+(\w+)`), "class"},
		{regexp.MustCompile(`(?mi)^\s*filter\s+(\w+)`), "function"},
	}
}

// juliaPatterns returns fallback regex patterns for Julia.
func juliaPatterns() []boundaryPattern {
	return []boundaryPattern{
		{regexp.MustCompile(`(?m)^\s*function\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^\s*macro\s+(\w+)`), "function"},
		{regexp.MustCompile(`(?m)^\s*(?:mutable\s+)?struct\s+(\w+)`), "struct"},
		{regexp.MustCompile(`(?m)^\s*abstract\s+type\s+(\w+)`), "type"},
		{regexp.MustCompile(`(?m)^\s*module\s+(\w+)`), "module"},
	}
}

// chunkByRegex dispatches to the appropriate regex patterns for a language.
// Used as fallback when tree-sitter is not available.
func (c *ASTChunker) chunkByRegex(source, filename string, lang Language) []CodeChunk {
	switch lang {
	case LangPython:
		return c.chunkByPattern(source, filename, pythonPatterns(), lang)
	case LangJavaScript, LangTypeScript, LangVue, LangSvelte:
		return c.chunkByPattern(source, filename, jstsPatterns(), lang)
	case LangJava, LangCSharp, LangKotlin, LangScala:
		return c.chunkByPattern(source, filename, javaPatterns(), lang)
	case LangC, LangCPP, LangObjectiveC:
		return c.chunkByPattern(source, filename, cPatterns(), lang)
	case LangRust, LangZig:
		return c.chunkByPattern(source, filename, rustPatterns(), lang)
	case LangRuby, LangElixir:
		return c.chunkByPattern(source, filename, rubyElixirPatterns(), lang)
	case LangPHP:
		return c.chunkByPattern(source, filename, phpPatterns(), lang)
	case LangSwift:
		return c.chunkByPattern(source, filename, swiftPatterns(), lang)
	case LangLua:
		return c.chunkByPattern(source, filename, luaPatterns(), lang)
	case LangPowerShell:
		return c.chunkByPattern(source, filename, powershellPatterns(), lang)
	case LangJulia:
		return c.chunkByPattern(source, filename, juliaPatterns(), lang)
	default:
		return c.chunkSlidingWindow(source, filename, lang)
	}
}

// chunkByPattern splits source code using regex boundary patterns.
// It finds all boundary locations, then groups lines between boundaries
// into semantic chunks.
func (c *ASTChunker) chunkByPattern(source, filename string, patterns []boundaryPattern, lang Language) []CodeChunk {
	lines := strings.Split(source, "\n")
	if len(lines) == 0 {
		return nil
	}

	// Find all boundary lines.
	var boundaries []boundary

	for _, p := range patterns {
		matches := p.Pattern.FindAllStringSubmatchIndex(source, -1)
		for _, match := range matches {
			// Convert byte offset to line number.
			lineNum := strings.Count(source[:match[0]], "\n")
			name := ""
			if len(match) >= 4 {
				name = source[match[2]:match[3]]
			}
			boundaries = append(boundaries, boundary{
				line:     lineNum,
				nodeType: p.NodeType,
				name:     name,
			})
		}
	}

	if len(boundaries) == 0 {
		return c.chunkSlidingWindow(source, filename, lang)
	}

	// Sort boundaries by line number.
	sortBoundariesSlice(boundaries)

	// Remove duplicate lines (multiple patterns matching same line).
	deduped := []boundary{boundaries[0]}
	for i := 1; i < len(boundaries); i++ {
		if boundaries[i].line != boundaries[i-1].line {
			deduped = append(deduped, boundaries[i])
		}
	}
	boundaries = deduped

	var chunks []CodeChunk

	// Preamble before first boundary.
	if boundaries[0].line > 0 {
		text := joinLines(lines, 0, boundaries[0].line)
		if strings.TrimSpace(text) != "" {
			chunks = append(chunks, CodeChunk{
				Text:      text,
				StartLine: 1,
				EndLine:   boundaries[0].line,
				NodeType:  "preamble",
				Name:      "imports",
				Metadata: map[string]any{
					"language":  string(lang),
					"file_path": filename,
					"node_type": "preamble",
				},
			})
		}
	}

	// Each boundary to the next.
	for i, b := range boundaries {
		startLine := b.line
		var endLine int
		if i+1 < len(boundaries) {
			endLine = boundaries[i+1].line
		} else {
			endLine = len(lines)
		}

		text := joinLines(lines, startLine, endLine)
		if strings.TrimSpace(text) == "" {
			continue
		}

		chunks = append(chunks, CodeChunk{
			Text:      text,
			StartLine: startLine + 1,
			EndLine:   endLine,
			NodeType:  b.nodeType,
			Name:      b.name,
			Metadata: map[string]any{
				"language":  string(lang),
				"file_path": filename,
				"node_type": b.nodeType,
				"name":      b.name,
			},
		})
	}

	return chunks
}

// chunkSlidingWindow provides fallback chunking with a sliding window.
func (c *ASTChunker) chunkSlidingWindow(source, filename string, lang Language) []CodeChunk {
	lines := strings.Split(source, "\n")
	if len(lines) == 0 {
		return nil
	}

	maxLines := c.config.MaxChunkSize / 40 // ~40 chars per line estimate
	if maxLines < 10 {
		maxLines = 10
	}
	overlapLines := c.config.ChunkOverlap / 40
	if overlapLines < 2 {
		overlapLines = 2
	}

	var chunks []CodeChunk
	for start := 0; start < len(lines); {
		end := start + maxLines
		if end > len(lines) {
			end = len(lines)
		}

		text := joinLines(lines, start, end)
		if strings.TrimSpace(text) != "" {
			chunks = append(chunks, CodeChunk{
				Text:      text,
				StartLine: start + 1,
				EndLine:   end,
				NodeType:  "block",
				Name:      fmt.Sprintf("block_%d", start+1),
				Metadata: map[string]any{
					"language":  string(lang),
					"file_path": filename,
					"node_type": "block",
				},
			})
		}

		step := maxLines - overlapLines
		if step < 1 {
			step = 1
		}
		start += step
	}

	return chunks
}

// splitOversizedChunk splits a chunk that exceeds MaxChunkSize into smaller pieces.
func (c *ASTChunker) splitOversizedChunk(chunk CodeChunk) []CodeChunk {
	lines := strings.Split(chunk.Text, "\n")
	maxLines := c.config.MaxChunkSize / 40
	if maxLines < 10 {
		maxLines = 10
	}

	var result []CodeChunk
	for start := 0; start < len(lines); {
		end := start + maxLines
		if end > len(lines) {
			end = len(lines)
		}

		text := strings.Join(lines[start:end], "\n")
		if strings.TrimSpace(text) != "" {
			partNum := len(result) + 1
			result = append(result, CodeChunk{
				Text:      text,
				StartLine: chunk.StartLine + start,
				EndLine:   chunk.StartLine + end - 1,
				NodeType:  chunk.NodeType,
				Name:      fmt.Sprintf("%s_part%d", chunk.Name, partNum),
				Metadata:  copyMetadata(chunk.Metadata),
			})
		}

		start = end
	}

	return result
}

// Chunk implements the gleann Chunker interface — splits text with no metadata.
func (c *ASTChunker) Chunk(text string) []string {
	chunks := c.ChunkCode(text, "unknown.txt")
	result := make([]string, len(chunks))
	for i, ch := range chunks {
		result[i] = ch.Text
	}
	return result
}

// ChunkWithMetadata implements the gleann Chunker interface with metadata.
func (c *ASTChunker) ChunkWithMetadata(text string, metadata map[string]any) []Chunk {
	filename := "unknown.txt"
	if fp, ok := metadata["file_path"].(string); ok {
		filename = fp
	} else if src, ok := metadata["source"].(string); ok {
		filename = src
	}

	chunks := c.ChunkCode(text, filename)
	items := make([]Chunk, len(chunks))
	for i, ch := range chunks {
		merged := copyMetadata(metadata)
		for k, v := range ch.Metadata {
			merged[k] = v
		}
		merged["start_line"] = ch.StartLine
		merged["end_line"] = ch.EndLine
		merged["node_type"] = ch.NodeType
		if ch.Name != "" {
			merged["name"] = ch.Name
		}
		if len(ch.OutboundCalls) > 0 {
			merged["outbound_calls"] = ch.OutboundCalls
		}
		items[i] = Chunk{
			Text:     ch.Text,
			Metadata: merged,
		}
	}
	return items
}

// --- Helpers ---

// joinLines joins lines[start:end] with newline.
func joinLines(lines []string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

// addLineNumbers prepends line numbers to each line.
func addLineNumbers(text string, startLine int) string {
	lines := strings.Split(text, "\n")
	lastLine := startLine + len(lines) - 1
	width := len(fmt.Sprintf("%d", lastLine))

	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "%*d|%s", width, startLine+i, line)
	}
	return sb.String()
}

// exprName extracts the type name from a Go AST expression.
func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return exprName(e.X)
	case *ast.SelectorExpr:
		return exprName(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}

// sortBoundariesSlice sorts a slice of boundaries by line number.
func sortBoundariesSlice(boundaries []boundary) {
	sort.Slice(boundaries, func(i, j int) bool {
		return boundaries[i].line < boundaries[j].line
	})
}

// copyMetadata makes a shallow copy of a metadata map.
func copyMetadata(m map[string]any) map[string]any {
	if m == nil {
		return make(map[string]any)
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
