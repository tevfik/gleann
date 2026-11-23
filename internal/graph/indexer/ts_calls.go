//go:build treesitter

package indexer

import (
	"bytes"
	"path"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// callQueryBodies is the registry of S-expression queries used for CALLS
// extraction. Keys are chunking.Language values; values are the S-expression
// bodies that bind @name (callee identifier) and @call (whole call node).
//
// These strings are compiled lazily on first use via chunking.GetOrCompileQuery;
// the chunking package owns both the parser pool and the query cache, both
// keyed by (lang, body) and safe for concurrent use. We deliberately do not
// pre-compile a global map or maintain a separate parser pool here — that
// duplication was the root cause of the 2x-3x per-file parse cost.
var callQueryBodies = map[chunking.Language]string{
	chunking.LangPython: `
(call
  function: [
    (identifier) @name
    (attribute attribute: (identifier) @name)
  ]
) @call
`,
	chunking.LangJavaScript: `
(call_expression
  function: [
    (identifier) @name
    (member_expression property: (property_identifier) @name)
  ]
) @call
`,
	chunking.LangTypeScript: `
(call_expression
  function: [
    (identifier) @name
    (member_expression property: (property_identifier) @name)
  ]
) @call
`,
	chunking.LangC: `
(call_expression
  function: (identifier) @name
) @call
`,
	chunking.LangCPP: `
(call_expression
  function: [
    (identifier) @name
    (field_expression field: (field_identifier) @name)
    (qualified_identifier name: (identifier) @name)
  ]
) @call
`,
	chunking.LangRust: `
(call_expression
  function: [
    (identifier) @name
    (field_expression field: (field_identifier) @name)
    (scoped_identifier name: (identifier) @name)
  ]
) @call
`,
	chunking.LangJava: `
(method_invocation
  name: (identifier) @name
) @call
`,
	chunking.LangCSharp: `
(invocation_expression
  function: [
    (identifier) @name
    (member_access_expression name: (identifier) @name)
  ]
) @call
`,
	// Ruby gives every call a `call` node with the callee in its `method`
	// field, whether it is written with parentheses (`helper(x)`), without
	// them (`helper x`, `puts "hi"`) or on a receiver (`obj.save`,
	// `Mod.call(x)`); a `def` name is not a call. A receiverless, argumentless
	// word (`save` alone) parses as a plain identifier, so the grammar does not
	// expose it as a call at all.
	chunking.LangRuby: `
(call
  method: (identifier) @name) @call
`,
	chunking.LangPHP: `
[
  (function_call_expression
    function: (name) @name) @call
  (scoped_call_expression
    name: (name) @name) @call
  (member_call_expression
    name: (name) @name) @call
]
`,
	// Kotlin's grammar attaches no field names to its nodes: a bare call's
	// callee is a simple_identifier child, a navigation call's (`a.b()`) is the
	// simple_identifier inside the navigation_suffix.
	chunking.LangKotlin: `
(call_expression
  [
    (simple_identifier) @name
    (navigation_expression
      (navigation_suffix (simple_identifier) @name))
  ]) @call
`,
	// Scala exposes the callee in the `function` field: an identifier for
	// `helper()` and a field_expression for `obj.run()`.
	chunking.LangScala: `
(call_expression
  function: [
    (identifier) @name
    (field_expression field: (identifier) @name)
  ]
) @call
`,
	chunking.LangSwift: `
(call_expression
  (simple_identifier) @name) @call
`,
	// A Lua call's callee is the identifier immediately before its argument
	// list; the identifiers before it belong to the receiver (`m` in
	// `m.helper()`, `obj` in `obj:send()`). The dot anchor keeps them out, and
	// the argument alternatives cover the argument forms without a paren list
	// (`require "x"`, `f{...}`).
	chunking.LangLua: `
(function_call
  (identifier) @name
  . [(function_call_paren) (string_argument) (table_argument)]) @call
`,
	// Every Elixir macro invocation is a `call` node. A call's target is a
	// plain identifier (`helper()`) or a `dot` for a remote call
	// (`Mod.func()`, `A.B.helper()`, `foo().bar()`); the receiver is not part
	// of the callee, matching Python/JS/Ruby. The defining/directive macros and
	// the definition headers (a guarded signature) are filtered in chargeCalls,
	// so only real callees become edges.
	//
	// The pipe operator calls its right-hand side (`xs |> helper` is
	// `helper(xs)`), but the grammar models a parenless RHS as a bare
	// identifier rather than a call node. A parenthesized or remote RHS
	// (`xs |> helper()`, `xs |> Mod.func`) is a call and is matched above.
	chunking.LangElixir: `
[
  (call
    target: [
      (identifier) @name
      (dot right: (identifier) @name)
    ]) @call
  (binary_operator
    operator: "|>"
    right: (identifier) @name) @call
]
`,
	// Svelte has no entry: its grammar keeps each <script> body as an unparsed
	// raw_text node, so collectSvelteCalls parses those blocks with the
	// JavaScript/TypeScript grammar instead. A file with no script block has no
	// calls.
}

// collectTSCallsPrecise walks an already-parsed tree-sitter tree and returns
// the synthetic "<script>" caller (if any) and the CALLS edges of one file.
// The caller owns the parser and tree lifetimes: this function does NOT close
// the tree and does NOT access any parser pool. module and relPath are all the
// FQN rule (buildFQNOf) requires.
//
// A call is charged to the innermost declaration containing it (a method
// rather than its class) and the synthetic "<script>" caller of module-level
// calls carries a real source anchor (AGENTS.md section 2.2).
//
// For Svelte the component's inline <script> blocks are parsed with the
// JavaScript/TypeScript grammar and contribute their declarations and CALLS
// edges (collectSvelteCalls); template markup is not scanned for calls.
//
// Returns nil, nil if the language has no registered call query (e.g. Go: Go
// uses go/ast directly).
func collectTSCallsPrecise(module, relPath string, lang chunking.Language, tree *sitter.Tree, sourceBytes []byte, chunks []chunking.CodeChunk) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls) {
	if lang == chunking.LangSvelte {
		return collectSvelteCalls(module, relPath, tree, sourceBytes)
	}
	if _, ok := callQueryBodies[lang]; !ok {
		return nil, nil
	}
	return callsToGraph(module, relPath, sourceBytes, chargeCalls(lang, tree, sourceBytes, callersOf(chunks), 0))
}

