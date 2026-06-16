//go:build cgo && treesitter

// Tree-sitter based AST chunking for non-Go languages.
// Provides precise AST parsing for Python, JavaScript, TypeScript,
// Java, C, C++, Rust, and C# using tree-sitter grammars.
//
// Build with: go build -tags treesitter
// Requires: CGo toolchain (gcc/clang)
package chunking

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	csharp "github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/elixir"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/lua"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/scala"
	"github.com/smacker/go-tree-sitter/svelte"
	"github.com/smacker/go-tree-sitter/swift"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// treeSitterAvailable reports whether tree-sitter support is compiled in.
const treeSitterAvailable = true

// parserPools keeps one sync.Pool per Language, lazily initialised.
// Each pool creates a parser pre-configured for that language.
var parserPools sync.Map // map[Language]*sync.Pool

// getParser retrieves a *sitter.Parser from the pool for lang,
// creating the pool on first use.
func getParser(lang Language) *sitter.Parser {
	tsLang := treeSitterLanguage(lang)
	val, _ := parserPools.LoadOrStore(lang, &sync.Pool{
		New: func() any {
			p := sitter.NewParser()
			p.SetLanguage(tsLang)
			return p
		},
	})
	return val.(*sync.Pool).Get().(*sitter.Parser)
}

// GetParser retrieves a *sitter.Parser from the pool for lang.
func GetParser(lang Language) *sitter.Parser { return getParser(lang) }

// ReturnParser resets and returns the parser to the pool.
func ReturnParser(lang Language, p *sitter.Parser) { returnParser(lang, p) }

// returnParser resets and returns the parser to the pool.
func returnParser(lang Language, p *sitter.Parser) {
	p.Reset()
	if val, ok := parserPools.Load(lang); ok {
		val.(*sync.Pool).Put(p)
	}
}

// treeSitterLanguage returns the tree-sitter Language for a given Language enum.
//
// Note: Zig, PowerShell, and Julia currently have no binding in
// github.com/smacker/go-tree-sitter. They are listed in the Language
// enum so file-extension detection works, but they fall through to
// the regex-based chunker in chunkByRegex. To add tree-sitter support
// for them, drop a Go binding of the corresponding tree-sitter grammar
// (community-maintained forks exist on GitHub) and import it here.
// Tracked as a follow-up in the AST coverage roadmap.
func treeSitterLanguage(lang Language) *sitter.Language {
	switch lang {
	case LangPython:
		return python.GetLanguage()
	case LangJavaScript:
		return javascript.GetLanguage()
	case LangTypeScript:
		return typescript.GetLanguage()
	case LangJava:
		return java.GetLanguage()
	case LangC:
		return c.GetLanguage()
	case LangCPP:
		return cpp.GetLanguage()
	case LangRust:
		return rust.GetLanguage()
	case LangCSharp:
		return csharp.GetLanguage()
	case LangRuby:
		return ruby.GetLanguage()
	case LangPHP:
		return php.GetLanguage()
	case LangKotlin:
		return kotlin.GetLanguage()
	case LangScala:
		return scala.GetLanguage()
	case LangSwift:
		return swift.GetLanguage()
	case LangLua:
		return lua.GetLanguage()
	case LangElixir:
		return elixir.GetLanguage()
	case LangSvelte:
		return svelte.GetLanguage()
	case LangVue:
		// Vue SFC contains JS/TS sections — use JavaScript grammar as best approximation
		return javascript.GetLanguage()
	case LangObjectiveC:
		// Objective-C is a C superset, use C grammar for basic parsing
		return c.GetLanguage()
	default:
		return nil
	}
}

