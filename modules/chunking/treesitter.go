//go:build cgo && treesitter

// Tree-sitter based AST chunking for non-Go languages.
// Provides precise AST parsing for Python, JavaScript, TypeScript,
// Java, C, C++, Rust, C#, Ruby, PHP, Kotlin, Scala, Swift, Lua, Elixir,
// Svelte, Vue and Objective-C using tree-sitter grammars.
//
// Build with: go build -tags treesitter
// Requires: CGo toolchain (gcc/clang)
package chunking

import (
	"bytes"
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
		// The bundled Lua grammar names its declaration node
		// function_statement (`local function f`, `function M.run`,
		// `function M:send`); function_declaration/local_function_declaration
		// exist in other Lua grammars only.
		{Type: "function_statement", ChunkType: "function"},
	},
	LangElixir: {
		// Every Elixir macro invocation is a `call` node; the walk below keeps
		// only the defining macros (def/defp/defmodule/...) as chunks.
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

	// Lua — function_statement carries the declared name; for
	// `function M.run`/`function M:send` it is a function_name node whose
	// content is the qualified name.
	"function_statement": "name",
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

			// The Lua grammar attaches the leading whitespace (often a newline)
			// to a function_statement's first token and the statement
			// terminator to its last one, so the raw node span starts on the
			// previous line and ends one line into the next statement. Anchor
			// the declaration on `function`..`end` instead.
			if lang == LangLua && nodeType == "function_statement" {
				startLine = firstNonSpaceLine(sourceBytes, int(node.StartByte()))
				if fe := lastChildOfType(node, "function_end"); fe != nil {
					endLine = int(fe.EndPoint().Row) + 1
				}
			}

			name := extractNodeName(node, nodeType, lang, sourceBytes)

			// A Lua function declared with a qualified name
			// (`function M.run`, `function M:send`) is a method.
			if lang == LangLua && nodeType == "function_statement" && strings.ContainsAny(name, ".:") {
				chunkType = "method"
			}

			// Every Elixir macro invocation is a `call` node, so only the
			// defining macros are declarations. The others (alias, import,
			// use, defstruct, plain calls) are walked through, so a
			// definition nested in one is still found.
			if lang == LangElixir && nodeType == "call" {
				declName, declType := elixirDeclaration(node, sourceBytes)
				if declType == "" {
					for i := 0; i < int(node.ChildCount()); i++ {
						walk(node.Child(i), depth+1, parentScope)
					}
					return
				}
				name, chunkType = declName, declType
			}

			// Handle decorated definitions (Python @decorator).
			if nodeType == "decorated_definition" {
				// The actual definition is a child.
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					if child.Type() == "function_definition" || child.Type() == "class_definition" {
						chunkType = targetTypes[child.Type()]
						name = extractNodeName(child, child.Type(), lang, sourceBytes)
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
						name = extractNodeName(child, child.Type(), lang, sourceBytes)
						break
					}
				}
			}

			// Build parent context header for chunk expansion.
			scopeCtx := parentScope

			text := joinLines(lines, startLine-1, endLine)
			if config.SignaturesOnly && (chunkType == "function" || chunkType == "method") {
				sigEnd := startLine
				for l := startLine; l <= endLine && l < startLine+5; l++ {
					sigEnd = l
					lineStr := lines[l-1]
					if strings.Contains(lineStr, "{") || strings.HasSuffix(strings.TrimSpace(lineStr), ":") {
						break
					}
				}
				text = joinLines(lines, startLine-1, sigEnd)
				endLine = sigEnd
			}

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
func extractNodeName(node *sitter.Node, nodeType string, lang Language, source []byte) string {
	// Try the known field name first.
	if fieldName, ok := nameFieldByNodeType[nodeType]; ok && fieldName != "" {
		child := node.ChildByFieldName(fieldName)
		if child != nil {
			return child.Content(source)
		}
	}

	// Kotlin's bundled grammar exposes no field names at all, so the name node
	// has to be located by type. Without this every Kotlin chunk had an empty
	// name and the indexer dropped the declaration.
	if lang == LangKotlin {
		if n := kotlinNameNode(node, nodeType); n != nil {
			return n.Content(source)
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
				return extractNodeName(child, child.Type(), lang, source)
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
			inner := extractNodeName(child, child.Type(), lang, source)
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

// SvelteScript is one inline <script> block of a Svelte component.
type SvelteScript struct {
	Lang      Language // LangJavaScript, or LangTypeScript for lang="ts"
	Content   string   // the body of the block, without the tags
	StartRow  int      // 0-based row of the block's first content line
	StartCol  int      // 0-based column at which the content starts
	ByteStart int      // byte offset of the content in the file
}

// SvelteScripts returns the inline <script> blocks of a parsed Svelte
// component in document order. The Svelte grammar keeps a block's JavaScript or
// TypeScript body as an unparsed raw_text node, so callers that want the
// declarations and calls inside one parse the block themselves with the
// JavaScript/TypeScript grammar and shift its line numbers by StartRow (the
// parser reports rows relative to the block, not to the component). StartCol
// and ByteStart locate the content for consumers that need an exact anchor.
//
// A component without a <script> block (template only) returns nil.
func SvelteScripts(tree *sitter.Tree, source []byte) []SvelteScript {
	if tree == nil {
		return nil
	}
	var out []SvelteScript
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "script_element" {
			if s, ok := svelteScriptOf(n, source); ok {
				out = append(out, s)
			}
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	return out
}

// svelteScriptOf builds the SvelteScript of one script_element node.
func svelteScriptOf(el *sitter.Node, source []byte) (SvelteScript, bool) {
	var raw *sitter.Node
	lang := LangJavaScript
	for i := 0; i < int(el.ChildCount()); i++ {
		ch := el.Child(i)
		switch ch.Type() {
		case "raw_text":
			raw = ch
		case "start_tag":
			if svelteScriptIsTS(ch, source) {
				lang = LangTypeScript
			}
		}
	}
	if raw == nil {
		return SvelteScript{}, false
	}
	sp := raw.StartPoint()
	return SvelteScript{
		Lang:      lang,
		Content:   raw.Content(source),
		StartRow:  int(sp.Row),
		StartCol:  int(sp.Column),
		ByteStart: int(raw.StartByte()),
	}, true
}

// svelteScriptIsTS reports whether a <script> start tag carries lang="ts" or
// lang="typescript".
func svelteScriptIsTS(startTag *sitter.Node, source []byte) bool {
	for i := 0; i < int(startTag.ChildCount()); i++ {
		attr := startTag.Child(i)
		if attr.Type() != "attribute" {
			continue
		}
		name := firstChildOfType(attr, "attribute_name")
		if name == nil || name.Content(source) != "lang" {
			continue
		}
		if v := findDescendantOfType(attr, "attribute_value", 2); v != nil {
			switch strings.ToLower(v.Content(source)) {
			case "ts", "typescript":
				return true
			}
		}
	}
	return false
}

// findDescendantOfType returns the first descendant of node (including the
// direct children) of the given type, searching at most maxDepth levels down.
func findDescendantOfType(node *sitter.Node, nodeType string, maxDepth int) *sitter.Node {
	if node == nil || maxDepth < 0 {
		return nil
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		ch := node.Child(i)
		if ch.Type() == nodeType {
			return ch
		}
		if found := findDescendantOfType(ch, nodeType, maxDepth-1); found != nil {
			return found
		}
	}
	return nil
}

// isClassLike returns true if the chunk type represents a container (class, struct, impl, etc.)
// whose children should be individually chunked.
func isClassLike(chunkType string) bool {
	switch chunkType {
	case "class", "struct", "impl", "namespace", "module", "trait", "interface", "object":
		return true
	}
	return false
}

// kotlinNameNode returns the node holding the declared name of a Kotlin
// declaration. The grammar bundled with go-tree-sitter has no field names: a
// function's name is its first simple_identifier, a class/object/interface name
// its first type_identifier, and a property's name is the simple_identifier of
// its variable_declaration.
func kotlinNameNode(node *sitter.Node, nodeType string) *sitter.Node {
	switch nodeType {
	case "function_declaration":
		return firstChildOfType(node, "simple_identifier")
	case "class_declaration", "object_declaration", "interface_declaration":
		return firstChildOfType(node, "type_identifier", "simple_identifier")
	case "property_declaration":
		if vd := firstChildOfType(node, "variable_declaration"); vd != nil {
			return firstChildOfType(vd, "simple_identifier")
		}
	}
	return nil
}

// firstChildOfType returns the first direct child of node whose type is one of
// types, or nil.
func firstChildOfType(node *sitter.Node, types ...string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		ch := node.Child(i)
		for _, t := range types {
			if ch.Type() == t {
				return ch
			}
		}
	}
	return nil
}

// lastChildOfType returns the last direct child of node of the given type, or
// nil.
func lastChildOfType(node *sitter.Node, nodeType string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := int(node.ChildCount()) - 1; i >= 0; i-- {
		if ch := node.Child(i); ch.Type() == nodeType {
			return ch
		}
	}
	return nil
}

// firstNonSpaceLine returns the 1-based line of the first byte that is not
// whitespace at or after off. Some grammars (Lua) include the preceding
// newline in the node they hand to the chunker; the declaration really starts
// at its first token.
func firstNonSpaceLine(src []byte, off int) int {
	if off < 0 || off > len(src) {
		return 1
	}
	line := 1 + bytes.Count(src[:off], []byte("\n"))
	for i := off; i < len(src); i++ {
		switch src[i] {
		case '\n':
			line++
		case ' ', '\t', '\r', '\f', '\v':
			// skip
		default:
			return line
		}
	}
	return line
}

// elixirDeclaration returns the name and chunk type declared by an Elixir
// `call` node when the call is a defining macro, or two empty strings when it
// is not. The defining macros keep the project's real declarations:
//
//	defmodule M do ... end          -> module M
//	def foo do ... end              -> function foo
//	defp bar(x), do: other(x)       -> function bar
//	defmacro mac(x) do ... end      -> function mac
//
// defstruct/defimpl/defexception are deliberately not declarations: they define
// no nameable function, and two `defimpl X, for: Y` blocks would collide on the
// same FQN.
func elixirDeclaration(node *sitter.Node, source []byte) (name, chunkType string) {
	target := node.ChildByFieldName("target")
	if target == nil || target.Type() != "identifier" {
		return "", ""
	}
	switch target.Content(source) {
	case "defmodule", "defprotocol":
		chunkType = "module"
	case "def", "defp", "defmacro", "defmacrop", "defdelegate", "defguard", "defguardp":
		chunkType = "function"
	default:
		return "", ""
	}

	// `arguments` is a child node type, not a field, in the bundled grammar.
	args := firstChildOfType(node, "arguments")
	if args == nil {
		return "", ""
	}
	first := elixirSignatureNode(firstNamedChild(args))
	if first == nil {
		return "", ""
	}
	switch first.Type() {
	case "identifier", "alias":
		return first.Content(source), chunkType
	case "call":
		// `def foo(x) do ... end`: the signature is itself a call node.
		if t := first.ChildByFieldName("target"); t != nil && t.Type() == "identifier" {
			return t.Content(source), chunkType
		}
	}
	return "", ""
}

// elixirSignatureNode returns the declared signature of an Elixir definition
// header. A `when` guard makes the grammar wrap the header in a
// `binary_operator` whose operator field is the `when` node and whose left
// child is the signature:
//
//	def foo(n) when is_integer(n)   -> binary_operator(left: call foo(n), ...)
//	defguard is_even(n) when ...    -> binary_operator(left: call is_even(n), ...)
//
// Multiple guards nest to the right (`when a when b`), so the signature is the
// leftmost `left` child of the whole chain. An unguarded header passes through
// unchanged (the plain identifier, alias or signature call). A header that is
// any other binary operator (`def foo(a + b)`) has no extractable name.
func elixirSignatureNode(n *sitter.Node) *sitter.Node {
	for n != nil && n.Type() == "binary_operator" {
		op := n.ChildByFieldName("operator")
		if op == nil || op.Type() != "when" {
			return nil
		}
		n = n.ChildByFieldName("left")
	}
	return n
}

// IsElixirDefinitionHeaderCall reports whether call belongs to the header of an
// Elixir defining macro or module attribute rather than being a real call site.
//
// A module attribute is parsed as `unary_operator(@, call(<name>, arguments))`.
// Its name (`@moduledoc`, `@doc`, `@spec`, `@attr`, ...) is a declaration, and
// every expression inside a typespec attribute (`@spec`, `@type`, `@callback`,
// ...) denotes a type, not a call. The value expression of a plain attribute
// (`@attr helper()`, `@config build_config() |> finalize()`) is evaluated at
// compile time and remains a real call site.
//
// A definition header is the first argument of the defining macro: the declared
// signature (`foo` in `def foo(x)`), possibly with default arguments, followed
// by a `when` guard chain. The signature itself is not a call site, and neither
// is any guard expression, at any nesting depth: guards are restricted to
// guard-safe expressions and never denote a project function, so a guard-safe
// builtin nested inside another guard call's argument list
// (`byte_size` in `def foo(x) when binary_part(x, 0, byte_size(x)) == "a"`) is
// header too.
//
// A call under the macro's `do_block` is a body call and is never header. A
// default argument (`def foo(x \\ helper())`) is a real call site: it sits in
// the signature call's own argument list and is evaluated when foo is called.
// A keyword-form body (`def foo(x) when is_map(x), do: helper(x)`) follows the
// same rule: the `do:` value is a trailing keyword pair of the macro's
// arguments, not part of its header.
func IsElixirDefinitionHeaderCall(call *sitter.Node, src []byte) bool {
	if call == nil {
		return false
	}
	if elixirAttributeNonCall(call, src) {
		return true
	}
	header := elixirDefiningHeader(call, src)
	if header == nil {
		return false
	}
	sig := elixirSignatureNode(header)
	if sig == nil || !elixirNodeContains(sig, call) {
		return true
	}
	// The signature call itself is the declared name; its own argument list
	// holds default values, which are evaluated at call time.
	return call.StartByte() == sig.StartByte() && call.EndByte() == sig.EndByte()
}

// elixirDefiningHeader returns the header node of the innermost defining macro
// whose argument list contains call, or nil when call is not in such a header.
// The header is the macro's first argument (signature plus its `when` guards);
// a trailing `keywords` list (`, do: ...`) and the `do_block` body are not.
func elixirDefiningHeader(call *sitter.Node, src []byte) *sitter.Node {
	for n := call.Parent(); n != nil; n = n.Parent() {
		if n.Type() == "do_block" {
			// A body call: no defining macro header above a do_block.
			return nil
		}
		if n.Type() != "arguments" || !elixirDefiningArguments(n, src) {
			continue
		}
		header := firstNamedChild(n)
		if header == nil || !elixirNodeContains(header, call) {
			return nil
		}
		return header
	}
	return nil
}

// elixirAttributeNonCall reports whether call is the name of a module attribute
// or an expression of a typespec attribute value, i.e. not a call site.
func elixirAttributeNonCall(call *sitter.Node, src []byte) bool {
	for n := call.Parent(); n != nil; n = n.Parent() {
		if n.Type() != "unary_operator" {
			continue
		}
		op := n.ChildByFieldName("operator")
		if op == nil || strings.TrimSpace(op.Content(src)) != "@" {
			// `not`/`!`/`^`: not an attribute; an enclosing expression could
			// still be one, so keep walking outward.
			continue
		}
		operand := n.ChildByFieldName("operand")
		if operand == nil {
			return false
		}
		// The call directly under the `@` is the attribute's name.
		if operand.Equal(call) {
			return true
		}
		return IsElixirTypespecAttribute(elixirAttributeName(operand, src))
	}
	return false
}

// elixirAttributeName returns the attribute name of a `unary_operator(@)`'s
// operand call, or "" when the operand is not a named call.
func elixirAttributeName(operand *sitter.Node, src []byte) string {
	if operand == nil || operand.Type() != "call" {
		return ""
	}
	target := operand.ChildByFieldName("target")
	if target == nil || target.Type() != "identifier" {
		return ""
	}
	return strings.TrimSpace(target.Content(src))
}

// elixirNodeContains reports whether inner lies inside the source span of
// outer.
func elixirNodeContains(outer, inner *sitter.Node) bool {
	return outer != nil && inner != nil &&
		inner.StartByte() >= outer.StartByte() && inner.EndByte() <= outer.EndByte()
}

// IsElixirNonCallKeywordCall reports whether call is an unqualified invocation
// of an Elixir definition, directive or control-flow macro, i.e. a `call` node
// that must not be recorded as a CALLS edge.
//
// Only a bare `identifier` target can be the Kernel macro: a qualified callee
// is a `dot` (`Mod.if(x)` calls a function named `if` on Mod), so it is a real
// remote call and its name must not be filtered.
func IsElixirNonCallKeywordCall(call *sitter.Node, name string) bool {
	if !IsElixirNonCallKeyword(name) {
		return false
	}
	if call == nil {
		// Without the call node we cannot tell unqualified from qualified;
		// keep the conservative (keyword) behaviour.
		return true
	}
	target := call.ChildByFieldName("target")
	if target == nil {
		return true
	}
	return target.Type() == "identifier"
}

// elixirDefiningArguments reports whether args is the argument list of a call
// to one of the defining macros (`def`, `defp`, `defguard`, ...).
func elixirDefiningArguments(args *sitter.Node, src []byte) bool {
	def := args.Parent()
	if def == nil || def.Type() != "call" {
		return false
	}
	target := def.ChildByFieldName("target")
	if target == nil || target.Type() != "identifier" {
		return false
	}
	return IsElixirDefiningMacro(strings.TrimSpace(target.Content(src)))
}

// firstNamedChild returns the first named child of node, or nil.
func firstNamedChild(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		if ch := node.Child(i); ch.IsNamed() {
			return ch
		}
	}
	return nil
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

		// Elixir needs the whole call node to recognise the signature and
		// guards of a definition header; other languages capture only the
		// callee name.
		var elixirCall *sitter.Node
		if lang == LangElixir {
			for _, cap := range m.Captures {
				if q.CaptureNameForId(cap.Index) == "call" {
					elixirCall = cap.Node
				}
			}
		}

		for _, cap := range m.Captures {
			if q.CaptureNameForId(cap.Index) == "callee" {
				name := strings.TrimSpace(cap.Node.Content(source))
				if name == "" {
					continue
				}
				// Elixir parses its defining and directive macros as calls;
				// they are not callees. A definition header (the signature
				// and its `when` guards) is not a call site either. Both
				// filters only apply to an unqualified target: `Mod.if(x)`
				// is a real remote call to a function named `if`.
				if lang == LangElixir && (IsElixirNonCallKeywordCall(elixirCall, name) || IsElixirDefinitionHeaderCall(elixirCall, source)) {
					continue
				}
				if !seen[name] {
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
		// A call's callee is an identifier for `helper()` and a `dot` for a
		// remote call (`Mod.func()`, `A.B.helper()`, `foo().bar()`); the
		// receiver is not part of the callee, matching Python/JS/Ruby. The
		// whole call is captured too so the definition header (a guarded
		// signature) can be told apart from a body call.
		//
		// The pipe operator calls its right-hand side (`xs |> helper` is
		// `helper(xs)`), but the grammar models a parenless RHS as a bare
		// identifier rather than a call node. A parenthesized or remote RHS
		// (`xs |> helper()`, `xs |> Mod.func`) is a call and is matched above.
		return `
[
  (call
    target: [
      (identifier) @callee
      (dot right: (identifier) @callee)
    ]) @call
  (binary_operator
    operator: "|>"
    right: (identifier) @callee) @call
]
`, true
	case LangRuby:
		// Ruby uses `(call ...)` with callee under "method".
		return `
(call method: (_) @callee)
`, true
	case LangJavaScript, LangTypeScript, LangVue, LangCPP, LangRust, LangObjectiveC:
		// Most ECMAScript-family and C-family grammars expose the
		// callee as the "function" field of call_expression. Svelte is
		// intentionally NOT in this list: its grammar keeps each
		// <script> body as an unparsed raw_text node, so the indexer
		// parses those blocks with the JavaScript/TypeScript grammar
		// (see SvelteScripts and collectTSCallsPrecise).
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
		// A Lua call's callee is the identifier immediately before its
		// argument list; the identifiers before it belong to the receiver
		// (`m` in `m.helper()`, `obj` in `obj:send()`). The dot anchor keeps
		// them out, and the argument alternatives cover the argument forms
		// without a paren list (`require "x"`, `f{...}`).
		return `
(function_call
  (identifier) @callee
  . [(function_call_paren) (string_argument) (table_argument)])
`, true
	case LangKotlin:
		// Kotlin's grammar has no field names: a bare call's callee is a
		// simple_identifier child, a navigation call's (`a.b()`) the
		// simple_identifier inside the navigation_suffix.
		return `
(call_expression
  [
    (simple_identifier) @callee
    (navigation_expression (navigation_suffix (simple_identifier) @callee))
  ])
`, true
	case LangScala:
		// Scala exposes the callee in the `function` field: an identifier for
		// `helper()` and a field_expression for `obj.run()`.
		return `
(call_expression
  function: [
    (identifier) @callee
    (field_expression field: (identifier) @callee)
  ])
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

// ChunkCodeWithTree is ChunkCode for an already-parsed tree-sitter tree: the
// same chunks (oversized ones split, line numbers per config) without parsing
// the file a second time. Returns nil when tree is nil; the caller falls back
// to ChunkCode.
func (c *ASTChunker) ChunkCodeWithTree(lang Language, filename, source string, tree *sitter.Tree, sourceBytes []byte) []CodeChunk {
	if tree == nil {
		return nil
	}
	chunks := treeSitterChunkFromTree(source, filename, lang, c.config, tree, sourceBytes)
	if chunks == nil {
		return nil
	}
	return c.postProcess(chunks)
}
