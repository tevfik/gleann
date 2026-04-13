package gleann

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/modules/hnsw"
)

// testEmbedder is a deterministic embedding computer for tests.
type testEmbedder struct {
	dim   int
	calls int
}

func (e *testEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls++
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, e.dim)
		for j := range vec {
			vec[j] = float32(len(text)+j) * 0.01
		}
		result[i] = vec
	}
	return result, nil
}

func (e *testEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *testEmbedder) Dimensions() int   { return e.dim }
func (e *testEmbedder) ModelName() string { return "test-model" }

// hnswAdapter wraps the hnsw module's BackendBuilder to satisfy gleann.BackendBuilder.
type hnswAdapter struct {
	inner hnsw.BackendBuilder
}

func (a *hnswAdapter) Build(ctx context.Context, embeddings [][]float32) ([]byte, error) {
	return a.inner.Build(ctx, embeddings)
}

func (a *hnswAdapter) AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error) {
	return a.inner.AddVectors(ctx, indexData, embeddings, startID)
}

func (a *hnswAdapter) RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error) {
	return a.inner.RemoveVectors(ctx, indexData, ids)
}

func newTestBuilder(t *testing.T, dir string, dim int) (*LeannBuilder, *testEmbedder) {
	t.Helper()
	embedder := &testEmbedder{dim: dim}
	hnswCfg := hnsw.Config{
		HNSWConfig: hnsw.HNSWConfig{M: 4, EfConstruction: 16, EfSearch: 16},
	}
	factory := &hnsw.Factory{}
	return &LeannBuilder{
		config:   Config{IndexDir: dir, Backend: "hnsw"},
		backend:  &hnswAdapter{inner: factory.NewBuilder(hnswCfg)},
		embedder: embedder,
	}, embedder
}

func TestUpdateIndex(t *testing.T) {
	dir := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)

	ctx := context.Background()

	// Step 1: Build initial index with items from two sources.
	initialItems := []Item{
		{Text: "alpha from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "beta from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "gamma from file-b", Metadata: map[string]any{"source": "b.md"}},
		{Text: "delta from file-c", Metadata: map[string]any{"source": "c.md"}},
	}

	if err := builder.Build(ctx, "test", initialItems); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Verify initial state.
	basePath := filepath.Join(dir, "test", "test")
	pm := NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if pm.Count() != 4 {
		t.Fatalf("expected 4 passages, got %d", pm.Count())
	}
	pm.Close()

	// Step 2: UpdateIndex — remove a.md passages and add new ones.
	newItems := []Item{
		{Text: "alpha-v2 from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "beta-v2 from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "gamma-v2 from file-a", Metadata: map[string]any{"source": "a.md"}},
	}

	builder2, _ := newTestBuilder(t, dir, 8)

	if err := builder2.UpdateIndex(ctx, "test", newItems, []string{"a.md"}); err != nil {
		t.Fatalf("update index: %v", err)
	}

	// Verify: should have 2 (b.md + c.md) + 3 (new a.md) = 5 passages.
	pm2 := NewPassageManager(basePath)
	if err := pm2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pm2.Close()

	if pm2.Count() != 5 {
		t.Errorf("expected 5 passages after update, got %d", pm2.Count())
	}

	// Verify old a.md passages are gone (IDs 0, 1).
	_, err := pm2.Get(0)
	if err == nil {
		t.Error("expected old passage 0 to be removed")
	}

	// Verify b.md passage still exists (ID 2).
	p, err := pm2.Get(2)
	if err != nil {
		t.Fatalf("get passage 2: %v", err)
	}
	if p.Text != "gamma from file-b" {
		t.Errorf("expected unchanged b.md passage, got %q", p.Text)
	}
}

func TestUpdateIndexDeleteOnly(t *testing.T) {
	dir := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)

	ctx := context.Background()

	items := []Item{
		{Text: "keep this", Metadata: map[string]any{"source": "keep.md"}},
		{Text: "delete this", Metadata: map[string]any{"source": "delete.md"}},
	}

	if err := builder.Build(ctx, "test2", items); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Update: remove delete.md, add nothing.
	builder2, _ := newTestBuilder(t, dir, 8)
	if err := builder2.UpdateIndex(ctx, "test2", nil, []string{"delete.md"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	basePath := filepath.Join(dir, "test2", "test2")
	pm := NewPassageManager(basePath)
	pm.Load()
	defer pm.Close()

	if pm.Count() != 1 {
		t.Errorf("expected 1 passage after delete, got %d", pm.Count())
	}
}

func TestUpdateIndexAddOnly(t *testing.T) {
	dir := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)

	ctx := context.Background()

	items := []Item{
		{Text: "existing chunk", Metadata: map[string]any{"source": "existing.md"}},
	}

	if err := builder.Build(ctx, "test3", items); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Update: add new file, remove nothing.
	builder2, _ := newTestBuilder(t, dir, 8)
	newItems := []Item{
		{Text: "new chunk", Metadata: map[string]any{"source": "new.md"}},
	}
	if err := builder2.UpdateIndex(ctx, "test3", newItems, nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	basePath := filepath.Join(dir, "test3", "test3")
	pm := NewPassageManager(basePath)
	pm.Load()
	defer pm.Close()

	if pm.Count() != 2 {
		t.Errorf("expected 2 passages after add, got %d", pm.Count())
	}
}
