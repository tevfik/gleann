//go:build cgo && treesitter

package chunking

import (
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
)

// queryCache holds compiled tree-sitter *sitter.Query objects keyed by
// (Language, query-string-fingerprint). Queries are immutable after
// compilation, so a single instance may be shared across all goroutines
// (tree-sitter guarantees query-Cursor safety, not query-mutation safety,
// and we never mutate after NewQuery).
//
// We use a sync.Map to avoid global lock contention. First-use compilation
// pays the NewQuery cost; subsequent uses hit the map in O(1).
var queryCache sync.Map // map[queryKey]*sitter.Query

// queryKey uniquely identifies a query by language and S-expression body.
type queryKey struct {
	Lang Language
	Body string
}

// GetOrCompileQuery returns the compiled query for the given (lang, body)
// pair, compiling and caching it on first use. Returns (nil, false) on
// a bad query or missing language binding — callers must treat this
// as "use a fallback" rather than crashing. The previous behaviour
// was to panic, which broke the indexer when a future tree-sitter
// grammar renamed a node type that the chunker doesn't know about.
func GetOrCompileQuery(lang Language, body string) *sitter.Query {
	key := queryKey{Lang: lang, Body: body}
	if v, ok := queryCache.Load(key); ok {
		vq := v.(*sitter.Query)
		if vq == nilSentinel {
			return nil
		}
		return vq
	}
	tsLang := treeSitterLanguage(lang)
	if tsLang == nil {
		// Cache the negative result so we don't repeatedly look up
		// the missing binding on every call.
		actual, _ := queryCache.LoadOrStore(key, nilSentinel)
		if q, ok := actual.(*sitter.Query); ok {
			if q == nilSentinel {
				return nil
			}
			return q
		}
		return nil
	}
	q, err := sitter.NewQuery([]byte(body), tsLang)
	if err != nil {
		// Bad query: same cache-the-negative approach.
		actual, _ := queryCache.LoadOrStore(key, nilSentinel)
		if q, ok := actual.(*sitter.Query); ok {
			if q == nilSentinel {
				return nil
			}
			return q
		}
		return nil
	}
	actual, _ := queryCache.LoadOrStore(key, q)
	return actual.(*sitter.Query)
}

// nilSentinel is a marker value that lets us cache negative query
// results (bad query or missing language binding) without confusing
// them with a successful "nil" return from a future implementation.
var nilSentinel = (*sitter.Query)(nil)
