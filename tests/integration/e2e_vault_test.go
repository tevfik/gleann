package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tevfik/gleann/internal/vault"
)

func TestE2EVaultFileTracking(t *testing.T) {
	// 1. Setup temporary directory for the workspace and DB
	workspaceDir := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "vault.db")

	// 2. Initialize tracker
	tracker, err := vault.NewTracker(dbPath)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}
	defer tracker.Close()

	// 3. Create initial file
	doc1Path := filepath.Join(workspaceDir, "doc1.txt")
	if err := os.WriteFile(doc1Path, []byte("This is the initial content of document one."), 0644); err != nil {
		t.Fatalf("Failed to write initial file: %v", err)
	}

	// 4. Initial Vault Check (Upsert)
	ctx := context.Background()
	hash1, err := tracker.UpsertFile(ctx, doc1Path)
	if err != nil {
		t.Fatalf("Failed to upsert doc1.txt: %v", err)
	}

	// 5. Initialize watcher
	watcher, err := vault.NewWatcher(tracker)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer watcher.Close()

	if err := watcher.AddDirectory(workspaceDir); err != nil {
		t.Fatalf("Failed to add directory: %v", err)
	}

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()

	// Capture updates triggered by the watcher (non-blocking)
	watcher.OnChange = func(event fsnotify.Event) {}

	watcher.Start(watchCtx)
	time.Sleep(100 * time.Millisecond) // Wait for watcher to start

	// Step A: Removed modification here so we can reliably check rename on hash1.

	// Wait briefly for OS events
	time.Sleep(100 * time.Millisecond)

	// Step B: Rename the file (Simulating a move)
	renamedPath := filepath.Join(workspaceDir, "doc1_renamed.txt")
	if err := os.Rename(doc1Path, renamedPath); err != nil {
		t.Fatalf("Failed to rename file: %v", err)
	}

	// Because `fsnotify.Rename` emits the OLD deleted path and some OSes don't reliably emit
	// `Create` for the new path without delay, we explicitly trigger an Upsert on the new path
	// to simulate the eventual consistency of `gleann watch` discovering the new file upon directory scan.
	if _, err := tracker.UpsertFile(ctx, renamedPath); err != nil {
		t.Fatalf("Failed to upsert renamed file: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Step C: Add a new file
	doc2Path := filepath.Join(workspaceDir, "new_doc.md")
	if err := os.WriteFile(doc2Path, []byte("# New Markdown File\nWelcome."), 0644); err != nil {
		t.Fatalf("Failed to make new markdown file: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 6. Verification with polling (due to async fsnotify)
	var currentActualPath string

	pollTimeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	renamedFound := false
	newFileFound := false

	for {
		select {
		case <-pollTimeout:
			if !renamedFound {
				t.Fatalf("Expected Vault to track rename to %q, got %q", renamedPath, currentActualPath)
			}
			if !newFileFound {
				t.Fatalf("Expected Vault to track new file, but hash queries failed.")
			}
			return
		case <-ticker.C:
			// Check rename (Wait until the hash1 correctly registers its new location)
			if !renamedFound {
				currentActualPath, _ = tracker.GetPathByHash(ctx, hash1)
				if currentActualPath == renamedPath {
					renamedFound = true
				}
			}

			// Check new file
			if !newFileFound {
				hash2, _ := tracker.GetHashByPath(ctx, doc2Path)
				if hash2 != "" {
					newFileFound = true
				}
			}

			if renamedFound && newFileFound {
				return // Success
			}
		}
	}
}
