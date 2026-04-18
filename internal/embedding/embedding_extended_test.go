package embedding

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// mockComputer satisfies gleann.EmbeddingComputer for unit tests.
type mockComputer struct {
	dim   int
	model string
}

func (m *mockComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, m.dim)
	}
	return result, nil
}

func (m *mockComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, m.dim), nil
}

func (m *mockComputer) Dimensions() int   { return m.dim }
func (m *mockComputer) ModelName() string  { return m.model }

func TestCacheKey(t *testing.T) {
	// Same input should produce same key
	key1 := cacheKey("model-a", "hello world")
	key2 := cacheKey("model-a", "hello world")
	if key1 != key2 {
		t.Error("same input should produce same key")
	}

	// Different model should produce different key
	key3 := cacheKey("model-b", "hello world")
	if key1 == key3 {
		t.Error("different model should produce different key")
	}

	// Different text should produce different key
	key4 := cacheKey("model-a", "goodbye world")
	if key1 == key4 {
		t.Error("different text should produce different key")
	}

	// Key should be hex-encoded
	if len(key1) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("key length = %d, want 64", len(key1))
	}
}

func TestUint32Float32RoundTrip(t *testing.T) {
	values := []float32{0.0, 1.0, -1.0, 3.14, math.SmallestNonzeroFloat32, math.MaxFloat32}
	for _, v := range values {
		u := uint32FromFloat32(v)
		got := float32FromUint32(u)
		if got != v {
			t.Errorf("round-trip failed for %f: got %f", v, got)
		}
	}
}

func TestGetModelTokenLimit(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"bge-m3", 2048},
		{"nomic-embed-text", 2048},
		{"unknown-model", 384}, // default
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := GetModelTokenLimit(tt.model)
			if got != tt.want {
				t.Errorf("GetModelTokenLimit(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestTruncateToTokenLimit(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		maxTokens int
		truncated bool
	}{
		{"no truncation", "short text", 100, false},
		{"empty text", "", 100, false},
		{"zero limit", "any text", 0, false},
		{"negative limit", "any text", -1, false},
		{"truncate long text", string(make([]byte, 10000)), 500, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateToTokenLimit(tt.text, tt.maxTokens)
			if tt.truncated && len(got) >= len(tt.text) {
				t.Errorf("expected truncation, got len %d >= original %d", len(got), len(tt.text))
			}
			if !tt.truncated && got != tt.text {
				t.Error("should not truncate when within limit")
			}
		})
	}
}

func TestCacheSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	inner := &mockComputer{dim: 3, model: "test"}
	cc := NewCachedComputer(inner, CacheOptions{
		Dir: dir,
	})

	// Save a vector
	vec := []float32{1.0, 2.0, 3.0}
	key := cacheKey("test", "hello")
	if err := cc.saveToDisk(key, vec); err != nil {
		t.Fatalf("saveToDisk: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, key+".bin")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}

	// Load it back
	loaded, err := cc.loadFromDisk(key)
	if err != nil {
		t.Fatalf("loadFromDisk: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("loaded length = %d, want 3", len(loaded))
	}
	for i, v := range vec {
		if loaded[i] != v {
			t.Errorf("loaded[%d] = %f, want %f", i, loaded[i], v)
		}
	}
}

func TestCacheClear(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "test.bin"), []byte{1, 2, 3}, 0o644)

	inner := &mockComputer{dim: 3, model: "test"}
	cc := NewCachedComputer(inner, CacheOptions{Dir: dir})

	if err := cc.ClearCache(); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("cache dir should be removed after clear")
	}
}

func TestCacheStats(t *testing.T) {
	inner := &mockComputer{dim: 3, model: "test"}
	cc := NewCachedComputer(inner, CacheOptions{
		Dir: t.TempDir(),
	})

	hits, total := cc.Stats()
	if hits != 0 || total != 0 {
		t.Errorf("initial stats should be 0,0; got %d,%d", hits, total)
	}

	hr := cc.HitRate()
	if hr != 0.0 {
		t.Errorf("initial hit rate should be 0.0, got %f", hr)
	}
}

func TestCacheDimensions(t *testing.T) {
	inner := &mockComputer{dim: 768, model: "bge-m3"}
	cc := NewCachedComputer(inner, CacheOptions{Dir: t.TempDir()})

	if cc.Dimensions() != 768 {
		t.Errorf("Dimensions = %d, want 768", cc.Dimensions())
	}
	if cc.ModelName() != "bge-m3" {
		t.Errorf("ModelName = %q, want bge-m3", cc.ModelName())
	}
}

func TestLoadFromDiskCorrupt(t *testing.T) {
	dir := t.TempDir()
	inner := &mockComputer{dim: 3, model: "test"}
	cc := NewCachedComputer(inner, CacheOptions{Dir: dir})

	// Write corrupt file (not multiple of 4 bytes)
	key := "corrupt-key"
	os.WriteFile(filepath.Join(dir, key+".bin"), []byte{1, 2, 3}, 0o644)

	_, err := cc.loadFromDisk(key)
	if err == nil {
		t.Error("expected error for corrupt cache entry")
	}
}

func TestLoadFromDiskMissing(t *testing.T) {
	dir := t.TempDir()
	inner := &mockComputer{dim: 3, model: "test"}
	cc := NewCachedComputer(inner, CacheOptions{Dir: dir})

	_, err := cc.loadFromDisk("nonexistent")
	if err == nil {
		t.Error("expected error for missing cache entry")
	}
}

func TestResolveOllamaHostExtended(t *testing.T) {
	// Clear env to test default
	orig := os.Getenv("OLLAMA_HOST")
	os.Unsetenv("OLLAMA_HOST")
	defer func() {
		if orig != "" {
			os.Setenv("OLLAMA_HOST", orig)
		}
	}()

	host := resolveOllamaHost()
	if host == "" {
		t.Error("resolveOllamaHost should not return empty")
	}
}

func TestResolveOllamaHostFromEnv(t *testing.T) {
	os.Setenv("OLLAMA_HOST", "http://custom:1234")
	defer os.Unsetenv("OLLAMA_HOST")

	host := resolveOllamaHost()
	if host != "http://custom:1234" {
		t.Errorf("host = %q, want http://custom:1234", host)
	}
}
