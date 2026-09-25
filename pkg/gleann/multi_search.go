package gleann

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
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
// If names is nil, all accessible indexes in indexDir are searched.
func SearchMultiple(ctx context.Context, config Config, embedder EmbeddingComputer, names []string, query string, opts ...SearchOption) ([]MultiSearchResult, error) {
	tagEnv := os.Getenv("GLEANN_TAGS")
	var allowedTags []string
	if tagEnv != "" {
		for _, t := range strings.Split(tagEnv, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				allowedTags = append(allowedTags, t)
			}
		}
	}

	if len(names) == 0 {
		indexes, err := ListIndexes(config.IndexDir)
		if err != nil {
			return nil, fmt.Errorf("list indexes: %w", err)
		}
		for _, idx := range indexes {
			if !idx.IsMCPExposed() {
				continue
			}
			if len(allowedTags) > 0 && !idx.HasAnyTag(allowedTags) {
				continue
			}
			names = append(names, idx.Name)
		}
	} else {
		var expanded []string
		seen := make(map[string]bool)
		for _, name := range names {
			name = strings.TrimSpace(name)
			if strings.HasPrefix(name, "@") {
				tagName := strings.TrimPrefix(name, "@")
				tagged, err := ListIndexesByTag(config.IndexDir, tagName)
				if err != nil {
					continue
				}
				for _, idx := range tagged {
					if !idx.IsMCPExposed() {
						continue
					}
					if len(allowedTags) > 0 && !idx.HasAnyTag(allowedTags) {
						continue
					}
					if !seen[idx.Name] {
						seen[idx.Name] = true
						expanded = append(expanded, idx.Name)
					}
				}
			} else {
				if meta, err := GetIndexMeta(config.IndexDir, name); err == nil {
					if !meta.IsMCPExposed() {
						continue
					}
					if len(allowedTags) > 0 && !meta.HasAnyTag(allowedTags) {
						continue
					}
				}
				if !seen[name] {
					seen[name] = true
					expanded = append(expanded, name)
				}
			}
		}
		names = expanded
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
