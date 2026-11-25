package gleann

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
)

// rerankWarner prints each distinct reranker failure once per process. A
// reranker that cannot work (Ollama has no rerank API) fails the same way on
// every query, and a long-running server or a multi-index search would
// otherwise repeat the line for each one.
type rerankWarner struct {
	mu   sync.Mutex
	out  io.Writer
	seen map[string]bool
}

// rerankWarnings is the process-wide warner of rerankStage.
var rerankWarnings = &rerankWarner{out: os.Stderr}

func (w *rerankWarner) warn(err error) {
	msg := err.Error()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[msg] {
		return
	}
	if w.seen == nil {
		w.seen = make(map[string]bool)
	}
	w.seen[msg] = true
	fmt.Fprintf(w.out, "reranker warning: %v (using the original ranking; shown once)\n", err)
}

// rerankStage applies the reranker to the first-stage results according to
// opts (pool size, identifier gate) and returns the top-k in reranker order.
// On a reranker error the first-stage order is kept. (Fusing the reranker
// with the first-stage order by RRF or a score blend was measured and did
// not beat plain replacement; see docs/retrieval-experiments.md.)
func rerankStage(ctx context.Context, rr Reranker, query string, results []SearchResult, topK int, opts SearchConfig) []SearchResult {
	cut := func(rs []SearchResult) []SearchResult {
		if len(rs) > topK {
			return rs[:topK]
		}
		return rs
	}
	if len(results) == 0 {
		return results
	}
	if opts.RerankSkipIdentifiers && IsIdentifierQuery(query) {
		return cut(results)
	}
	pool := results
	p := opts.RerankPool
	if pp, ok := rr.(interface{ PreferredPool() int }); ok && p <= 0 {
		p = pp.PreferredPool()
	}
	if p > 0 && len(pool) > max(p, topK) {
		pool = pool[:max(p, topK)]
	}
	reranked, err := rr.Rerank(ctx, query, pool, len(pool))
	if err != nil {
		rerankWarnings.warn(err)
		return cut(results)
	}
	for i := range reranked {
		reranked[i].Reranked = true
	}
	return cut(reranked)
}

// IsIdentifierQuery reports whether query looks like a code identifier
// rather than a question: a single token ("Redacted", "parse_args",
// "pkg.Func", "Search()") or two tokens of which one is code-shaped
// ("func NewServer").
func IsIdentifierQuery(query string) bool {
	words := strings.Fields(query)
	switch len(words) {
	case 0:
		return false
	case 1:
		return true
	case 2:
		return codeShaped(words[0]) || codeShaped(words[1])
	}
	return false
}

// codeShaped reports whether w contains identifier structure: _ . ( :: or
// an inner upper-case letter (camelCase).
func codeShaped(w string) bool {
	if strings.ContainsAny(w, "_.(:/") {
		return true
	}
	rs := []rune(w)
	for i := 1; i < len(rs); i++ {
		if unicode.IsUpper(rs[i]) && unicode.IsLower(rs[i-1]) {
			return true
		}
	}
	return false
}
