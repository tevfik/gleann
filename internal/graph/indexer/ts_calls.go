package indexer

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/internal/graph/kuzu"
)

type tsQuery struct {
	Lang  *sitter.Language
	Query string
}

var callQueries = map[chunking.Language]tsQuery{
	chunking.LangPython: {
		Lang: python.GetLanguage(),
		Query: `
(call
function: [
(identifier) @name
(attribute attribute: (identifier) @name)
]
) @call
`,
	},
	chunking.LangJavaScript: {
		Lang: javascript.GetLanguage(),
		Query: `
(call_expression
function: [
(identifier) @name
(member_expression property: (property_identifier) @name)
]
) @call
`,
	},
	chunking.LangTypeScript: {
		Lang: typescript.GetLanguage(),
		Query: `
(call_expression
function: [
(identifier) @name
(member_expression property: (property_identifier) @name)
]
) @call
`,
	},
	chunking.LangC: {
		Lang: c.GetLanguage(),
		Query: `
(call_expression
function: (identifier) @name
) @call
`,
	},
	chunking.LangCPP: {
		Lang: cpp.GetLanguage(),
		Query: `
(call_expression
function: [
(identifier) @name
(field_expression field: (field_identifier) @name)
(qualified_identifier name: (identifier) @name)
]
) @call
`,
	},
	chunking.LangRust: {
		Lang: rust.GetLanguage(),
		Query: `
(call_expression
function: [
(identifier) @name
(field_expression field: (field_identifier) @name)
(scoped_identifier name: (identifier) @name)
]
) @call
`,
	},
}

func extractTSCallEdges(idx *Indexer, absPath, relPath, source string, chunks []chunking.CodeChunk) error {
	lang := chunking.DetectLanguage(absPath)
	tsQ, ok := callQueries[lang]
	if !ok {
		return nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(tsQ.Lang)

	sourceBytes := []byte(source)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceBytes)
	if err != nil || tree == nil {
		return fmt.Errorf("tree-sitter parse failed")
	}
	defer tree.Close()

	q, err := sitter.NewQuery([]byte(tsQ.Query), tsQ.Lang)
	if err != nil {
		return fmt.Errorf("invalid tree-sitter query: %w", err)
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()

	qc.Exec(q, tree.RootNode())

	var callers []chunking.CodeChunk
	for _, ch := range chunks {
		if ch.NodeType == "function" || ch.NodeType == "method" || ch.NodeType == "class" {
			callers = append(callers, ch)
		}
	}

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		m = qc.FilterPredicates(m, sourceBytes)

		var calleeName string
		var callLine int

		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			if name == "name" {
				calleeName = c.Node.Content(sourceBytes)
			}
			if name == "call" {
				callLine = int(c.Node.StartPoint().Row) + 1
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
			_ = idx.db.UpsertSymbol(kuzu.SymbolNode{
				FQN:  callerFQN,
				Kind: "script",
				File: relPath,
				Line: 0,
				Name: "<script>",
			})
		}

		calleeFQN := idx.buildFQN(relPath, calleeName)
		if callerFQN == calleeFQN {
			continue
		}

		_ = idx.db.UpsertSymbol(kuzu_symbol(calleeFQN))
		_ = idx.db.AddCalls(callerFQN, calleeFQN)
	}

	return nil
}
