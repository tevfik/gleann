package gleann

import "context"

// Searcher is the common interface for single-index and multi-index search.
// LeannSearcher and MultiSearcher both implement this interface.
// LeannChat accepts a Searcher so it can transparently work with one or many indexes.
type Searcher interface {
	// Search performs a semantic search and returns ranked results.
	Search(ctx context.Context, query string, opts ...SearchOption) ([]SearchResult, error)
	// Close releases resources held by the searcher.
	Close() error
}

// Verify at compile time that LeannSearcher satisfies Searcher.
var _ Searcher = (*LeannSearcher)(nil)

// NullSearcher is a no-op searcher that returns no results.
// Used when no index is available (pure LLM mode without RAG context).
type NullSearcher struct{}

func (NullSearcher) Search(_ context.Context, _ string, _ ...SearchOption) ([]SearchResult, error) {
	return nil, nil
}

func (NullSearcher) Close() error { return nil }
