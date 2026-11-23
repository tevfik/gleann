// Package memsync mirrors Memory Engine entity writes into a gleann vector
// index (<name>_memory_vectors), so memory entities are searchable through
// the standard search path. It has no CGo dependency and is enabled in every
// build.
package memsync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/tevfik/gleann/pkg/gleann"
)

// LeannSyncer implements VectorSyncer by bridging Memory Engine graph writes
// to a gleann HNSW+BM25 vector index.  When a Memory Engine entity node is
// created or updated with non-empty content, the content is embedded and added
// to the corresponding per-index LeannBuilder so that it becomes searchable
// via gleann's standard /search endpoint.
//
// The syncer operates against a single named index.  The REST/MCP layers
// should create one LeannSyncer per memory pool entry.
type LeannSyncer struct {
	builder  *gleann.LeannBuilder
	name     string // index name (used for AddToIndex/UpdateIndex paths)
	embedder gleann.EmbeddingComputer
	mu       sync.Mutex
}

// LeannSyncerOption configures optional syncer behaviour.
type LeannSyncerOption func(*LeannSyncer)

// NewLeannSyncer creates a VectorSyncer backed by a gleann LeannBuilder.
//
//   - builder: a configured LeannBuilder (embeds + writes to disk).
//   - name:    the index name under which passages are stored.
//   - embedder: embedding computer (used for on-the-fly single-text embedding).
//
// If builder is nil the syncer is effectively a no-op (all calls succeed
// without side-effects) — this preserves backward compatibility when the
// calling process has no embedding infrastructure configured.
func NewLeannSyncer(builder *gleann.LeannBuilder, name string, embedder gleann.EmbeddingComputer) *LeannSyncer {
	return &LeannSyncer{
		builder:  builder,
		name:     name,
		embedder: embedder,
	}
}

// AddContent embeds the given content and appends it to the vector index.
// The nodeID is stored in passage metadata so that graph→vector lookups
// can be resolved during retrieval.
//
// Thread-safe: serialised through an internal mutex because the underlying
// PassageManager does not support concurrent writes.
func (s *LeannSyncer) AddContent(ctx context.Context, nodeID, content string, attrs map[string]any) error {
	if s.builder == nil {
		return nil // no-op when builder not configured
	}
	if content == "" {
		return nil
	}

	meta := map[string]any{
		"memory_node_id": nodeID,
		"source":         "memory_engine:" + nodeID,
	}
	// Merge caller-supplied attributes into metadata.
	for k, v := range attrs {
		if k != "memory_node_id" && k != "source" {
			meta[k] = v
		}
	}

	item := gleann.Item{
		Text:     content,
		Metadata: meta,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.builder.AddToIndex(ctx, s.name, []gleann.Item{item}); err != nil {
		if errors.Is(err, gleann.ErrIndexNotFound) {
			if buildErr := s.builder.Build(ctx, s.name, []gleann.Item{item}); buildErr == nil {
				return nil
			} else {
				return fmt.Errorf("leann syncer: add content for node %q: build index: %w", nodeID, buildErr)
			}
		}
		return fmt.Errorf("leann syncer: add content for node %q: %w", nodeID, err)
	}
	return nil
}

// Content is one entity for AddContents. It is the shared gleann.VectorContent
// so the method also satisfies gleann.VectorBatchSyncer, which is what lets the
// Memory Engine publish a whole inject request in one update.
type Content = gleann.VectorContent

// AddContents embeds several entities and writes them to the vector index in
// one update (one publication instead of one per entity). An entity that is
// already in the index is replaced, so a repeated call adds no duplicates.
// An entity repeated within the batch keeps its last content, matching the
// database upsert (one passage per entity). Only then are entities without
// content skipped: a cleared duplicate must not publish the earlier text it
// replaced.
func (s *LeannSyncer) AddContents(ctx context.Context, contents []Content) error {
	if s.builder == nil {
		return nil
	}
	var items []gleann.Item
	var sources []string
	seen := make(map[string]int, len(contents))
	for _, c := range contents {
		meta := map[string]any{
			"memory_node_id": c.NodeID,
			"source":         "memory_engine:" + c.NodeID,
		}
		for k, v := range c.Attrs {
			if k != "memory_node_id" && k != "source" {
				meta[k] = v
			}
		}
		if i, ok := seen[c.NodeID]; ok {
			items[i].Text, items[i].Metadata = c.Content, meta
			continue
		}
		seen[c.NodeID] = len(items)
		items = append(items, gleann.Item{Text: c.Content, Metadata: meta})
		sources = append(sources, "memory_engine:"+c.NodeID)
	}

	// Deduplication happens first, exactly like the database upsert; only the
	// entity's last effective content decides whether it is published.
	keptItems, keptSources := items[:0], sources[:0]
	for i := range items {
		if items[i].Text == "" {
			continue
		}
		keptItems = append(keptItems, items[i])
		keptSources = append(keptSources, sources[i])
	}
	items, sources = keptItems, keptSources

	if len(items) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.builder.UpdateIndex(ctx, s.name, items, sources)
	if errors.Is(err, gleann.ErrIndexNotFound) {
		err = s.builder.Build(ctx, s.name, items)
	}
	if err != nil {
		return fmt.Errorf("leann syncer: add %d entities: %w", len(items), err)
	}
	return nil
}

// DeleteContent removes all passages whose metadata source matches the
// given nodeID from the vector index.  This uses UpdateIndex with removal
// of the "memory_engine:<nodeID>" source key.
func (s *LeannSyncer) DeleteContent(ctx context.Context, nodeID string) error {
	if s.builder == nil {
		return nil // no-op
	}

	source := "memory_engine:" + nodeID

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.builder.UpdateIndex(ctx, s.name, nil, []string{source}); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("leann syncer: delete content for node %q: %w", nodeID, err)
	}
	return nil
}
