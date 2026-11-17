package gleann

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/modules/hnsw"
)

// testEmbedder is a deterministic embedding computer for tests. Like a real
// embedder it is safe for concurrent use (SearchMultiple embeds the query of
// every index in parallel).
type testEmbedder struct {
	dim   int
	calls atomic.Int64
}

func (e *testEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
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
		limits:   defaultIncrementalLimits(),
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
	pm := testPassageStore(basePath)
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
	pm2 := testPassageStore(basePath)
	if err := pm2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pm2.Close()

	if pm2.Count() != 5 {
		t.Errorf("expected 5 passages after update, got %d", pm2.Count())
	}

	// Removed passages leave dead vector slots (RFC 005); every live passage
	// keeps a slot below the high-water mark. Check by content.
	if dense, err := pm2.IsDense(); err != nil || !dense {
		t.Fatalf("every passage must have a slot below the high-water mark (dense=%v, %v)", dense, err)
	}
	texts := map[string]bool{}
	for _, p := range pm2.All() {
		texts[p.Text] = true
	}
	for _, gone := range []string{"alpha from file-a", "beta from file-a"} {
		if texts[gone] {
			t.Errorf("old a.md passage %q survived the update", gone)
		}
	}
	for _, kept := range []string{"gamma from file-b", "delta from file-c", "alpha-v2 from file-a", "beta-v2 from file-a", "gamma-v2 from file-a"} {
		if !texts[kept] {
			t.Errorf("passage %q missing after update", kept)
		}
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
	pm := testPassageStore(basePath)
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
	pm := testPassageStore(basePath)
	pm.Load()
	defer pm.Close()

	if pm.Count() != 2 {
		t.Errorf("expected 2 passages after add, got %d", pm.Count())
	}
}