// nodeTypeRules defines which AST node types represent semantic boundaries
// for each language. These are the top-level constructs we want to chunk on.
var nodeTypeRules = map[Language][]nodeRule{
	LangPython: {
		{Type: "function_definition", ChunkType: "function"},
		{Type: "class_definition", ChunkType: "class"},
		{Type: "decorated_definition", ChunkType: "decorated"},
	},
	LangJavaScript: {
		{Type: "function_declaration", ChunkType: "function"},
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "method_definition", ChunkType: "method"},
		{Type: "lexical_declaration", ChunkType: "declaration"}, // const/let
		{Type: "export_statement", ChunkType: "export"},
	},
	LangTypeScript: {
		{Type: "function_declaration", ChunkType: "function"},
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "method_definition", ChunkType: "method"},
		{Type: "lexical_declaration", ChunkType: "declaration"},
		{Type: "export_statement", ChunkType: "export"},
		{Type: "interface_declaration", ChunkType: "interface"},
		{Type: "type_alias_declaration", ChunkType: "type"},
	},
	LangJava: {
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "interface_declaration", ChunkType: "interface"},
		{Type: "method_declaration", ChunkType: "method"},
		{Type: "constructor_declaration", ChunkType: "constructor"},
		{Type: "enum_declaration", ChunkType: "enum"},
	},
	LangC: {
		{Type: "function_definition", ChunkType: "function"},
		{Type: "struct_specifier", ChunkType: "struct"},
		{Type: "enum_specifier", ChunkType: "enum"},
		{Type: "preproc_function_def", ChunkType: "macro"},
		{Type: "preproc_def", ChunkType: "macro"},
		{Type: "type_definition", ChunkType: "type"},
	},
	LangCPP: {
		{Type: "function_definition", ChunkType: "function"},
		{Type: "class_specifier", ChunkType: "class"},
		{Type: "struct_specifier", ChunkType: "struct"},
		{Type: "namespace_definition", ChunkType: "namespace"},
		{Type: "enum_specifier", ChunkType: "enum"},
		{Type: "template_declaration", ChunkType: "template"},
		{Type: "declaration", ChunkType: "declaration"},
		{Type: "type_definition", ChunkType: "type"},
		{Type: "type_definition", ChunkType: "type"},
	},
	LangRust: {
		{Type: "function_item", ChunkType: "function"},
		{Type: "struct_item", ChunkType: "struct"},
		{Type: "enum_item", ChunkType: "enum"},
		{Type: "impl_item", ChunkType: "impl"},
		{Type: "trait_item", ChunkType: "trait"},
		{Type: "mod_item", ChunkType: "module"},
		{Type: "type_item", ChunkType: "type"},
	},
	LangCSharp: {
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "interface_declaration", ChunkType: "interface"},
		{Type: "method_declaration", ChunkType: "method"},
		{Type: "constructor_declaration", ChunkType: "constructor"},
		{Type: "enum_declaration", ChunkType: "enum"},
		{Type: "struct_declaration", ChunkType: "struct"},
		{Type: "namespace_declaration", ChunkType: "namespace"},
		{Type: "property_declaration", ChunkType: "property"},
	},
	LangRuby: {
		{Type: "method", ChunkType: "method"},
		{Type: "class", ChunkType: "class"},
		{Type: "module", ChunkType: "module"},
		{Type: "singleton_method", ChunkType: "method"},
	},
	LangPHP: {
		{Type: "function_definition", ChunkType: "function"},
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "method_declaration", ChunkType: "method"},
		{Type: "interface_declaration", ChunkType: "interface"},
		{Type: "trait_declaration", ChunkType: "trait"},
	},
	LangKotlin: {
		{Type: "function_declaration", ChunkType: "function"},
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "object_declaration", ChunkType: "object"},
		{Type: "interface_declaration", ChunkType: "interface"},
		{Type: "property_declaration", ChunkType: "property"},
	},
	LangScala: {
		{Type: "function_definition", ChunkType: "function"},
		{Type: "class_definition", ChunkType: "class"},
		{Type: "object_definition", ChunkType: "object"},
		{Type: "trait_definition", ChunkType: "trait"},
		{Type: "val_definition", ChunkType: "val"},
	},
	LangSwift: {
		{Type: "function_declaration", ChunkType: "function"},
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "struct_declaration", ChunkType: "struct"},
		{Type: "protocol_declaration", ChunkType: "protocol"},
		{Type: "enum_declaration", ChunkType: "enum"},
	},
	LangLua: {
		{Type: "function_declaration", ChunkType: "function"},
		{Type: "local_function_declaration", ChunkType: "function"},
		{Type: "function_definition", ChunkType: "function"},
	},
	LangElixir: {
		{Type: "call", ChunkType: "function"},
	},
	LangSvelte: {
		{Type: "script_element", ChunkType: "script"},
		{Type: "style_element", ChunkType: "style"},
		{Type: "element", ChunkType: "component"},
	},
	LangVue: {
		{Type: "function_declaration", ChunkType: "function"},
		{Type: "class_declaration", ChunkType: "class"},
		{Type: "method_definition", ChunkType: "method"},
		{Type: "lexical_declaration", ChunkType: "declaration"},
		{Type: "export_statement", ChunkType: "export"},
	},
	LangObjectiveC: {
		{Type: "function_definition", ChunkType: "function"},
		{Type: "struct_specifier", ChunkType: "struct"},
		{Type: "enum_specifier", ChunkType: "enum"},
		{Type: "preproc_function_def", ChunkType: "macro"},
		{Type: "type_definition", ChunkType: "type"},
	},
}

