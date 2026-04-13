package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LeannBuilder builds and manages indexes.
// This mirrors Python LEANN's LeannBuilder.
type LeannBuilder struct {
	config   Config
	passages *PassageManager
	backend  BackendBuilder
	embedder EmbeddingComputer
	chunker  Chunker
}

// NewBuilder creates a new LeannBuilder.
func NewBuilder(config Config, embedder EmbeddingComputer) (*LeannBuilder, error) {
	factory, err := GetBackend(config.Backend)
	if err != nil {
		return nil, fmt.Errorf("get backend: %w", err)
	}

	return &LeannBuilder{
		config:   config,
		backend:  factory.NewBuilder(config),
		embedder: embedder,
	}, nil
}

// SetChunker sets a custom chunker for text processing.
func (b *LeannBuilder) SetChunker(chunker Chunker) {
	b.chunker = chunker
}

// Build creates a new index from the given items.
func (b *LeannBuilder) Build(ctx context.Context, name string, items []Item) error {
	if len(items) == 0 {
		return fmt.Errorf("no items to index")
	}

	indexDir := filepath.Join(b.config.IndexDir, name)
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}

	basePath := filepath.Join(indexDir, name)

	// Clean up any old passages database to ensure we start from ID 0.
	os.Remove(basePath + ".passages.db")

	// Initialize passage manager and store passages.
	pm := NewPassageManager(basePath)
	defer pm.Close()
	b.passages = pm

	ids, err := pm.Add(items)
	if err != nil {
		return fmt.Errorf("add passages: %w", err)
	}

	// Extract texts for embedding computation.
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}

	// Compute embeddings.
	embeddings, err := b.embedder.Compute(ctx, texts)
	if err != nil {
		return fmt.Errorf("compute embeddings: %w", err)
	}

	// Build index.
	indexData, err := b.backend.Build(ctx, embeddings)
	if err != nil {
		return fmt.Errorf("build index: %w", err)
	}

	// Write index file.
	indexPath := basePath + ".index"
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}

	// Write metadata.
	meta := IndexMeta{
		Name:           name,
		Backend:        b.config.Backend,
		EmbeddingModel: b.embedder.ModelName(),
		Dimensions:     b.embedder.Dimensions(),
		NumPassages:    len(ids),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        "1.0.0",
	}

	metaPath := basePath + ".meta.json"
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

// UpdateIndex performs an incremental index update: removes old passages for the
// given source paths and adds new items. This is much faster than a full rebuild
// when only a few files have changed.
func (b *LeannBuilder) UpdateIndex(ctx context.Context, name string, newItems []Item, removeSources []string) error {
	indexDir := filepath.Join(b.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// Check if index exists.
	indexPath := basePath + ".index"
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read existing index: %w", err)
	}

	pm := NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return fmt.Errorf("load passages: %w", err)
	}
	defer pm.Close()

	// Step 1: Remove old passages and vectors for changed sources.
	if len(removeSources) > 0 {
		removedIDs, err := pm.RemoveBySource(removeSources)
		if err != nil {
			return fmt.Errorf("remove passages by source: %w", err)
		}
		if len(removedIDs) > 0 {
			indexData, err = b.backend.RemoveVectors(ctx, indexData, removedIDs)
			if err != nil {
				return fmt.Errorf("remove vectors: %w", err)
			}
		}
	}

	// Step 2: Add new items.
	if len(newItems) > 0 {
		ids, err := pm.Add(newItems)
		if err != nil {
			return fmt.Errorf("add passages: %w", err)
		}

		texts := make([]string, len(newItems))
		for i, item := range newItems {
			texts[i] = item.Text
		}

		embeddings, err := b.embedder.Compute(ctx, texts)
		if err != nil {
			return fmt.Errorf("compute embeddings: %w", err)
		}

		indexData, err = b.backend.AddVectors(ctx, indexData, embeddings, ids[0])
		if err != nil {
			return fmt.Errorf("add vectors: %w", err)
		}
	}

	// Write updated index.
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write updated index: %w", err)
	}

	// Update metadata.
	metaPath := basePath + ".meta.json"
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}
	var meta IndexMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("unmarshal metadata: %w", err)
	}
	meta.NumPassages = pm.Count()
	meta.UpdatedAt = time.Now()
	updatedMeta, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, updatedMeta, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

// BuildFromTexts is a convenience method that creates Items from plain texts.
func (b *LeannBuilder) BuildFromTexts(ctx context.Context, name string, texts []string) error {
	items := make([]Item, len(texts))
	for i, text := range texts {
		items[i] = Item{Text: text}
	}
	return b.Build(ctx, name, items)
}

// AddToIndex adds new items to an existing index.
func (b *LeannBuilder) AddToIndex(ctx context.Context, name string, items []Item) error {
	indexDir := filepath.Join(b.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// Load existing passages.
	pm := NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return fmt.Errorf("load passages: %w", err)
	}
	defer pm.Close()

	startID := int64(pm.Count())

	// Add new passages.
	_, err := pm.Add(items)
	if err != nil {
		return fmt.Errorf("add passages: %w", err)
	}

	// Compute embeddings for new items.
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}

	embeddings, err := b.embedder.Compute(ctx, texts)
	if err != nil {
		return fmt.Errorf("compute embeddings: %w", err)
	}

	// Load existing index.
	indexPath := basePath + ".index"
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read existing index: %w", err)
	}

	// Add to index.
	newIndexData, err := b.backend.AddVectors(ctx, indexData, embeddings, startID)
	if err != nil {
		return fmt.Errorf("add vectors: %w", err)
	}

	// Write updated index.
	if err := os.WriteFile(indexPath, newIndexData, 0o644); err != nil {
		return fmt.Errorf("write updated index: %w", err)
	}

	// Update metadata.
	metaPath := basePath + ".meta.json"
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}

	var meta IndexMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("unmarshal metadata: %w", err)
	}

	meta.NumPassages = pm.Count()
	meta.UpdatedAt = time.Now()

	updatedMeta, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, updatedMeta, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}
