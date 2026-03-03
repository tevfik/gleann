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
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
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

// returnParser resets and returns the parser to the pool.
func returnParser(lang Language, p *sitter.Parser) {
	p.Reset()
	if val, ok := parserPools.Load(lang); ok {
		val.(*sync.Pool).Put(p)
	}
}

// treeSitterLanguage returns the tree-sitter Language for a given Language enum.
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

	// Rust
	"function_item": "name",
	"struct_item":   "name",
	"enum_item":     "name",
	"impl_item":     "type",
	"trait_item":    "name",
	"mod_item":      "name",
	"type_item":     "name",
}

// treeSitterChunk parses source code using tree-sitter and returns semantic chunks.
// Returns nil if tree-sitter is not available or the language is unsupported.
func treeSitterChunk(source, filename string, lang Language, config ASTChunkerConfig) []CodeChunk {
	tsLang := treeSitterLanguage(lang)
	if tsLang == nil {
		return nil
	}

	rules, ok := nodeTypeRules[lang]
	if !ok {
		return nil
	}

	// Get a pooled parser instead of allocating a new one per file.
	parser := getParser(lang)
	defer returnParser(lang, parser)

	sourceBytes := []byte(source)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceBytes)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
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
		parentCtx string // parent scope context for expansion
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

			// Build parent context header for chunk expansion.
			scopeCtx := parentScope

			text := joinLines(lines, startLine-1, endLine)

			collected = append(collected, astChunkInfo{
				startLine: startLine,
				endLine:   endLine,
				nodeType:  chunkType,
				name:      name,
				text:      text,
				parentCtx: scopeCtx,
			})

			// For class-like nodes, recurse to find nested methods/functions.
			if isClassLike(chunkType) {
				scope := name
				if parentScope != "" {
					scope = parentScope + "." + name
				}
				for i := 0; i < int(node.ChildCount()); i++ {
					walk(node.Child(i), depth+1, scope)
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
		meta := map[string]any{
			"language":  string(lang),
			"file_path": filename,
			"node_type": info.nodeType,
			"parser":    "tree-sitter",
		}
		if info.name != "" {
			meta["name"] = info.name
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
			Text:      text,
			StartLine: info.startLine,
			EndLine:   info.endLine,
			NodeType:  info.nodeType,
			Name:      info.name,
			Metadata:  meta,
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
	if nodeType == "function_definition" {
		decl := node.ChildByFieldName("declarator")
		if decl != nil {
			// Could be a function_declarator wrapping an identifier.
			nameNode := decl.ChildByFieldName("declarator")
			if nameNode != nil {
				return nameNode.Content(source)
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
