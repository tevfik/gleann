package gleann

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
)

// MultiSearcher searches across multiple loaded indexes concurrently
// and merges results by score. It implements the Searcher interface so
// LeannChat can work with one or many indexes transparently.
type MultiSearcher struct {
	searchers map[string]*LeannSearcher // name → loaded searcher
	names     []string                  // ordered index names
}

// NewMultiSearcher creates a MultiSearcher from pre-loaded searchers.
// The caller is responsible for calling Load() on each searcher before
// passing it here.
func NewMultiSearcher(named map[string]*LeannSearcher) *MultiSearcher {
	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	sort.Strings(names)
	return &MultiSearcher{searchers: named, names: names}
}

// LoadMultiSearcher creates and loads searchers for the given index names.
// This is a convenience constructor that handles Load() for each index.
func LoadMultiSearcher(ctx context.Context, config Config, embedder EmbeddingComputer, names []string) (*MultiSearcher, error) {
	named := make(map[string]*LeannSearcher, len(names))
	for _, name := range names {
		s := NewSearcher(config, embedder)
		if err := s.Load(ctx, name); err != nil {
			// Close any already-opened searchers on error.
			for _, opened := range named {
				opened.Close()
			}
			return nil, fmt.Errorf("load index %q: %w", name, err)
		}
		named[name] = s
	}
	return NewMultiSearcher(named), nil
}

// Search fans out the query to all indexes concurrently and merges
// results by score (highest first). Each result gets an "_index" key
// in its Metadata so the caller can tell which index it came from.
func (ms *MultiSearcher) Search(ctx context.Context, query string, opts ...SearchOption) ([]SearchResult, error) {
	type indexedResults struct {
		name    string
		results []SearchResult
	}

	var (
		mu       sync.Mutex
		allParts []indexedResults
	)

	g, gctx := errgroup.WithContext(ctx)

	for _, name := range ms.names {
		name := name
		s := ms.searchers[name]
		g.Go(func() error {
			results, err := s.Search(gctx, query, opts...)
			if err != nil {
				return fmt.Errorf("search index %q: %w", name, err)
			}
			mu.Lock()
			allParts = append(allParts, indexedResults{name: name, results: results})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Merge all results, tagging each with its source index.
	var merged []SearchResult
	for _, part := range allParts {
		for _, r := range part.results {
			// Copy metadata and add _index tag.
			meta := make(map[string]any, len(r.Metadata)+1)
			for k, v := range r.Metadata {
				meta[k] = v
			}
			meta["_index"] = part.name
			r.Metadata = meta
			merged = append(merged, r)
		}
	}

	// Sort by score descending.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	// Apply topK from options.
	searchOpts := SearchConfig{TopK: 10}
	for _, opt := range opts {
		opt(&searchOpts)
	}
	topK := searchOpts.TopK
	if topK <= 0 {
		topK = 10
	}
	if len(merged) > topK {
		merged = merged[:topK]
	}

	return merged, nil
}

// Close releases all underlying searchers.
func (ms *MultiSearcher) Close() error {
	var errs []error
	for name, s := range ms.searchers {
		if err := s.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %q: %w", name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("multi-searcher close errors: %v", errs)
	}
	return nil
}

// Names returns the ordered list of index names.
func (ms *MultiSearcher) Names() []string {
	return ms.names
}

// Verify at compile time that MultiSearcher satisfies Searcher.
var _ Searcher = (*MultiSearcher)(nil)
