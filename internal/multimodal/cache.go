// Package multimodal — content-hash cache layer.
//
// Bug #3 fix: avoid re-invoking the VLM for files that have not changed.
// We hash file bytes (SHA-256) plus model+prompt-version and cache the
// result JSON under ~/.gleann/cache/multimodal/<sha>.json. Subsequent
// runs short-circuit ProcessFile to return the cached description.
package multimodal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
)

// PromptVersion is bumped whenever the prompt templates change so that
// cached entries from older Gleann versions are invalidated automatically.
const PromptVersion = "v2"

// cacheRoot returns the on-disk cache directory, creating it if needed.
// Honours GLEANN_MULTIMODAL_CACHE_DIR for tests/CI; falls back to
// ~/.gleann/cache/multimodal.
func cacheRoot() string {
	if v := os.Getenv("GLEANN_MULTIMODAL_CACHE_DIR"); v != "" {
		_ = os.MkdirAll(v, 0o755)
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".gleann", "cache", "multimodal")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// cacheDisabled returns true when the user opts out via env.
func cacheDisabled() bool {
	v := os.Getenv("GLEANN_MULTIMODAL_CACHE")
	return v == "0" || v == "off" || v == "false"
}

// fileFingerprint returns SHA-256(content || model || promptVersion || lang).
// Reading the entire file is acceptable because the 50 MB ceiling is already
// enforced by ProcessFile; for hot paths the OS page cache makes this cheap.
func fileFingerprint(path, model, lang string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	h.Write([]byte("|" + model + "|" + PromptVersion + "|" + lang))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// cachedDescription is the on-disk JSON shape for a cached result.
type cachedDescription struct {
	Description string `json:"description"`
	MediaType   int    `json:"media_type"`
	Model       string `json:"model"`
	Lang        string `json:"lang"`
}

// cacheStats tracks hits/misses for observability and tests.
var (
	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
)

// CacheStats returns observed hit/miss counters since process start.
func CacheStats() (hits, misses int64) {
	return cacheHits.Load(), cacheMisses.Load()
}

// ResetCacheStats is a test helper.
func ResetCacheStats() {
	cacheHits.Store(0)
	cacheMisses.Store(0)
}

// loadCached returns a cached result for the fingerprint or false when miss.
func loadCached(fp string) (cachedDescription, bool) {
	if cacheDisabled() || fp == "" {
		return cachedDescription{}, false
	}
	root := cacheRoot()
	if root == "" {
		return cachedDescription{}, false
	}
	data, err := os.ReadFile(filepath.Join(root, fp+".json"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// non-fatal; treat as miss
		}
		cacheMisses.Add(1)
		return cachedDescription{}, false
	}
	var cd cachedDescription
	if err := json.Unmarshal(data, &cd); err != nil {
		cacheMisses.Add(1)
		return cachedDescription{}, false
	}
	cacheHits.Add(1)
	return cd, true
}

// storeCached writes a result to the cache. Errors are swallowed because
// cache failures must never break indexing.
func storeCached(fp string, cd cachedDescription) {
	if cacheDisabled() || fp == "" {
		return
	}
	root := cacheRoot()
	if root == "" {
		return
	}
	data, err := json.Marshal(cd)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(root, fp+".json"), data, 0o644)
}
