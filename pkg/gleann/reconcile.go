// Package gleann — reconcile.go
// Target state reconciliation: detect and remove orphaned passages
// that reference source files no longer present on disk. When files
// are deleted outside of watch mode, stale vectors linger in the
// index, polluting search results. Reconcile scans the passage DB,
// cross-references each source against the filesystem, and removes
// passages + vectors for missing sources.
package gleann

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ReconcileResult summarizes what changed during reconciliation.
type ReconcileResult struct {
	// ScannedPassages is how many passages were inspected.
	ScannedPassages int `json:"scanned_passages"`
	// OrphanSources lists source paths that no longer exist on disk.
	OrphanSources []string `json:"orphan_sources"`
	// RemovedPassages is how many passages were cleaned up.
	RemovedPassages int `json:"removed_passages"`
	// RemovedVectors is how many vectors were removed from the index.
	RemovedVectors int `json:"removed_vectors"`
}

// Reconcile detects orphaned passages in an index and removes them.
// docsRoot is the root directory that was originally indexed (e.g. --docs ./src/).
// It compares each passage's metadata["source"] against docsRoot to find
// files that were deleted after indexing.
func (b *LeannBuilder) Reconcile(ctx context.Context, name, docsRoot string) (*ReconcileResult, error) {
	indexDir := filepath.Join(b.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// Load passages.
	pm := NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return nil, fmt.Errorf("load passages: %w", err)
	}

	result := &ReconcileResult{}

	// Scan all passages and find unique sources.
	sourcePassageIDs := make(map[string][]int64) // source → passage IDs
	pm.ForEachPassage(func(p Passage) error {
		result.ScannedPassages++
		src, ok := p.Metadata["source"].(string)
		if !ok || src == "" {
			return nil
		}
		sourcePassageIDs[src] = append(sourcePassageIDs[src], p.ID)
		return nil
	})

	// Close the passage manager before calling UpdateIndex, which opens its own PM.
	pm.Close()

	// Check which sources still exist on disk.
	var orphanSources []string
	for src := range sourcePassageIDs {
		fullPath := filepath.Join(docsRoot, src)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			orphanSources = append(orphanSources, src)
		}
	}

	if len(orphanSources) == 0 {
		return result, nil
	}

	result.OrphanSources = orphanSources

	// Count how many passages will be removed.
	for _, src := range orphanSources {
		result.RemovedPassages += len(sourcePassageIDs[src])
	}
	result.RemovedVectors = result.RemovedPassages

	// Remove orphaned passages and their vectors via UpdateIndex.
	err := b.UpdateIndex(ctx, name, nil, orphanSources)
	if err != nil {
		return result, fmt.Errorf("remove orphan passages: %w", err)
	}

	return result, nil
}

// ReconcilePassageManager checks a PassageManager for orphan sources
// and returns the list of sources that no longer exist on disk.
// This is a read-only check; call Reconcile to actually remove them.
func ReconcilePassageManager(pm *PassageManager, docsRoot string) (orphanSources []string, totalPassages int) {
	seenSources := make(map[string]bool)

	pm.ForEachPassage(func(p Passage) error {
		totalPassages++
		src, ok := p.Metadata["source"].(string)
		if !ok || src == "" {
			return nil
		}
		if _, checked := seenSources[src]; checked {
			return nil
		}
		fullPath := filepath.Join(docsRoot, src)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			seenSources[src] = false
			orphanSources = append(orphanSources, src)
		} else {
			seenSources[src] = true
		}
		return nil
	})
	return
}