// nodeRule maps a tree-sitter node type to a chunk type.
type nodeRule struct {
	Type      string // tree-sitter AST node type
	ChunkType string // our chunk category
}

// nameExtractors defines how to extract the name from AST nodes per language.
// The key is the tree-sitter node type, the value is a function extracting name.
var nameFieldByNodeType = map[string]string{
	// Python
	"function_definition":  "name",
	"class_definition":     "name",
	"decorated_definition": "", // handled specially

	// JavaScript / TypeScript
	"function_declaration":   "name",
	"class_declaration":      "name",
	"method_definition":      "name",
	"interface_declaration":  "name",
	"type_alias_declaration": "name",

	// Java / C#
	"class_declaration_java":  "name",
	"method_declaration":      "name",
	"constructor_declaration": "name",
	"enum_declaration":        "name",
	"struct_declaration":      "name",
	"namespace_declaration":   "name",
	"property_declaration":    "name",

	// C / C++
	"struct_specifier":     "name",
	"enum_specifier":       "name",
	"class_specifier":      "name",
	"namespace_definition": "name",
	"declaration":          "name",

	// Rust
	"function_item": "name",
	"struct_item":   "name",
	"enum_item":     "name",
	"impl_item":     "type", // typically 'impl MyStruct' gives MyStruct as type
	"trait_item":    "name",
	"mod_item":      "name",
	"type_item":     "name",

	// Ruby
	"method":           "name",
	"singleton_method": "name",
	"class":            "name",
	"module":           "name",

	// PHP
	// PHP functions/methods share the same types as C++ but are handled
	// gracefully by the same extractor pattern. trait_declaration etc.
	"trait_declaration": "name",

	// Kotlin
	"object_declaration": "name",

	// Scala — class_definition already mapped above (Python), same field "name"
	"object_definition": "name",
	"trait_definition":  "name",
	"val_definition":    "pattern",

	// Swift
	"protocol_declaration": "name",

	// Lua — function_declaration uses "name", local_function_declaration uses "name"
	"local_function_declaration": "name",
}

// ParseTree parses source with a pooled tree-sitter parser and returns the
// resulting *sitter.Tree along with the source bytes. The caller MUST call
// ReturnParser(lang, parser) when done. The tree's lifetime is independent
// of the parser (it owns its own memory), so the caller may close the tree
// or keep it alive across multiple extraction passes — useful when both
// symbol extraction and call extraction need the same parse.
//
// Returns (nil, nil, false) if tree-sitter has no binding for the given
// language.
func ParseTree(lang Language, source string) (parser *sitter.Parser, tree *sitter.Tree, sourceBytes []byte, ok bool) {
	tsLang := treeSitterLanguage(lang)
	if tsLang == nil {
		return nil, nil, nil, false
	}
	parser = getParser(lang)
	sourceBytes = []byte(source)
	t, err := parser.ParseCtx(context.Background(), nil, sourceBytes)
	if err != nil || t == nil {
		returnParser(lang, parser)
		return nil, nil, nil, false
	}
	return parser, t, sourceBytes, true
}

