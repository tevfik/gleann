package gleann

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
)

// MultiSearchResult wraps a SearchResult with the index it came from.
type MultiSearchResult struct {
	SearchResult
	Index string `json:"index"`
}

// SearchMultiple searches across multiple indexes concurrently and merges
// results by score. Each result is tagged with the originating index name.
// If names is nil, all available indexes in indexDir are searched.
func SearchMultiple(ctx context.Context, config Config, embedder EmbeddingComputer, names []string, query string, opts ...SearchOption) ([]MultiSearchResult, error) {
	if len(names) == 0 {
		indexes, err := ListIndexes(config.IndexDir)
		if err != nil {
			return nil, fmt.Errorf("list indexes: %w", err)
		}
		for _, idx := range indexes {
			names = append(names, idx.Name)
		}
	}

	if len(names) == 0 {
		return nil, nil
	}

	// Fan out searches concurrently.
	type indexResults struct {
		name    string
		results []SearchResult
	}

	var (
		mu       sync.Mutex
		allParts []indexResults
	)

	g, gctx := errgroup.WithContext(ctx)

	for _, name := range names {
		name := name
		g.Go(func() error {
			searcher := NewSearcher(config, embedder)
			if err := searcher.Load(gctx, name); err != nil {
				return fmt.Errorf("load index %q: %w", name, err)
			}
			defer searcher.Close()

			results, err := searcher.Search(gctx, query, opts...)
			if err != nil {
				return fmt.Errorf("search index %q: %w", name, err)
			}

			mu.Lock()
			allParts = append(allParts, indexResults{name: name, results: results})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Merge and sort by score.
	var merged []MultiSearchResult
	for _, part := range allParts {
		for _, r := range part.results {
			merged = append(merged, MultiSearchResult{
				SearchResult: r,
				Index:        part.name,
			})
		}
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	// Apply topK from opts if configured.
	searchOpts := config.SearchConfig
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
