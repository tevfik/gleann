// Package embedding — model_invalidation.go
// Code-aware cache invalidation: when the embedding model changes between
// index builds, all cached embeddings for that index become meaningless
// (different model = incompatible vector space). This module detects
// model mismatches and purges the stale entries.
package embedding

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ModelChangeResult describes what happened during model-change detection.
type ModelChangeResult struct {
	// PreviousModel is the model that was used to build the index last time.
	PreviousModel string `json:"previous_model"`
	// CurrentModel is the model being used now.
	CurrentModel string `json:"current_model"`
	// Changed is true if the model differs from the last build.
	Changed bool `json:"changed"`
	// InvalidatedL2 is the number of L2 disk cache files removed.
	InvalidatedL2 int `json:"invalidated_l2"`
	// InvalidatedMemo is the number of chunk memo entries removed.
	InvalidatedMemo int `json:"invalidated_memo"`
}

// ModelFingerprint computes a short identifier for a model name.
// This is used to compare models across builds without exact string matching
// (handles cases like "bge-m3" vs "BAAI/bge-m3" pointing to the same model).
func ModelFingerprint(model string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(model))))
	return fmt.Sprintf("%x", h[:8])
}

// DetectModelChange checks whether the current embedding model differs from
// the one recorded in the index metadata. Returns the result without modifying anything.
func DetectModelChange(previousModel, currentModel string) bool {
	if previousModel == "" || currentModel == "" {
		return false // can't determine change without both models
	}
	return ModelFingerprint(previousModel) != ModelFingerprint(currentModel)
}

// InvalidateL2CacheForModel scans the L2 disk cache directory and removes
// entries whose cache keys were computed with a different model.
// Since L2 keys are SHA-256(model + \x00 + text), we can't directly tell
// which model produced a key. Instead, we purge the entire L2 directory
// when a model change is detected (safe because L2 is purely a cache).
func InvalidateL2CacheForModel(cacheDir string) (int, error) {
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".gleann", "cache", "embeddings")
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read cache dir: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".bin") {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		if err := os.Remove(path); err == nil {
			removed++
		}
	}
	return removed, nil
}

// InvalidateOnModelChange is the top-level function that performs a full
// model-change check and invalidation cycle:
// 1. Compare previous model (from index meta) with current model
// 2. If changed: purge L2 disk cache + chunk memo for the old model
// 3. Return what happened
func InvalidateOnModelChange(previousModel, currentModel, cacheDir string, memo *ChunkMemoStore) (*ModelChangeResult, error) {
	result := &ModelChangeResult{
		PreviousModel: previousModel,
		CurrentModel:  currentModel,
	}

	if !DetectModelChange(previousModel, currentModel) {
		return result, nil
	}

	result.Changed = true

	// Purge L2 disk cache.
	removed, err := InvalidateL2CacheForModel(cacheDir)
	if err != nil {
		return result, fmt.Errorf("invalidate L2 cache: %w", err)
	}
	result.InvalidatedL2 = removed

	// Purge chunk memo entries for the old model.
	if memo != nil {
		result.InvalidatedMemo = memo.RemoveByModel(previousModel)
		if err := memo.Save(); err != nil {
			return result, fmt.Errorf("save memo after invalidation: %w", err)
		}
	}

	return result, nil
}
