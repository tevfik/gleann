package indexer

import (
	"context"
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	csharp "github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/modules/chunking"
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
	chunking.LangJava: {
		Lang: java.GetLanguage(),
		Query: `
(method_invocation
  name: (identifier) @name
) @call
`,
	},
	chunking.LangCSharp: {
		Lang: csharp.GetLanguage(),
		Query: `
(invocation_expression
  function: [
    (identifier) @name
    (member_access_expression name: (identifier) @name)
  ]
) @call
`,
	},
	chunking.LangRuby: {
		Lang: ruby.GetLanguage(),
		Query: `
(call
  method: (identifier) @name) @call
`,
	},
	chunking.LangPHP: {
		Lang: php.GetLanguage(),
		Query: `
[
  (function_call_expression
    function: (name) @name) @call
  (scoped_call_expression
    name: (name) @name) @call
  (member_call_expression
    name: (name) @name) @call
]
`,
	},
}

// compiledQueries holds pre-compiled tree-sitter Query objects (immutable, thread-safe).
var compiledQueries = func() map[chunking.Language]*sitter.Query {
	m := make(map[chunking.Language]*sitter.Query, len(callQueries))
	for lang, tsq := range callQueries {
		q, err := sitter.NewQuery([]byte(tsq.Query), tsq.Lang)
		if err != nil {
			panic(fmt.Sprintf("bad call query for lang %v: %v", lang, err))
		}
		m[lang] = q
	}
	return m
}()

// callParserPools keeps one sync.Pool of *sitter.Parser per Language.
var callParserPools sync.Map // map[chunking.Language]*sync.Pool

func getCallParser(lang chunking.Language) *sitter.Parser {
	tsq := callQueries[lang]
	val, _ := callParserPools.LoadOrStore(lang, &sync.Pool{
		New: func() any {
			p := sitter.NewParser()
			p.SetLanguage(tsq.Lang)
			return p
		},
	})
	return val.(*sync.Pool).Get().(*sitter.Parser)
}

func returnCallParser(lang chunking.Language, p *sitter.Parser) {
	p.Reset()
	if val, ok := callParserPools.Load(lang); ok {
		val.(*sync.Pool).Put(p)
	}
}

// collectTSCallQueries uses tree-sitter to find call edges and returns Cypher
// queries for them (to be batched into a transaction by the caller).
func collectTSCallQueries(idx *Indexer, absPath, relPath, source string, chunks []chunking.CodeChunk) (nodes []kuzu.SymbolNode, edges []kuzu.EdgeCalls, err error) {
	lang := chunking.DetectLanguage(absPath)
	cq, ok := compiledQueries[lang]
	if !ok {
		return nil, nil, nil
	}

	parser := getCallParser(lang)
	defer returnCallParser(lang, parser)

	sourceBytes := []byte(source)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceBytes)
	if err != nil || tree == nil {
		return nil, nil, fmt.Errorf("tree-sitter parse failed")
	}
	defer tree.Close()

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
		edges = append(edges, kuzu.EdgeCalls{CallerFQN: callerFQN, CalleeFQN: calleeFQN})
	}

	return nodes, edges, nil
}
