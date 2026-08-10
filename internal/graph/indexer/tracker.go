package indexer

import (
	"os"
	"sync"
	"time"
)

// ChangeTracker tracks the modification times of indexed files to support
// incremental indexing based on file system timestamps.
type ChangeTracker struct {
	mu     sync.RWMutex
	mtimes map[string]time.Time
}

// NewChangeTracker creates a new ChangeTracker.
func NewChangeTracker() *ChangeTracker {
	return &ChangeTracker{
		mtimes: make(map[string]time.Time),
	}
}

// IsDirty checks if a file has been modified since it was last indexed.
// If the file is not in the tracker, it is considered dirty.
func (ct *ChangeTracker) IsDirty(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true // Treat errors as dirty to be safe.
	}

	ct.mu.RLock()
	lastTime, exists := ct.mtimes[path]
	ct.mu.RUnlock()

	if !exists {
		return true
	}

	return info.ModTime().After(lastTime)
}

// MarkClean updates the modification time for a file, marking it as indexed.
func (ct *ChangeTracker) MarkClean(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	ct.mu.Lock()
	ct.mtimes[path] = info.ModTime()
	ct.mu.Unlock()
}

// Clear removes all tracked files from the tracker.
func (ct *ChangeTracker) Clear() {
	ct.mu.Lock()
	ct.mtimes = make(map[string]time.Time)
	ct.mu.Unlock()
}