// IsTreeSitterLanguage reports whether the given language has a tree-sitter
// grammar binding registered in this build. Languages without bindings
// (LangGo, plus any future enum additions) fall through to other strategies
// (go/ast, regex).
func IsTreeSitterLanguage(lang Language) bool {
	return treeSitterLanguage(lang) != nil
}

// treeSitterChunk parses source code using tree-sitter and returns semantic chunks.
// Returns nil if tree-sitter is not available or the language is unsupported.
func treeSitterChunk(source, filename string, lang Language, config ASTChunkerConfig) []CodeChunk {
	parser, tree, sourceBytes, ok := ParseTree(lang, source)
	if !ok {
		return nil
	}
	defer returnParser(lang, parser)
	defer tree.Close()
	return treeSitterChunkFromTree(source, filename, lang, config, tree, sourceBytes)
}

// treeSitterChunkFromTree is the pure extraction step: given an already-parsed
// tree and its source bytes, return semantic CodeChunks. It does NOT acquire
// a parser, does NOT close the tree, and does NOT call ParseTree. This is the
// hot-path entry point used by ChunkCodeWithTree so the indexer can reuse one
// tree for both symbol and call extraction.
func treeSitterChunkFromTree(source, filename string, lang Language, config ASTChunkerConfig, tree *sitter.Tree, sourceBytes []byte) []CodeChunk {
	root := tree.RootNode()
	if root == nil {
		return nil
	}

	rules, ok := nodeTypeRules[lang]
	if !ok {
		return nil
	}

	lines := strings.Split(source, "\n")

	// Build a set of target node types for fast lookup.
	targetTypes := make(map[string]string) // node type -> chunk type
	for _, r := range rules {
		targetTypes[r.Type] = r.ChunkType
	}

	// Collect semantic nodes via DFS traversal.
	type astChunkInfo struct {
		startLine int
		endLine   int
		nodeType  string
		name      string
		text      string
		parentCtx string   // parent scope context for expansion
		calls     []string // outbound function calls
	}

	var collected []astChunkInfo

	// Walk the tree and collect top-level semantic nodes.
	var walk func(node *sitter.Node, depth int, parentScope string)
	walk = func(node *sitter.Node, depth int, parentScope string) {
		if node == nil {
			return
		}

		nodeType := node.Type()
		chunkType, isTarget := targetTypes[nodeType]

		if isTarget {
			startLine := int(node.StartPoint().Row) + 1
			endLine := int(node.EndPoint().Row) + 1
			name := extractNodeName(node, nodeType, sourceBytes)

			// Handle decorated definitions (Python @decorator).
			if nodeType == "decorated_definition" {
				// The actual definition is a child.
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					if child.Type() == "function_definition" || child.Type() == "class_definition" {
						chunkType = targetTypes[child.Type()]
						name = extractNodeName(child, child.Type(), sourceBytes)
						break
					}
				}
			}

			// Handle export statements (JS/TS) to use inner chunk type
			if nodeType == "export_statement" {
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					if ct, ok := targetTypes[child.Type()]; ok {
						chunkType = ct
						name = extractNodeName(child, child.Type(), sourceBytes)
						break
					}
				}
			}

			// Build parent context header for chunk expansion.
			scopeCtx := parentScope

			text := joinLines(lines, startLine-1, endLine)

			// Extract outgoing `call_expression` nodes within this scope
			calls := extractCallsForLang(lang, node, sourceBytes)

			collected = append(collected, astChunkInfo{
				startLine: startLine,
				endLine:   endLine,
				nodeType:  chunkType,
				name:      name,
				text:      text,
				parentCtx: scopeCtx,
				calls:     calls,
			})

			// For class-like nodes, recurse to find nested methods/functions.
			if isClassLike(chunkType) {
				scope := name
				if parentScope != "" {
					scope = parentScope + "::" + name
				}
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					// In C++, struct/class contents are usually under field_declaration_list
					if child.Type() == "field_declaration_list" {
						for j := 0; j < int(child.ChildCount()); j++ {
							walk(child.Child(j), depth+1, scope)
						}
					} else {
						walk(child, depth+1, scope)
					}
				}
			}
			return
		}

		// Not a target node — recurse into children.
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i), depth+1, parentScope)
		}
	}

	walk(root, 0, "")

	if len(collected) == 0 {
		return nil // Let caller fall back to regex/sliding window.
	}

	// Build chunks.
	var chunks []CodeChunk

	// Extract preamble (imports, package declarations) before first semantic node.
	if len(collected) > 0 && collected[0].startLine > 1 {
		preambleText := joinLines(lines, 0, collected[0].startLine-1)
		if strings.TrimSpace(preambleText) != "" {
			chunks = append(chunks, CodeChunk{
				Text:      preambleText,
				StartLine: 1,
				EndLine:   collected[0].startLine - 1,
				NodeType:  "preamble",
				Name:      "imports",
				Metadata: map[string]any{
					"language":  string(lang),
					"file_path": filename,
					"node_type": "preamble",
					"parser":    "tree-sitter",
				},
			})
		}
	}

	for _, info := range collected {
		name := info.name
		if info.parentCtx != "" && name != "" {
			name = info.parentCtx + "::" + name
		}

		meta := map[string]any{
			"language":  string(lang),
			"file_path": filename,
			"node_type": info.nodeType,
			"parser":    "tree-sitter",
		}
		if name != "" {
			meta["name"] = name
		}
		if info.parentCtx != "" {
			meta["parent_scope"] = info.parentCtx
		}

		text := info.text

		// Chunk expansion: prepend parent context as a header comment.
		if config.ChunkExpansion && info.parentCtx != "" {
			commentPrefix := langCommentPrefix(lang)
			header := fmt.Sprintf("%s File: %s | Scope: %s", commentPrefix, filename, info.parentCtx)
			text = header + "\n" + text
		}

		chunks = append(chunks, CodeChunk{
			Text:          text,
			StartLine:     info.startLine,
			EndLine:       info.endLine,
			NodeType:      info.nodeType,
			Name:          name,
			Metadata:      meta,
			OutboundCalls: info.calls,
		})
	}

	// Collect gaps between semantic nodes (standalone code, comments, globals).
	var gaps []CodeChunk
	prevEnd := 0
	if len(collected) > 0 {
		prevEnd = collected[0].startLine - 1
	}

	for i, info := range collected {
		if info.startLine > prevEnd+1 {
			gapText := joinLines(lines, prevEnd, info.startLine-1)
			if strings.TrimSpace(gapText) != "" {
				gaps = append(gaps, CodeChunk{
					Text:      gapText,
					StartLine: prevEnd + 1,
					EndLine:   info.startLine - 1,
					NodeType:  "block",
					Name:      fmt.Sprintf("gap_%d", i),
					Metadata: map[string]any{
						"language":  string(lang),
						"file_path": filename,
						"node_type": "block",
						"parser":    "tree-sitter",
					},
				})
			}
		}
		if info.endLine > prevEnd {
			prevEnd = info.endLine
		}
	}

	// Trailing code after last semantic node.
	if prevEnd < len(lines) {
		trailText := joinLines(lines, prevEnd, len(lines))
		if strings.TrimSpace(trailText) != "" {
			gaps = append(gaps, CodeChunk{
				Text:      trailText,
				StartLine: prevEnd + 1,
				EndLine:   len(lines),
				NodeType:  "block",
				Name:      "trailing",
				Metadata: map[string]any{
					"language":  string(lang),
					"file_path": filename,
					"node_type": "block",
					"parser":    "tree-sitter",
				},
			})
		}
	}

	// Merge semantic chunks + gaps, sorted by start line.
	result := append(chunks, gaps...)
	sortChunksByLine(result)

	return result
}

