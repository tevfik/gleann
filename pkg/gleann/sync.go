package gleann

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileState tracks the state of a single file for change detection.
type FileState struct {
	Path     string    `json:"path"`
	Hash     string    `json:"hash"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Passages []int64   `json:"passages"` // Passage IDs from this file.
}

// SyncState tracks file→passage mapping for incremental index updates.
type SyncState struct {
	Files       map[string]*FileState `json:"files"`
	IndexName   string                `json:"index_name"`
	LastSync    time.Time             `json:"last_sync"`
	NextID      int64                 `json:"next_id"` // Next available passage ID.
	TotalFiles  int                   `json:"total_files"`
}

// SyncResult describes changes detected during synchronization.
type SyncResult struct {
	Added    []string // New files.
	Modified []string // Changed files.
	Deleted  []string // Removed files.
}

// FileSynchronizer provides incremental index updates by tracking file changes.
type FileSynchronizer struct {
	stateDir string
}

// NewFileSynchronizer creates a synchronizer with the given state directory.
func NewFileSynchronizer(stateDir string) *FileSynchronizer {
	return &FileSynchronizer{stateDir: stateDir}
}

// LoadState loads the sync state for the given index, or returns a new empty state.
func (fs *FileSynchronizer) LoadState(indexName string) (*SyncState, error) {
	path := fs.statePath(indexName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SyncState{
				Files:     make(map[string]*FileState),
				IndexName: indexName,
				NextID:    0,
			}, nil
		}
		return nil, fmt.Errorf("read sync state: %w", err)
	}

	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse sync state: %w", err)
	}
	if state.Files == nil {
		state.Files = make(map[string]*FileState)
	}
	return &state, nil
}

// SaveState persists the sync state for the given index.
func (fs *FileSynchronizer) SaveState(state *SyncState) error {
	if err := os.MkdirAll(fs.stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sync state: %w", err)
	}

	path := fs.statePath(state.IndexName)
	return os.WriteFile(path, data, 0o644)
}

// DetectChanges scans the given directory and compares against saved state.
// It returns lists of added, modified, and deleted files.
func (fs *FileSynchronizer) DetectChanges(state *SyncState, dir string, extensions []string) (*SyncResult, error) {
	result := &SyncResult{}

	// Build current file set.
	currentFiles := make(map[string]bool)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible entries.
		}
		if info.IsDir() {
			// Skip hidden directories.
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !matchExtensions(path, extensions) {
			return nil
		}

		relPath, _ := filepath.Rel(dir, path)
		currentFiles[relPath] = true

		prev, exists := state.Files[relPath]
		if !exists {
			result.Added = append(result.Added, relPath)
			return nil
		}

		// Quick check: size and mod time.
		if info.Size() != prev.Size || info.ModTime().After(prev.ModTime) {
			hash, hashErr := hashFile(path)
			if hashErr != nil {
				return nil
			}
			if hash != prev.Hash {
				result.Modified = append(result.Modified, relPath)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan directory: %w", err)
	}

	// Find deleted files.
	for relPath := range state.Files {
		if !currentFiles[relPath] {
			result.Deleted = append(result.Deleted, relPath)
		}
	}

	return result, nil
}

// UpdateFileState records the current state of a file and assigns passage IDs.
func (fs *FileSynchronizer) UpdateFileState(state *SyncState, dir, relPath string, numPassages int) error {
	absPath := filepath.Join(dir, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	hash, err := hashFile(absPath)
	if err != nil {
		return fmt.Errorf("hash file: %w", err)
	}

	// Assign passage IDs.
	passages := make([]int64, numPassages)
	for i := 0; i < numPassages; i++ {
		passages[i] = state.NextID
		state.NextID++
	}

	state.Files[relPath] = &FileState{
		Path:     relPath,
		Hash:     hash,
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		Passages: passages,
	}
	state.TotalFiles = len(state.Files)
	state.LastSync = time.Now()
	return nil
}

// RemoveFile removes a file's state and returns its passage IDs for deletion.
func (fs *FileSynchronizer) RemoveFile(state *SyncState, relPath string) []int64 {
	entry, ok := state.Files[relPath]
	if !ok {
		return nil
	}
	ids := entry.Passages
	delete(state.Files, relPath)
	state.TotalFiles = len(state.Files)
	state.LastSync = time.Now()
	return ids
}

// GetPassageIDs returns the passage IDs associated with a file.
func (fs *FileSynchronizer) GetPassageIDs(state *SyncState, relPath string) []int64 {
	entry, ok := state.Files[relPath]
	if !ok {
		return nil
	}
	return entry.Passages
}

// HasChanges returns true if any changes were detected.
func (r *SyncResult) HasChanges() bool {
	return len(r.Added) > 0 || len(r.Modified) > 0 || len(r.Deleted) > 0
}

// TotalChanged returns the total number of changed files.
func (r *SyncResult) TotalChanged() int {
	return len(r.Added) + len(r.Modified) + len(r.Deleted)
}

// statePath returns the file path for storing sync state.
func (fs *FileSynchronizer) statePath(indexName string) string {
	return filepath.Join(fs.stateDir, indexName+".sync.json")
}

// hashFile computes SHA-256 of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// matchExtensions checks if a file matches any of the given extensions.
// If extensions is empty, all files match.
func matchExtensions(path string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range extensions {
		target := e
		if !strings.HasPrefix(target, ".") {
			target = "." + target
		}
		if ext == strings.ToLower(target) {
			return true
		}
	}
	return false
}
