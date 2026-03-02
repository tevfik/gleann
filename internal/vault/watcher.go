package vault

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// Watcher provides near-real-time filesystem updates to the Tracker.
// It watches directories and re-hashes/updates files on modification.
type Watcher struct {
	tracker  *Tracker
	watcher  *fsnotify.Watcher
	paths    map[string]bool
	OnChange func(event fsnotify.Event) // Optional callback for external actions
}

// NewWatcher initializes a new Watcher bound to a Tracker.
func NewWatcher(tracker *Tracker) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		tracker: tracker,
		watcher: w,
		paths:   make(map[string]bool),
	}, nil
}

// AddDirectory recursively adds directories to be watched.
func (w *Watcher) AddDirectory(dir string) error {
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip hidden directories (e.g. .git, .gleann)
			if strings.HasPrefix(filepath.Base(path), ".") && path != dir {
				return filepath.SkipDir
			}
			if err := w.watcher.Add(path); err != nil {
				return err
			}
			w.paths[path] = true
		}
		return nil
	})
	return err
}

// Start watching events and updating the tracker in the background.
func (w *Watcher) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}

				changed := false
				// Handle file modifications/creations -> re-hash and upsert
				// On some operating systems, Rename doesn't explicitly trigger Create immediately for the new path
				// We also watch Rename and Chmod just in case.
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Chmod) {
					info, err := os.Stat(event.Name)
					if err == nil && !info.IsDir() {
						_, err = w.tracker.UpsertFile(ctx, event.Name)
						if err != nil {
							log.Printf("vault watcher: error upserting %s: %v", event.Name, err)
						} else {
							changed = true
						}
					} else if err == nil && info.IsDir() {
						// Dynamically watch newly created directories
						w.AddDirectory(event.Name)
					}
				}

				// Trigger callback if defined and hash was meaningfully updated
				if changed && w.OnChange != nil {
					w.OnChange(event)
				}

				// NOTE: Renames typically trigger Rename on old path and Create on new path.
				// By not deleting immediately on Rename/Remove, the hash persists.
				// Once Create happens, the hash is recomputed and pointed to the new path, restoring the link smoothly!

			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				log.Printf("vault watcher error: %v", err)
			}
		}
	}()
}

// Close the watcher.
func (w *Watcher) Close() error {
	return w.watcher.Close()
}
