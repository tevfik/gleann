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
	"time"

	"github.com/maypok86/otter/v2"
	gleann "github.com/tevfik/gleann/pkg/gleann"
)

// CachedComputer wraps an EmbeddingComputer with a two-tier cache:
// L1: otter in-memory cache (hot, zero I/O)
// L2: disk-based SHA-256 keyed files (cold, persists across restarts)
type CachedComputer struct {
	inner      gleann.EmbeddingComputer
	dir        string // cache directory; "" when memoryOnly
	memoryOnly bool
	batchSize  int // uncached texts computed and persisted per inner call
	mu         sync.RWMutex
	dim        int // vector size seen in this process; guarded by mu
	hits       int
	total      int
	l1         *otter.Cache[string, []float32] // hot in-memory cache
}

// CacheOptions configures the embedding cache.
type CacheOptions struct {
	// Dir is the cache directory (default: ~/.gleann/cache/embeddings/).
	Dir string
	// MemoryCacheSize is the max number of vectors in the L1 in-memory cache.
	// Default: 50000 (~200MB for 1024-dim float32 vectors).
	MemoryCacheSize int
	// BatchSize is how many uncached texts are computed and written to disk
	// per inner call, so a killed build keeps the batches already written.
	// Default: the inner computer's batch size times its concurrency (one
	// parallel wave of requests), or 64 when the inner computer is not a
	// *Computer.
	BatchSize int
	// MemoryOnly keeps the cache in memory: no directory is created or read,
	// nothing is written to disk, and ClearCache only empties the L1 cache.
	// Used for builds that must leave no trace outside their index.
	MemoryOnly bool
}

// NewCachedComputer wraps an existing EmbeddingComputer with 2-tier caching.
func NewCachedComputer(inner gleann.EmbeddingComputer, opts CacheOptions) *CachedComputer {
	dir := opts.Dir
	if opts.MemoryOnly {
		dir = ""
	} else {
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".gleann", "cache", "embeddings")
		}
		_ = os.MkdirAll(dir, 0o755)
		removeStaleTemps(dir)
	}

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

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		// One persisted batch is one "wave" of the inner computer: its
		// request batch size times its concurrency. The inner computer runs
		// that many requests in parallel, so a smaller chunk would serialise
		// them and slow every build; a larger one would lose more work when
		// a build is killed.
		if comp, ok := inner.(*Computer); ok && comp.batchSize > 0 {
			batchSize = comp.batchSize * max(comp.concurrency, 1)
		}
	}
	if batchSize <= 0 {
		batchSize = 64 // computers that don't batch still get crash-safe writes
	}

	return &CachedComputer{
		inner:      inner,
		dir:        dir,
		memoryOnly: opts.MemoryOnly,
		batchSize:  batchSize,
		l1:         l1,
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
			c.noteDim(len(vec))
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

	// Compute uncached texts in batches, persisting each batch (L1 + L2)
	// before the next one starts, so a build killed halfway keeps every
	// vector already computed (RFC 003 §3.3). Result order is unchanged:
	// vectors land at their original input indices.
	for start := 0; start < len(uncached); start += c.batchSize {
		end := start + c.batchSize
		if end > len(uncached) {
			end = len(uncached)
		}
		batchIdx := uncached[start:end]
		batchTexts := make([]string, len(batchIdx))
		for j, idx := range batchIdx {
			batchTexts[j] = texts[idx]
		}

		// Stop between batches when the caller has given up; the batches
		// already written stay on disk.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("computing embeddings: %w", err)
		}
		computed, err := c.inner.Compute(ctx, batchTexts)
		if err != nil {
			// Earlier batches are already on disk; a resumed build skips
			// them instead of recomputing.
			return nil, fmt.Errorf("computing a batch of %d embeddings (%d of %d uncached texts done): %w",
				len(batchTexts), start, len(uncached), err)
		}
		// A short answer would leave nil vectors in the result, which the
		// index builders cannot use.
		if len(computed) != len(batchTexts) {
			return nil, fmt.Errorf("embedder returned %d vectors for %d texts", len(computed), len(batchTexts))
		}

		for j, idx := range batchIdx {
			result[idx] = computed[j]
			c.noteDim(len(computed[j]))
			key := cacheKey(model, texts[idx])
			// A failed write (for example a reader holding the file on
			// Windows) only means this vector is computed again next time.
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
// The inner computer learns its dimension from its first request, so when
// every text is served from the cache it still reports 0; the size of the
// vectors this process has seen is used then.
func (c *CachedComputer) Dimensions() int {
	if d := c.inner.Dimensions(); d > 0 {
		return d
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dim
}

// noteDim records the vector size of the first vector this process sees.
func (c *CachedComputer) noteDim(n int) {
	c.mu.Lock()
	if c.dim == 0 {
		c.dim = n
	}
	c.mu.Unlock()
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
	if c.memoryOnly {
		return nil
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

// saveToDisk writes a float32 vector to a cache file atomically: the bytes
// go to a temp file that is renamed into place, so a killed process never
// leaves a truncated .bin that a later run would load as a wrong vector.
func (c *CachedComputer) saveToDisk(key string, vec []float32) error {
	if c.memoryOnly {
		return nil
	}
	if len(vec) == 0 {
		// Refuse to persist empty vectors — they would poison the cache
		// and cause cryptic zero-dim panics on the next run.
		return fmt.Errorf("refusing to cache empty vector for key %s", key)
	}
	path := filepath.Join(c.dir, key+".bin")
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], uint32FromFloat32(v))
	}
	tmp, err := os.CreateTemp(c.dir, key+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing cache temp file: %w", err)
	}
	_ = tmp.Chmod(0o644) // keep the permissions os.WriteFile used before
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming cache file into place: %w", err)
	}
	return nil
}

// loadFromDisk reads a float32 vector from a cache file.
func (c *CachedComputer) loadFromDisk(key string) ([]float32, error) {
	if c.memoryOnly {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(c.dir, key+".bin")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		// Empty / truncated cache entry — pretend it doesn't exist so the
		// caller recomputes. A previous broken run could have written zero
		// bytes which would otherwise surface as zero-dim vectors and
		// panic the backend.
		return nil, fmt.Errorf("empty cache entry: %s", key)
	}
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("corrupt cache entry: %s", key)
	}
	// When the dimension is known, a file of the wrong length is a
	// truncated or foreign entry — reject it so it is recomputed instead
	// of loading as a wrong vector.
	if dim := c.Dimensions(); dim > 0 && len(data) != dim*4 {
		return nil, fmt.Errorf("corrupt cache entry: %s has %d bytes, expected %d", key, len(data), dim*4)
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

// SetProgressCallback forwards the progress callback to the underlying embedder if supported.
func (c *CachedComputer) SetProgressCallback(cb ProgressCallback) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reporter, ok := c.inner.(interface{ SetProgressCallback(ProgressCallback) }); ok {
		reporter.SetProgressCallback(cb)
	}
}

// removeStaleTemps deletes temp files that a killed process left behind
// between writing and renaming a vector. Only files older than an hour are
// removed, so a write in progress in another process is never touched.
func removeStaleTemps(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	cutoff := time.Now().Add(-time.Hour)
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(m)
		}
	}
}
