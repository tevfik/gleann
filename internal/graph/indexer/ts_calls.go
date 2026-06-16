//go:build treesitter

package indexer

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tevfik/gleann/internal/graph/kuzu"
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
	chunking.LangKotlin: `
(call_expression
  (simple_identifier) @name) @call
`,
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
	chunking.LangLua: `
(function_call
  name: (identifier) @name) @call
`,
	chunking.LangElixir: `
(call
  target: (identifier) @name) @call
`,
	// Svelte intentionally has no call query registered here. The
	// Svelte tree-sitter grammar uses a node vocabulary that doesn't
	// line up with vanilla JS/TS (e.g. instance_call, "function"
	// field mismatch), and a failed query would block ALL Svelte
	// indexing. Future maintainers: investigate the smacker Svelte
	// grammar and add a narrow, well-tested query if the use case
	// warrants it.
}

// collectTSCallQueries walks an already-parsed tree-sitter tree and returns
// CALLS edges. The caller owns the parser and tree lifetimes: this function
// does NOT close the tree and does NOT access any parser pool. The tree may
// be reused for symbol extraction before being passed in.
//
// Returns nil, nil, nil if the language has no registered call query
// (e.g. Svelte, Go — Go uses go/ast directly, Svelte falls through).
func collectTSCallQueries(idx *Indexer, lang chunking.Language, relPath string, tree *sitter.Tree, sourceBytes []byte, chunks []chunking.CodeChunk) (nodes []kuzu.SymbolNode, edges []kuzu.EdgeCalls, err error) {
	body, ok := callQueryBodies[lang]
	if !ok {
		return nil, nil, nil
	}
	cq := chunking.GetOrCompileQuery(lang, body)
	if cq == nil {
		return nil, nil, nil
	}

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(cq, tree.RootNode())

	var callers []chunking.CodeChunk
	for _, ch := range chunks {
		if ch.NodeType == "function" || ch.NodeType == "method" || ch.NodeType == "class" {
			callers = append(callers, ch)
		}
	}

	seen := make(map[string]bool)

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, sourceBytes)

		var calleeName string
		var callLine int

		for _, cap := range m.Captures {
			name := cq.CaptureNameForId(cap.Index)
			if name == "name" {
				calleeName = cap.Node.Content(sourceBytes)
			}
			if name == "call" {
				callLine = int(cap.Node.StartPoint().Row) + 1
			}
		}
		if calleeName == "" {
			continue
		}

		callerFQN := ""
		for _, ch := range callers {
			if callLine >= ch.StartLine && callLine <= ch.EndLine {
				callerFQN = idx.buildFQN(relPath, ch.Name)
				break
			}
		}
		if callerFQN == "" {
			callerFQN = idx.buildFQN(relPath, "<script>")
			sym := kuzu.SymbolNode{FQN: callerFQN, Kind: "script", File: relPath, Name: "<script>"}
			nodes = append(nodes, sym)
		}

		calleeFQN := idx.buildFQN(relPath, calleeName)
		if callerFQN == calleeFQN {
			continue
		}

		edgeKey := callerFQN + "→" + calleeFQN
		if seen[edgeKey] {
			continue
		}
		seen[edgeKey] = true

		nodes = append(nodes, kuzu_symbol(calleeFQN))
		edges = append(edges, kuzu.EdgeCalls{CallerFQN: callerFQN, CalleeFQN: calleeFQN, Confidence: "extracted"})
	}

	return nodes, edges, nil
}
