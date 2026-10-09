package gleann

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ManifestEntry records metadata and digest for a single indexed source file.
type ManifestEntry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	Hash    string `json:"hash"`
}

// Manifest represents the complete file inventory and Merkle root hash of an index source directory.
type Manifest struct {
	Version   string                   `json:"version"`
	IndexName string                   `json:"index_name"`
	SourceDir string                   `json:"source_dir"`
	CreatedAt time.Time                `json:"created_at"`
	RootHash  string                   `json:"root_hash"`
	Entries   map[string]ManifestEntry `json:"entries"`
}

// ManifestDiff details differences between two manifests.
type ManifestDiff struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Deleted  []string `json:"deleted"`
}

// IsEmpty returns true if there are zero added, modified, or deleted files.
func (d *ManifestDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Modified) == 0 && len(d.Deleted) == 0
}

// ComputeRootHash calculates a deterministic Merkle root hash over all manifest entries.
func ComputeRootHash(entries map[string]ManifestEntry) string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		e := entries[k]
		fmt.Fprintf(h, "%s:%d:%d:%s\n", e.Path, e.Size, e.ModTime, e.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HashFileContent computes SHA-256 for a given file.
func HashFileContent(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// BuildManifest constructs a Manifest for a set of file paths relative to sourceDir.
func BuildManifest(indexName, sourceDir string, eligibleFiles []string) (*Manifest, error) {
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		absSource = sourceDir
	}

	entries := make(map[string]ManifestEntry, len(eligibleFiles))
	for _, f := range eligibleFiles {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}

		rel, err := filepath.Rel(absSource, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		rel = filepath.ToSlash(rel)

		hash, err := HashFileContent(f)
		if err != nil {
			continue
		}

		entries[rel] = ManifestEntry{
			Path:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
			Hash:    hash,
		}
	}

	rootHash := ComputeRootHash(entries)

	return &Manifest{
		Version:   "1.0.0",
		IndexName: indexName,
		SourceDir: absSource,
		CreatedAt: time.Now(),
		RootHash:  rootHash,
		Entries:   entries,
	}, nil
}

// Diff compares the receiver manifest against a newer manifest.
func (m *Manifest) Diff(other *Manifest) ManifestDiff {
	var diff ManifestDiff

	if m == nil && other == nil {
		return diff
	}
	if m == nil {
		for k := range other.Entries {
			diff.Added = append(diff.Added, k)
		}
		sort.Strings(diff.Added)
		return diff
	}
	if other == nil {
		for k := range m.Entries {
			diff.Deleted = append(diff.Deleted, k)
		}
		sort.Strings(diff.Deleted)
		return diff
	}

	// Deleted and Modified
	for path, oldEntry := range m.Entries {
		newEntry, exists := other.Entries[path]
		if !exists {
			diff.Deleted = append(diff.Deleted, path)
		} else if oldEntry.Hash != newEntry.Hash {
			diff.Modified = append(diff.Modified, path)
		}
	}

	// Added
	for path := range other.Entries {
		if _, exists := m.Entries[path]; !exists {
			diff.Added = append(diff.Added, path)
		}
	}

	sort.Strings(diff.Added)
	sort.Strings(diff.Modified)
	sort.Strings(diff.Deleted)

	return diff
}

// QuickDiffDirectory performs fast stat/mtime diffing against an existing manifest,
// computing file hashes only when size or modification timestamps differ.
func QuickDiffDirectory(existing *Manifest, sourceDir string, eligibleFiles []string) (*ManifestDiff, *Manifest, error) {
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		absSource = sourceDir
	}

	newEntries := make(map[string]ManifestEntry, len(eligibleFiles))

	for _, f := range eligibleFiles {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}

		rel, err := filepath.Rel(absSource, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		rel = filepath.ToSlash(rel)

		// Check if we can reuse the existing entry without re-reading
		if existing != nil {
			if oldEntry, ok := existing.Entries[rel]; ok {
				if oldEntry.Size == info.Size() && oldEntry.ModTime == info.ModTime().UnixNano() {
					newEntries[rel] = oldEntry
					continue
				}
			}
		}

		// File is new or changed: compute hash
		hash, err := HashFileContent(f)
		if err != nil {
			continue
		}

		newEntries[rel] = ManifestEntry{
			Path:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
			Hash:    hash,
		}
	}

	indexName := ""
	if existing != nil {
		indexName = existing.IndexName
	}

	newManifest := &Manifest{
		Version:   "1.0.0",
		IndexName: indexName,
		SourceDir: absSource,
		CreatedAt: time.Now(),
		RootHash:  ComputeRootHash(newEntries),
		Entries:   newEntries,
	}

	diff := existing.Diff(newManifest)
	return &diff, newManifest, nil
}

// SaveManifest writes the manifest as formatted JSON.
func SaveManifest(filePath string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// LoadManifest reads and parses a manifest JSON file.
func LoadManifest(filePath string) (*Manifest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
