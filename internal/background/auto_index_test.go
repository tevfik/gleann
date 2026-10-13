package background

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoIndexer_WatchAndFlush(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	tmpDir, err := os.MkdirTemp("", "auto_index_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{
		Debounce: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()

	// Watch the temp directory as "test-index".
	if err := ai.Watch("test-index", tmpDir); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai.Start(ctx)

	// Create a file in the watched directory.
	testFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + processing with polling (handles FSEvents delay on macOS)
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		for _, task := range manager.List("") {
			if task.Type == TaskTypeAutoIndex {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Error("expected AutoIndex task to be submitted after file change")
	}
}

func TestAutoIndexer_WatchedIndexes(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Stop()

	if len(ai.WatchedIndexes()) != 0 {
		t.Error("expected no watched indexes initially")
	}

	tmpDir, err := os.MkdirTemp("", "auto_index_test2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	if err := ai.Watch("my-index", tmpDir); err != nil {
		t.Fatal(err)
	}
	if len(ai.WatchedIndexes()) != 1 {
		t.Errorf("expected 1 watched index, got %d", len(ai.WatchedIndexes()))
	}
}

func TestAutoIndexer_StopIdempotent(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	ai, err := NewAutoIndexer(manager, AutoIndexConfig{})
	if err != nil {
		t.Fatal(err)
	}

	// Multiple stops should not panic.
	ai.Stop()
	ai.Stop()
}