// chargedCall is one call site: the callee name and the name of the enclosing
// declaration, empty when the call is at module level.
type chargedCall struct {
	callee string
	caller string
}

// callersOf returns the chunks a call can be charged to: the declarations
// whose source span can contain a call site.
func callersOf(chunks []chunking.CodeChunk) []chunking.CodeChunk {
	var callers []chunking.CodeChunk
	for _, ch := range chunks {
		if ch.Name == "" {
			continue
		}
		switch ch.NodeType {
		case "function", "method", "class":
			callers = append(callers, ch)
		}
	}
	return callers
}

// chargeCalls runs the call query registered for qlang over tree and charges
// every callee to the innermost caller of callers whose line range contains the
// call. rowOffset is the 0-based row at which the parsed source starts inside
// the file (0 for a file parsed whole, the script block's row for a Svelte
// <script> block parsed on its own).
func chargeCalls(qlang chunking.Language, tree *sitter.Tree, src []byte, callers []chunking.CodeChunk, rowOffset int) []chargedCall {
	body, ok := callQueryBodies[qlang]
	if !ok || tree == nil {
		return nil
	}
	cq := chunking.GetOrCompileQuery(qlang, body)
	if cq == nil {
		return nil
	}

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(cq, tree.RootNode())

	var out []chargedCall
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, src)

		var calleeName string
		var callLine int
		var callNode *sitter.Node

		for _, cap := range m.Captures {
			switch cq.CaptureNameForId(cap.Index) {
			case "name":
				// The bundled Lua grammar includes the leading whitespace of
				// an identifier in its node content (e.g. "\nprint") and its
				// start point is on the previous line: the end point is the
				// line the callee is actually written on.
				calleeName = strings.TrimSpace(cap.Node.Content(src))
				callLine = int(cap.Node.EndPoint().Row) + 1 + rowOffset
			case "call":
				callNode = cap.Node
			}
		}
		if calleeName == "" {
			continue
		}
		if qlang == chunking.LangElixir {
			if chunking.IsElixirNonCallKeywordCall(callNode, calleeName) || elixirDefinitionSignature(callNode, src) {
				continue
			}
		}
		out = append(out, chargedCall{callee: calleeName, caller: innerCallerOf(callers, callLine)})
	}
	return out
}

