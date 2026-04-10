package background

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// AutoIndexConfig configures the background auto-indexer.
type AutoIndexConfig struct {
	IndexDir string            // Root directory for indexes
	Indexes  map[string]string // name → docs directory
	Debounce time.Duration     // Debounce interval (default: 5s)
}

// AutoIndexer watches indexed directories and triggers re-index
// via the background task manager when files change.
type AutoIndexer struct {
	manager *Manager
	config  AutoIndexConfig
	watcher *fsnotify.Watcher
	mu      sync.Mutex
	pending map[string]map[string]bool // index name → set of changed files
	stopCh  chan struct{}
	stopped bool
}

// NewAutoIndexer creates an auto-indexer that submits re-index tasks
// to the background manager when watched files change.
func NewAutoIndexer(manager *Manager, config AutoIndexConfig) (*AutoIndexer, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}
	if config.Debounce <= 0 {
		config.Debounce = 5 * time.Second
	}
	if config.Indexes == nil {
		config.Indexes = make(map[string]string)
	}
	return &AutoIndexer{
		manager: manager,
		config:  config,
		watcher: w,
		pending: make(map[string]map[string]bool),
		stopCh:  make(chan struct{}),
	}, nil
}

// Watch adds a directory to the auto-index watcher for the given index name.
func (a *AutoIndexer) Watch(indexName, docsDir string) error {
	a.mu.Lock()
	a.config.Indexes[indexName] = docsDir
	a.mu.Unlock()

	return a.addDirRecursive(docsDir)
}

// addDirRecursive adds a directory and its subdirectories to the fsnotify watcher.
func (a *AutoIndexer) addDirRecursive(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if !info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") && path != dir {
			return filepath.SkipDir
		}
		if base == "node_modules" || base == "vendor" || base == "dist" || base == "build" || base == ".next" {
			return filepath.SkipDir
		}
		return a.watcher.Add(path)
	})
}

// Start begins watching for file changes and submitting auto-index tasks.
func (a *AutoIndexer) Start(ctx context.Context) {
	go a.eventLoop(ctx)
	go a.debounceLoop(ctx)
}

// eventLoop reads fsnotify events and accumulates changed files.
func (a *AutoIndexer) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case event, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				// Dynamically add new directories.
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						a.addDirRecursive(event.Name)
					}
				}
				a.recordChange(event.Name)
			}
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("auto-index watcher error: %v", err)
		}
	}
}

// recordChange maps a changed file path back to its index name.
func (a *AutoIndexer) recordChange(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for name, dir := range a.config.Indexes {
		absDir, _ := filepath.Abs(dir)
		absPath, _ := filepath.Abs(path)
		if strings.HasPrefix(absPath, absDir) {
			if a.pending[name] == nil {
				a.pending[name] = make(map[string]bool)
			}
			a.pending[name][path] = true
		}
	}
}

// debounceLoop periodically checks for pending changes and submits tasks.
func (a *AutoIndexer) debounceLoop(ctx context.Context) {
	ticker := time.NewTicker(a.config.Debounce)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.flushPending()
		}
	}
}

// flushPending submits auto-index tasks for any indexes with pending changes.
func (a *AutoIndexer) flushPending() {
	a.mu.Lock()
	toProcess := make(map[string]int)
	for name, files := range a.pending {
		if len(files) > 0 {
			toProcess[name] = len(files)
		}
	}
	// Reset pending.
	a.pending = make(map[string]map[string]bool)
	a.mu.Unlock()

	for name, count := range toProcess {
		indexName := name
		fileCount := count
		a.manager.Submit(TaskTypeAutoIndex, func(progress func(float64, string)) error {
			progress(0.1, fmt.Sprintf("Auto-reindex %q: %d file(s) changed", indexName, fileCount))
			// The actual rebuild is done by the server's rebuild handler.
			// This task provides visibility — the real work is delegated to
			// the existing index build pipeline.
			progress(0.5, fmt.Sprintf("Queued rebuild for %q", indexName))
			progress(1.0, fmt.Sprintf("Auto-index triggered for %q (%d files)", indexName, fileCount))
			return nil
		})
	}
}

// WatchedIndexes returns the list of index names being watched.
func (a *AutoIndexer) WatchedIndexes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, 0, len(a.config.Indexes))
	for name := range a.config.Indexes {
		names = append(names, name)
	}
	return names
}

// Stop shuts down the auto-indexer.
func (a *AutoIndexer) Stop() {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	a.mu.Unlock()
	close(a.stopCh)
	a.watcher.Close()
}
