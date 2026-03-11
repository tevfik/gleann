package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gleann "github.com/tevfik/gleann/pkg/gleann"
)

// mockEmbeddingComputer is a test double for EmbeddingComputer.
type mockEmbeddingComputer struct {
	model   string
	dim     int
	calls   int
	vectors map[string][]float32
}

func newMockEmbedder(dim int) *mockEmbeddingComputer {
	return &mockEmbeddingComputer{
		model:   "test-model",
		dim:     dim,
		vectors: make(map[string][]float32),
	}
}

func (m *mockEmbeddingComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	m.calls++
	result := make([][]float32, len(texts))
	for i, text := range texts {
		if v, ok := m.vectors[text]; ok {
			result[i] = v
		} else {
			// Generate deterministic vector.
			vec := make([]float32, m.dim)
			for j := range vec {
				vec[j] = float32(len(text)+j) * 0.01
			}
			m.vectors[text] = vec
			result[i] = vec
		}
	}
	return result, nil
}

func (m *mockEmbeddingComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := m.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (m *mockEmbeddingComputer) Dimensions() int   { return m.dim }
func (m *mockEmbeddingComputer) ModelName() string { return m.model }

// Verify mockEmbeddingComputer implements EmbeddingComputer.
var _ gleann.EmbeddingComputer = (*mockEmbeddingComputer)(nil)

func TestCachedComputer_CachesResults(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	ctx := context.Background()

	// First call should hit the inner embedder.
	vecs1, err := cached.Compute(ctx, []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 inner call, got %d", mock.calls)
	}
	if len(vecs1) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs1))
	}

	// Second call with same texts should NOT call inner (all cached).
	vecs2, err := cached.Compute(ctx, []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 1 {
		t.Errorf("expected still 1 inner call, got %d", mock.calls)
	}

	// Vectors should be identical.
	for i := range vecs1 {
		for j := range vecs1[i] {
			if vecs1[i][j] != vecs2[i][j] {
				t.Errorf("cached vector mismatch at [%d][%d]: %f != %f", i, j, vecs1[i][j], vecs2[i][j])
			}
		}
	}
}

func TestCachedComputer_PartialCache(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	ctx := context.Background()

	// Cache "hello".
	_, err := cached.Compute(ctx, []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 1 {
		t.Fatalf("expected 1 call, got %d", mock.calls)
	}

	// Now compute "hello" + "world" — only "world" should hit inner.
	_, err = cached.Compute(ctx, []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 2 {
		t.Errorf("expected 2 calls (partial miss), got %d", mock.calls)
	}
}

func TestCachedComputer_ComputeSingle(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	ctx := context.Background()

	vec, err := cached.ComputeSingle(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 4 {
		t.Errorf("expected 4 dimensions, got %d", len(vec))
	}
}

func TestCachedComputer_Stats(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	ctx := context.Background()

	// No calls yet.
	hits, total := cached.Stats()
	if hits != 0 || total != 0 {
		t.Errorf("expected 0/0 stats, got %d/%d", hits, total)
	}

	// First compute: 2 misses.
	_, _ = cached.Compute(ctx, []string{"a", "b"})
	hits, total = cached.Stats()
	if hits != 0 || total != 2 {
		t.Errorf("expected 0/2 stats, got %d/%d", hits, total)
	}

	// Second compute same texts: 2 hits.
	_, _ = cached.Compute(ctx, []string{"a", "b"})
	hits, total = cached.Stats()
	if hits != 2 || total != 4 {
		t.Errorf("expected 2/4 stats, got %d/%d", hits, total)
	}

	rate := cached.HitRate()
	if rate != 50.0 {
		t.Errorf("expected 50%% hit rate, got %.1f%%", rate)
	}
}

func TestCachedComputer_EmptyInput(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	result, err := cached.Compute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("expected nil for empty input")
	}
	if mock.calls != 0 {
		t.Error("should not call inner for empty input")
	}
}

func TestCachedComputer_Dimensions(t *testing.T) {
	mock := newMockEmbedder(128)
	cached := NewCachedComputer(mock, CacheOptions{Dir: t.TempDir()})

	if cached.Dimensions() != 128 {
		t.Errorf("expected 128, got %d", cached.Dimensions())
	}
}

func TestCachedComputer_ModelName(t *testing.T) {
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: t.TempDir()})

	if cached.ModelName() != "test-model" {
		t.Errorf("expected test-model, got %s", cached.ModelName())
	}
}

func TestCachedComputer_ClearCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: cacheDir})

	ctx := context.Background()

	_, _ = cached.Compute(ctx, []string{"test"})

	// Verify cache file exists.
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) == 0 {
		t.Error("expected cache files after compute")
	}

	// Clear cache.
	if err := cached.ClearCache(); err != nil {
		t.Fatal(err)
	}

	// Verify cache dir is gone.
	_, err := os.Stat(cacheDir)
	if !os.IsNotExist(err) {
		t.Error("expected cache dir to be removed")
	}
}

func TestCacheKey_Deterministic(t *testing.T) {
	k1 := cacheKey("model-a", "hello world")
	k2 := cacheKey("model-a", "hello world")
	if k1 != k2 {
		t.Error("same inputs should produce same key")
	}
}

func TestCacheKey_DifferentModels(t *testing.T) {
	k1 := cacheKey("model-a", "hello")
	k2 := cacheKey("model-b", "hello")
	if k1 == k2 {
		t.Error("different models should produce different keys")
	}
}

func TestDiskPersistence_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	key := cacheKey("test", "data")
	vec := []float32{1.0, 2.5, -3.7, 0.001}

	if err := cached.saveToDisk(key, vec); err != nil {
		t.Fatal(err)
	}

	loaded, err := cached.loadFromDisk(key)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded) != len(vec) {
		t.Fatalf("length mismatch: %d != %d", len(loaded), len(vec))
	}

	for i := range vec {
		if vec[i] != loaded[i] {
			t.Errorf("value mismatch at %d: %f != %f", i, vec[i], loaded[i])
		}
	}
}

func TestDiskPersistence_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	// Write corrupt data (not multiple of 4 bytes).
	key := "corrupt"
	path := filepath.Join(dir, key+".bin")
	_ = os.WriteFile(path, []byte{1, 2, 3}, 0o644)

	_, err := cached.loadFromDisk(key)
	if err == nil {
		t.Error("expected error for corrupt cache entry")
	}
}

func TestDiskPersistence_MissingFile(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	_, err := cached.loadFromDisk("nonexistent")
	if err == nil {
		t.Error("expected error for missing cache file")
	}
}

func TestCachedComputer_ImplementsInterface(t *testing.T) {
	mock := newMockEmbedder(4)
	cached := NewCachedComputer(mock, CacheOptions{Dir: t.TempDir()})

	// Verify CachedComputer satisfies EmbeddingComputer.
	var _ gleann.EmbeddingComputer = cached

	_ = fmt.Sprintf("cached: %v", cached)
}