// ChunkCodeWithTree runs the tree-sitter chunker against an already-parsed
// tree. It is the public entry point used by the graph indexer so that
// symbol extraction and call extraction can share a single tree-sitter parse.
// Returns nil if the language has no tree-sitter binding; the caller should
// fall back to ASTChunker.ChunkCode in that case.
func ChunkCodeWithTree(lang Language, filename, source string, tree *sitter.Tree, sourceBytes []byte) []CodeChunk {
	if tree == nil {
		return nil
	}
	cfg := DefaultASTChunkerConfig()
	return treeSitterChunkFromTree(source, filename, lang, cfg, tree, sourceBytes)
}

// extractNodeName extracts a human-readable name from a tree-sitter AST node.
func extractNodeName(node *sitter.Node, nodeType string, source []byte) string {
	// Try the known field name first.
	if fieldName, ok := nameFieldByNodeType[nodeType]; ok && fieldName != "" {
		child := node.ChildByFieldName(fieldName)
		if child != nil {
			return child.Content(source)
		}
	}

	// For lexical_declaration (const/let), try the first declarator name.
	if nodeType == "lexical_declaration" || nodeType == "export_statement" {
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "variable_declarator" || child.Type() == "lexical_declaration" {
				nameNode := child.ChildByFieldName("name")
				if nameNode != nil {
					return nameNode.Content(source)
				}
				// Recurse one more level for export_statement.
				return extractNodeName(child, child.Type(), source)
			}
		}
	}

	// For C/C++ function_definition, extract the declarator name.
	if nodeType == "function_definition" || nodeType == "declaration" {
		decl := node.ChildByFieldName("declarator")
		if decl != nil {
			// Find the deepest identifier named identifier or field_identifier
			for decl.ChildCount() > 0 && decl.Type() != "identifier" && decl.Type() != "field_identifier" && decl.Type() != "qualified_identifier" && decl.Type() != "scoped_identifier" {
				if n := decl.ChildByFieldName("declarator"); n != nil {
					decl = n
				} else if n := decl.ChildByFieldName("name"); n != nil {
					decl = n
				} else {
					break
				}
			}
			return decl.Content(source)
		}
	}

	// For template_declaration (C++), look at the inner declaration.
	if nodeType == "template_declaration" {
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			inner := extractNodeName(child, child.Type(), source)
			if inner != "" {
				return inner
			}
		}
	}

	// For Rust impl_item, extract the type name.
	if nodeType == "impl_item" {
		typeNode := node.ChildByFieldName("type")
		if typeNode != nil {
			return typeNode.Content(source)
		}
	}

	// For preproc_def / preproc_function_def (C macros).
	if nodeType == "preproc_def" || nodeType == "preproc_function_def" {
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			return nameNode.Content(source)
		}
	}

	return ""
}