// innerCallerOf returns the name of the innermost caller containing line, or ""
// when the line is outside every declaration. On a tie the later chunk wins (a
// method after its class on one line).
func innerCallerOf(callers []chunking.CodeChunk, line int) string {
	best := -1
	for i, ch := range callers {
		if line < ch.StartLine || line > ch.EndLine {
			continue
		}
		if best < 0 || ch.EndLine-ch.StartLine <= callers[best].EndLine-callers[best].StartLine {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return callers[best].Name
}

// elixirDefinitionSignature reports whether call belongs to the header of a
// defining macro rather than being a real call site: the declared signature
// (`foo` in `def foo(x)`, `bar` in `defp bar(x), do: ...`), the signature on
// the left of a `when` guard, or a guard check on its right
// (`is_integer(n)` in `def foo(n) when is_integer(n)`). Without this check the
// declaration (and its guard) would also produce bogus edges on partially
// qualified names.
//
// The rule lives in the chunking package so the chunker's OutboundCalls and the
// graph's CALLS edges stay identical.
func elixirDefinitionSignature(call *sitter.Node, src []byte) bool {
	return chunking.IsElixirDefinitionHeaderCall(call, src)
}

// callsToGraph turns charged call sites into the callee stub symbols, the
// per-file "<script>" symbol of module-level calls and the CALLS edges,
// deduplicated by caller → callee.
func callsToGraph(module, relPath string, src []byte, calls []chargedCall) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls) {
	seen := make(map[string]bool)
	scriptAdded := false

	for _, c := range calls {
		callerFQN := ""
		if c.caller != "" {
			callerFQN = buildFQNOf(module, relPath, c.caller)
		}
		if callerFQN == "" {
			callerFQN = buildFQNOf(module, relPath, scriptSymbolName(relPath))
			if !scriptAdded {
				scriptAdded = true
				nodes = append(nodes, scriptSymbol(callerFQN, relPath, src))
			}
		}

		calleeFQN := buildFQNOf(module, relPath, c.callee)
		if callerFQN == calleeFQN {
			continue
		}

		edgeKey := callerFQN + "→" + calleeFQN
		if seen[edgeKey] {
			continue
		}
		seen[edgeKey] = true

		nodes = append(nodes, calleeStub(calleeFQN))
		edges = append(edges, gtypes.EdgeCalls{CallerFQN: callerFQN, CalleeFQN: calleeFQN, Confidence: "extracted"})
	}

	return nodes, edges
}

// collectSvelteCalls extracts the declarations and CALLS edges of a Svelte
// component from the JavaScript/TypeScript of its inline <script> blocks. The
// Svelte grammar keeps a block's body as an unparsed raw_text node, so each
// block is parsed on its own and its line numbers are shifted back into the
// component; a block that does not parse as JS/TS contributes nothing and never
// fails the file. The template markup is not scanned for calls.
//
// Every declaration of a block is returned as an anchored symbol (not only the
// callers), because the component's own chunks describe the template and never
// name a <script> declaration (AGENTS.md section 2.2: no phantom symbols).
func collectSvelteCalls(module, relPath string, tree *sitter.Tree, src []byte) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls) {
	var calls []chargedCall
	for _, block := range chunking.SvelteScripts(tree, src) {
		nodes = append(nodes, svelteBlockGraph(module, relPath, block, &calls)...)
	}
	callNodes, callEdges := callsToGraph(module, relPath, src, calls)
	return append(nodes, callNodes...), callEdges
}

// svelteBlockGraph turns one <script> block into the component's declaration
// symbols and appends the block's call sites (with their enclosing declaration
// resolved) to calls. StartRow is the row of the block's first content line, so
// a 1-based line inside the block maps to block.StartRow + line in the file.
func svelteBlockGraph(module, relPath string, block chunking.SvelteScript, calls *[]chargedCall) []gtypes.SymbolNode {
	if strings.TrimSpace(block.Content) == "" {
		return nil
	}
	parser, tree, src, ok := chunking.ParseTree(block.Lang, block.Content)
	if !ok {
		return nil
	}
	defer chunking.ReturnParser(block.Lang, parser)
	defer tree.Close()

	chunks := chunking.ChunkCodeWithTree(block.Lang, relPath, block.Content, tree, src)
	var nodes []gtypes.SymbolNode
	callers := make([]chunking.CodeChunk, 0, len(chunks))
	for _, ch := range chunks {
		// The block's preamble and gap chunks are not declarations.
		if ch.Name == "" || ch.NodeType == "preamble" || ch.NodeType == "block" {
			continue
		}
		shifted := ch
		shifted.StartLine += block.StartRow
		shifted.EndLine += block.StartRow
		nodes = append(nodes, gtypes.SymbolNode{
			FQN:     buildFQNOf(module, relPath, ch.Name),
			Kind:    ch.NodeType,
			File:    relPath,
			Name:    ch.Name,
			Line:    int64(shifted.StartLine),
			EndLine: int64(shifted.EndLine),
		})
		switch ch.NodeType {
		case "function", "method", "class":
			callers = append(callers, shifted)
		}
	}
	*calls = append(*calls, chargeCalls(block.Lang, tree, src, callers, block.StartRow)...)
	return nodes
}

// scriptSymbolName is the FQN symbol part of a file's synthetic caller in the
// anchored form. Files of one directory share a package prefix, and a shared
// "<script>" FQN would make the first file win the declaration and drop the
// others' top-level calls, so the file name is part of it ("app.py" gives
// "<script:app_py>"); the symbol's Name stays "<script>".
//
// scriptSymbol is the synthetic caller of top-level calls. It spans the whole
// file (line 1, column 1, offset 0 to the last line).
func scriptSymbolName(relPath string) string {
	return "<script:" + strings.ReplaceAll(path.Base(relPath), ".", "_") + ">"
}

func scriptSymbol(fqn, relPath string, src []byte) gtypes.SymbolNode {
	return gtypes.SymbolNode{
		FQN: fqn, Kind: "script", File: relPath, Name: "<script>",
		Line: 1, StartCol: 1, EndLine: int64(bytes.Count(src, []byte("\n"))) + 1,
	}
}
