// Package embedding cache provides a content-hash keyed caching layer
// that wraps an EmbeddingComputer to skip recomputation for identical text.
package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/maypok86/otter/v2"
	gleann "github.com/tevfik/gleann/pkg/gleann"
)

// CachedComputer wraps an EmbeddingComputer with a two-tier cache:
// L1: otter in-memory cache (hot, zero I/O)
// L2: disk-based SHA-256 keyed files (cold, persists across restarts)
type CachedComputer struct {
	inner gleann.EmbeddingComputer
	dir   string // cache directory
	mu    sync.RWMutex
	hits  int
	total int
	l1    *otter.Cache[string, []float32] // hot in-memory cache
}

// CacheOptions configures the embedding cache.
type CacheOptions struct {
	// Dir is the cache directory (default: ~/.gleann/cache/embeddings/).
	Dir string
	// MemoryCacheSize is the max number of vectors in the L1 in-memory cache.
	// Default: 50000 (~200MB for 1024-dim float32 vectors).
	MemoryCacheSize int
}

// NewCachedComputer wraps an existing EmbeddingComputer with 2-tier caching.
func NewCachedComputer(inner gleann.EmbeddingComputer, opts CacheOptions) *CachedComputer {
	dir := opts.Dir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".gleann", "cache", "embeddings")
	}
	_ = os.MkdirAll(dir, 0o755)

	memSize := opts.MemoryCacheSize
	if memSize <= 0 {
		memSize = 50000
	}
	l1, err := otter.New[string, []float32](&otter.Options[string, []float32]{
		MaximumSize: memSize,
	})
	if err != nil {
		// Non-fatal: fall back to disk-only cache.
		l1 = nil
	}

	return &CachedComputer{
		inner: inner,
		dir:   dir,
		l1:    l1,
	}
}

// Compute computes embeddings using 2-tier cache: L1 (memory) → L2 (disk) → compute.
func (c *CachedComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	result := make([][]float32, len(texts))
	var uncached []int // indices of texts that need computation

	c.mu.RLock()
	model := c.inner.ModelName()
	c.mu.RUnlock()

	// Check cache for each text: L1 (otter) → L2 (disk).
	for i, text := range texts {
		key := cacheKey(model, text)

		// L1: in-memory cache.
		if c.l1 != nil {
			if vec, ok := c.l1.GetIfPresent(key); ok {
				result[i] = vec
				c.mu.Lock()
				c.hits++
				c.total++
				c.mu.Unlock()
				continue
			}
		}

		// L2: disk cache.
		if vec, err := c.loadFromDisk(key); err == nil {
			result[i] = vec
			// Promote to L1.
			if c.l1 != nil {
				c.l1.Set(key, vec)
			}
			c.mu.Lock()
			c.hits++
			c.total++
			c.mu.Unlock()
		} else {
			uncached = append(uncached, i)
			c.mu.Lock()
			c.total++
			c.mu.Unlock()
		}
	}

	// If everything was cached, return early.
	if len(uncached) == 0 {
		return result, nil
	}

	// Compute uncached texts.
	uncachedTexts := make([]string, len(uncached))
	for j, idx := range uncached {
		uncachedTexts[j] = texts[idx]
	}

	computed, err := c.inner.Compute(ctx, uncachedTexts)
	if err != nil {
		return nil, err
	}

	// Store computed results in L1 + L2 caches and fill result.
	for j, idx := range uncached {
		if j < len(computed) {
			result[idx] = computed[j]
			key := cacheKey(model, texts[idx])
			_ = c.saveToDisk(key, computed[j])
			if c.l1 != nil {
				c.l1.Set(key, computed[j])
			}
		}
	}

	return result, nil
}

// ComputeSingle computes embedding for a single text, using cache.
func (c *CachedComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return vecs[0], nil
}

// Dimensions returns the embedding dimensions from the inner computer.
func (c *CachedComputer) Dimensions() int {
	return c.inner.Dimensions()
}

// ModelName returns the model name from the inner computer.
func (c *CachedComputer) ModelName() string {
	return c.inner.ModelName()
}

// Stats returns cache hit statistics.
func (c *CachedComputer) Stats() (hits, total int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.total
}

// HitRate returns the cache hit percentage.
func (c *CachedComputer) HitRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.total == 0 {
		return 0
	}
	return float64(c.hits) / float64(c.total) * 100
}

// ClearCache removes all cached embeddings from both L1 and L2.
func (c *CachedComputer) ClearCache() error {
	if c.l1 != nil {
		c.l1.InvalidateAll()
	}
	return os.RemoveAll(c.dir)
}

// ── Internal ───────────────────────────────────────────────────

// cacheKey produces a hex-encoded SHA-256 hash of model+text.
func cacheKey(model, text string) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0}) // separator
	h.Write([]byte(text))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// saveToDisk writes a float32 vector to a cache file.
func (c *CachedComputer) saveToDisk(key string, vec []float32) error {
	path := filepath.Join(c.dir, key+".bin")
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], uint32FromFloat32(v))
	}
	return os.WriteFile(path, buf, 0o644)
}

// loadFromDisk reads a float32 vector from a cache file.
func (c *CachedComputer) loadFromDisk(key string) ([]float32, error) {
	path := filepath.Join(c.dir, key+".bin")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("corrupt cache entry: %s", key)
	}
	vec := make([]float32, len(data)/4)
	for i := range vec {
		vec[i] = float32FromUint32(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vec, nil
}

func uint32FromFloat32(f float32) uint32 {
	return math.Float32bits(f)
}

func float32FromUint32(u uint32) float32 {
	return math.Float32frombits(u)
}
