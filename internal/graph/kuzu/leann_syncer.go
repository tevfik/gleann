//go:build treesitter

package kuzu

import (
	"context"
	"fmt"
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
		return fmt.Errorf("leann syncer: add content for node %q: %w", nodeID, err)
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
		return fmt.Errorf("leann syncer: delete content for node %q: %w", nodeID, err)
	}
	return nil
}
