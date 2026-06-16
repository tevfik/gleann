//go:build cgo && treesitter

package chunking

import (
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
)

// symbolQueryCache holds the compiled "find all top-level semantic
// nodes for this language" query. We use one union query per language
// rather than N separate queries because a single tree-sitter cursor
// pass is materially faster than N cursor passes on a large AST — and
// for our pattern (just collect node start/end + type + name) the
// per-match overhead is dominated by the cursor advance, not the
// pattern count.
var symbolQueryCache sync.Map // map[Language]*sitter.Query

// GetOrCompileSymbolQuery returns (and caches) the compiled symbol-
// extraction query for the given language. The query is a single
// union over every node type registered in nodeTypeRules[lang] plus
// a few language-specific special cases (decorated_definition, etc.).
//
// The query is compiled lazily on first use. Compilation cost is paid
// once per language for the lifetime of the process; subsequent uses
// hit the cache and pay only a single sync.Map.Load.
func GetOrCompileSymbolQuery(lang Language) *sitter.Query {
	if v, ok := symbolQueryCache.Load(lang); ok {
		return v.(*sitter.Query)
	}
	body, ok := buildSymbolQuery(lang)
	if !ok {
		return nil
	}
	q := GetOrCompileQuery(lang, body)
	if q == nil {
		return nil
	}
	actual, _ := symbolQueryCache.LoadOrStore(lang, q)
	return actual.(*sitter.Query)
}

// buildSymbolQuery composes a single S-expression that matches every
// top-level semantic node type for the given language. The captured
// node itself is bound to @sym so callers can read its start/end
// point and child field "name" without an extra walk.
//
// We use S-expression `[A B C]` alternation syntax which is roughly
// equivalent to N separate top-level patterns but executes in one
// cursor pass.
func buildSymbolQuery(lang Language) (string, bool) {
	rules, ok := nodeTypeRules[lang]
	if !ok {
		return "", false
	}
	seen := make(map[string]bool)
	var types []string
	for _, r := range rules {
		if !seen[r.Type] {
			seen[r.Type] = true
			types = append(types, r.Type)
		}
	}
	if len(types) == 0 {
		return "", false
	}
	// Build a single S-expression capturing the whole node.
	// Pattern: [(type_a) (type_b) ...] @sym
	body := "[\n"
	for i, t := range types {
		if i > 0 {
			body += "\n"
		}
		body += "  (" + t + ") @sym"
	}
	body += "\n] @sym\n"
	return body, true
}