// isClassLike returns true if the chunk type represents a container (class, struct, impl, etc.)
// whose children should be individually chunked.
func isClassLike(chunkType string) bool {
	switch chunkType {
	case "class", "struct", "impl", "namespace", "module", "trait", "interface":
		return true
	}
	return false
}

// langCommentPrefix returns the single-line comment prefix for a language.
func langCommentPrefix(lang Language) string {
	switch lang {
	case LangPython:
		return "#"
	default:
		return "//"
	}
}

// sortChunksByLine sorts chunks by start line.
func sortChunksByLine(chunks []CodeChunk) {
	for i := 1; i < len(chunks); i++ {
		key := chunks[i]
		j := i - 1
		for j >= 0 && chunks[j].StartLine > key.StartLine {
			chunks[j+1] = chunks[j]
			j--
		}
		chunks[j+1] = key
	}
}

// extractCalls walks the subtree under node and returns the names of
// all call-like constructs (function calls, method invocations, etc.).
//
// It uses a single compiled tree-sitter query (built lazily via
// GetOrCompileQuery) instead of a recursive Go DFS. For files with
// 1000+ lines the cursor-driven approach is materially faster because
// the query engine in C short-circuits unmatched branches without
// paying the Go-call overhead per node.
//
// lang is the source language of `node`; it is used to pick the
// grammar binding under which the query is compiled. If lang is
// empty, the function falls back to a Go recursion so the call site
// (chunking machinery) is never broken by a missing binding.
func extractCalls(node *sitter.Node, source []byte) []string {
	return extractCallsForLang(LangUnknown, node, source)
}

