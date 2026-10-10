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
	config     Config
	backend    BackendBuilder
	embedder   EmbeddingComputer
	chunker    Chunker
	progressCb func(phase string, done, total int)
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

// SetProgressCallback registers a callback invoked as indexing phases make progress.
func (b *LeannBuilder) SetProgressCallback(cb func(phase string, done, total int)) {
	b.progressCb = cb
}

// SetChunker sets a custom chunker for text processing.
func (b *LeannBuilder) SetChunker(chunker Chunker) {
	b.chunker = chunker
}

// BuildPassages creates the index directory and stores passages into the passage database,
// immediately writing metadata marking lexical search as ready (Phase 1: Instant Lexical Index).
func (b *LeannBuilder) BuildPassages(name string, items []Item) ([]int64, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no items to index")
	}

	indexDir := filepath.Join(b.config.IndexDir, name)
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}

	basePath := filepath.Join(indexDir, name)

	// Clean up any old passages database to ensure we start from ID 0.
	os.Remove(basePath + ".passages.db")

	// Initialize passage manager and store passages.
	pm := NewPassageManager(basePath)
	ids, err := pm.Add(items)
	_ = pm.Close()
	if err != nil {
		return nil, fmt.Errorf("add passages: %w", err)
	}

	meta := IndexMeta{
		Name:           name,
		Backend:        b.config.Backend,
		EmbeddingModel: "",
		Dimensions:     0,
		NumPassages:    len(ids),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        "1.0.0",
		LexicalOnly:    true,
		VectorReady:    false,
		SignaturesOnly: b.config.ChunkConfig.SignaturesOnly,
	}
	if b.embedder != nil {
		meta.EmbeddingModel = b.embedder.ModelName()
		meta.Dimensions = b.embedder.Dimensions()
	}

	metaPath := basePath + ".meta.json"
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}

	return ids, nil
}

// BuildVectors computes vector embeddings for items and builds the vector index backend,
// updating metadata to mark vectors as ready (Phase 2: Semantic Vector Index).
func (b *LeannBuilder) BuildVectors(ctx context.Context, name string, items []Item, ids []int64) error {
	if b.embedder == nil {
		return fmt.Errorf("no embedder configured for vector build")
	}

	indexDir := filepath.Join(b.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// Extract texts for embedding computation.
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}

	// Wire progress reporting from embedder if supported
	if reporter, ok := b.embedder.(interface{ SetProgressCallback(func(int, int)) }); ok {
		reporter.SetProgressCallback(func(done, total int) {
			if b.progressCb != nil {
				b.progressCb("vector_index", done, total)
			}
		})
	}

	// Compute embeddings.
	embeddings, err := b.embedder.Compute(ctx, texts)
	if err != nil {
		return fmt.Errorf("compute embeddings: %w", err)
	}

	if len(embeddings) == 0 {
		return fmt.Errorf("embedder returned 0 vectors for %d texts", len(texts))
	}
	if len(embeddings[0]) == 0 {
		return fmt.Errorf("embedder returned zero-dimensional vectors (model=%q); check provider configuration", b.embedder.ModelName())
	}
	expectedDim := len(embeddings[0])
	for i, vec := range embeddings {
		if len(vec) != expectedDim {
			return fmt.Errorf("embedding row %d has %d dims, expected %d (text len=%d)", i, len(vec), expectedDim, len(texts[i]))
		}
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

	// Update metadata with vector completion
	_ = UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.EmbeddingModel = b.embedder.ModelName()
		m.Dimensions = expectedDim
		m.VectorReady = true
		m.LexicalOnly = false
		m.SignaturesOnly = b.config.ChunkConfig.SignaturesOnly
		m.UpdatedAt = time.Now()
	})

	return nil
}

// BuildLexicalOnly builds only Phase 1 instant lexical index without computing vector embeddings.
func (b *LeannBuilder) BuildLexicalOnly(name string, items []Item) error {
	_, err := b.BuildPassages(name, items)
	return err
}

// Build creates a new index from the given items executing staged build:
// Phase 1 (Instant Lexical) followed by Phase 2 (Vector Index).
func (b *LeannBuilder) Build(ctx context.Context, name string, items []Item) error {
	ids, err := b.BuildPassages(name, items)
	if err != nil {
		return err
	}
	return b.BuildVectors(ctx, name, items, ids)
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

	// Step 1: Remove old passages and vectors for changed sources.
	var removedIDs []int64
	if len(removeSources) > 0 {
		pm := NewPassageManager(basePath)
		if err := pm.Load(); err != nil {
			return fmt.Errorf("load passages: %w", err)
		}
		ids, err := pm.RemoveBySource(removeSources)
		_ = pm.Close()
		if err != nil {
			return fmt.Errorf("remove passages by source: %w", err)
		}
		removedIDs = ids

		if len(removedIDs) > 0 {
			indexData, err = b.backend.RemoveVectors(ctx, indexData, removedIDs)
			if err != nil {
				return fmt.Errorf("remove vectors: %w", err)
			}
		}
	}

	// Step 2: Add new items.
	if len(newItems) > 0 {
		pm := NewPassageManager(basePath)
		if err := pm.Load(); err != nil {
			return fmt.Errorf("load passages: %w", err)
		}
		ids, err := pm.Add(newItems)
		_ = pm.Close()
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

	readPM := NewReadOnlyPassageManager(basePath)
	if err := readPM.Load(); err == nil {
		meta.NumPassages = readPM.Count()
		_ = readPM.Close()
	}

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
