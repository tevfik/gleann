// Package embedding — chunk_memo.go
// Chunk-level embedding memoization: when a file changes, only the
// chunks whose text actually differs get re-embedded. Unchanged
// chunks (e.g. unmodified functions in a partially-edited file)
// are served from the existing embedding cache, saving 60-90%
// of embedding cost on incremental rebuilds of large indexes.
package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ChunkFingerprint stores the hash of a chunk's text content and its
// embedding model, enabling cache-hit detection at the chunk level.
type ChunkFingerprint struct {
	// TextHash is the SHA-256 hex digest of the chunk text.
	TextHash string `json:"text_hash"`
	// Model is the embedding model name used to compute the vector.
	Model string `json:"model"`
	// Source is the originating file path.
	Source string `json:"source"`
	// ChunkIndex is the position within the source.
	ChunkIndex int `json:"chunk_index"`
	// IndexedAt is when this fingerprint was recorded.
	IndexedAt time.Time `json:"indexed_at"`
}

// ChunkMemoStore persists chunk fingerprints per index, enabling
// rapid detection of which chunks changed when a file is re-indexed.
// Storage: {index_dir}/{name}/{name}.chunk_memo.json
type ChunkMemoStore struct {
	mu           sync.RWMutex
	path         string
	fingerprints map[string]ChunkFingerprint // key = textHash+model
}

// NewChunkMemoStore opens or creates a memo store for the given index.
func NewChunkMemoStore(indexDir, indexName string) *ChunkMemoStore {
	p := filepath.Join(indexDir, indexName, indexName+".chunk_memo.json")
	s := &ChunkMemoStore{
		path:         p,
		fingerprints: make(map[string]ChunkFingerprint),
	}
	s.load()
	return s
}

// NewChunkMemoStoreFromPath creates a memo store at an explicit path (for testing).
func NewChunkMemoStoreFromPath(path string) *ChunkMemoStore {
	s := &ChunkMemoStore{
		path:         path,
		fingerprints: make(map[string]ChunkFingerprint),
	}
	s.load()
	return s
}

// memoKey generates a composite key from text hash + model.
func memoKey(textHash, model string) string {
	return textHash + "|" + model
}

// HashText computes the SHA-256 hex digest of a chunk's text.
func HashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// Has checks whether a chunk with this exact text+model is known.
func (s *ChunkMemoStore) Has(textHash, model string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.fingerprints[memoKey(textHash, model)]
	return ok
}

// Record stores a chunk fingerprint.
func (s *ChunkMemoStore) Record(fp ChunkFingerprint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fingerprints[memoKey(fp.TextHash, fp.Model)] = fp
}

// RemoveBySource removes all fingerprints originating from the given sources.
func (s *ChunkMemoStore) RemoveBySource(sources []string) int {
	srcSet := make(map[string]bool, len(sources))
	for _, src := range sources {
		srcSet[src] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for k, fp := range s.fingerprints {
		if srcSet[fp.Source] {
			delete(s.fingerprints, k)
			removed++
		}
	}
	return removed
}

// Len returns the number of fingerprints in the store.
func (s *ChunkMemoStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.fingerprints)
}

// FilterUncached takes a list of chunk texts and a model, and returns
// the indices of texts that do NOT have a cached embedding.
// This is the core of the memoization: only these indices need embedding.
func (s *ChunkMemoStore) FilterUncached(texts []string, model string) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var uncached []int
	for i, text := range texts {
		h := HashText(text)
		if _, ok := s.fingerprints[memoKey(h, model)]; !ok {
			uncached = append(uncached, i)
		}
	}
	return uncached
}

// RecordBatch records fingerprints for a batch of chunks.
func (s *ChunkMemoStore) RecordBatch(texts []string, model, source string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for i, text := range texts {
		h := HashText(text)
		key := memoKey(h, model)
		s.fingerprints[key] = ChunkFingerprint{
			TextHash:   h,
			Model:      model,
			Source:     source,
			ChunkIndex: i,
			IndexedAt:  now,
		}
	}
}

// Save persists the store to disk.
func (s *ChunkMemoStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create memo dir: %w", err)
	}

	data, err := json.MarshalIndent(s.fingerprints, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memo store: %w", err)
	}
	return os.WriteFile(s.path, data, 0o644)
}

// load reads the store from disk. Non-fatal on error.
func (s *ChunkMemoStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var fps map[string]ChunkFingerprint
	if err := json.Unmarshal(data, &fps); err != nil {
		return
	}
	s.fingerprints = fps
}

// Stats returns memo store statistics.
type MemoStats struct {
	TotalFingerprints int            `json:"total_fingerprints"`
	ByModel           map[string]int `json:"by_model"`
	BySource          map[string]int `json:"by_source"`
}

// Stats computes statistics about the memo store.
func (s *ChunkMemoStore) Stats() MemoStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := MemoStats{
		TotalFingerprints: len(s.fingerprints),
		ByModel:           make(map[string]int),
		BySource:          make(map[string]int),
	}
	for _, fp := range s.fingerprints {
		stats.ByModel[fp.Model]++
		stats.BySource[fp.Source]++
	}
	return stats
}

// RemoveByModel removes all fingerprints computed with the given model.
// Returns the number of entries removed.
func (s *ChunkMemoStore) RemoveByModel(model string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for k, fp := range s.fingerprints {
		if fp.Model == model {
			delete(s.fingerprints, k)
			removed++
		}
	}
	return removed
}