// extractCallsForLang walks the subtree under node and returns the names
// of all call-like constructs (function calls, method invocations, etc.).
//
// It uses a single compiled tree-sitter query (built lazily via
// GetOrCompileQuery) instead of a recursive Go DFS. For files with
// 1000+ lines the cursor-driven approach is materially faster because
// the query engine in C short-circuits unmatched branches without
// paying the Go-call overhead per node.
//
// lang is the source language of `node`; it is used to pick the
// grammar binding under which the query is compiled AND to select
// the right S-expression (different grammars call their call nodes
// "call", "call_expression", "method_invocation", etc.). If lang is
// unknown, the function falls back to a Go recursion so the call
// site is never broken by a missing binding or grammar mismatch.
func extractCallsForLang(lang Language, node *sitter.Node, source []byte) []string {
	if node == nil {
		return nil
	}
	if lang == LangUnknown || lang == "" || treeSitterLanguage(lang) == nil {
		return extractCallsDFS(node, source)
	}
	body, ok := callsQueryBodyFor(lang)
	if !ok {
		return extractCallsDFS(node, source)
	}
	q := GetOrCompileQuery(lang, body)
	if q == nil {
		return extractCallsDFS(node, source)
	}
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, node)

	var calls []string
	seen := make(map[string]bool)
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, source)
		for _, cap := range m.Captures {
			if q.CaptureNameForId(cap.Index) == "callee" {
				name := cap.Node.Content(source)
				if name != "" && !seen[name] {
					seen[name] = true
					calls = append(calls, name)
				}
			}
		}
	}
	return calls
}

// callsQueryBodyFor returns the S-expression body that captures
// callee identifiers for the given language. The query body is
// per-language because the call-like node types and field names
// differ between grammars (e.g. Python's "call" vs JS's
// "call_expression" vs Java's "method_invocation").
func callsQueryBodyFor(lang Language) (string, bool) {
	switch lang {
	case LangPython:
		return `
(call function: (_) @callee)
`, true
	case LangElixir:
		return `
(call target: (_) @callee)
`, true
	case LangRuby:
		// Ruby uses `(call ...)` with callee under "method".
		return `
(call method: (_) @callee)
`, true
	case LangJavaScript, LangTypeScript, LangVue, LangCPP, LangRust, LangObjectiveC:
		// Most ECMAScript-family and C-family grammars expose the
		// callee as the "function" field of call_expression. Svelte
		// is intentionally NOT in this list: its tree-sitter grammar
		// uses a different vocabulary for call-like nodes inside
		// <script> blocks, and the Svelte-specific query lives in
		// ts_calls.go (the indexer) as a narrower, optional shape.
		// We keep Svelte out of the broad union so a single failure
		// in its grammar doesn't break call extraction for the other
		// JS-family languages.
		return `
(call_expression function: (_) @callee)
`, true
	case LangPHP:
		// PHP has three call shapes depending on how the callee is
		// accessed: bare function call, namespaced (`A::b()`), and
		// method-on-object (`$a->b()`). The original tree-sitter call
		// query in ts_calls.go uses the same alternation; we mirror it
		// here so the chunker and the call extractor stay in sync.
		return `
[
  (function_call_expression function: (_) @callee)
  (scoped_call_expression   name:     (_) @callee)
  (member_call_expression   name:     (_) @callee)
]
`, true
	case LangJava:
		return `
(method_invocation name: (_) @callee)
`, true
	case LangCSharp:
		return `
(invocation_expression function: (_) @callee)
`, true
	case LangC:
		return `
(call_expression function: (_) @callee)
`, true
	case LangLua:
		return `
(function_call name: (_) @callee)
`, true
	}
	return "", false
}

// extractCallsDFS is the original recursive implementation, retained
// as a fallback for stub builds and as a safety net if a future
// grammar rejects the union query.
func extractCallsDFS(node *sitter.Node, source []byte) []string {
	if node == nil {
		return nil
	}
	var calls []string
	seen := make(map[string]bool)
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type()
		if nodeType == "call_expression" || nodeType == "call" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil {
				name := string(funcNode.Content(source))
				if name != "" && !seen[name] {
					seen[name] = true
					calls = append(calls, name)
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return calls
}
