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
// Returns nil, nil if the language has no registered call query
// (e.g. Svelte, Go: Go uses go/ast directly, Svelte falls through).
func collectTSCallsPrecise(module, relPath string, lang chunking.Language, tree *sitter.Tree, sourceBytes []byte, chunks []chunking.CodeChunk) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls) {
	body, ok := callQueryBodies[lang]
	if !ok {
		return nil, nil
	}
	cq := chunking.GetOrCompileQuery(lang, body)
	if cq == nil {
		return nil, nil
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
	scriptAdded := false

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

		// Innermost declaration containing the call; on a tie the later chunk wins (a method after its class on one line).
		callerFQN := ""
		best := -1
		for i, ch := range callers {
			if ch.Name == "" || callLine < ch.StartLine || callLine > ch.EndLine {
				continue
			}
			if best < 0 || ch.EndLine-ch.StartLine <= callers[best].EndLine-callers[best].StartLine {
				best = i
			}
		}
		if best >= 0 {
			callerFQN = buildFQNOf(module, relPath, callers[best].Name)
		}
		if callerFQN == "" {
			callerFQN = buildFQNOf(module, relPath, scriptSymbolName(relPath))
			if !scriptAdded {
				scriptAdded = true
				nodes = append(nodes, scriptSymbol(callerFQN, relPath, sourceBytes))
			}
		}

		calleeFQN := buildFQNOf(module, relPath, calleeName)
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
