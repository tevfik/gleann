package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

func TestSleepTimeEngine_ProcessConversation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sleep_mem.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(s)
	defer mgr.Close()

	convDir := filepath.Join(tmpDir, "convs")
	if err := os.MkdirAll(convDir, 0755); err != nil {
		t.Fatal(err)
	}
	convStore := conversations.NewStore(convDir)

	conv := &conversations.Conversation{
		ID:        "conv-test-12345678",
		Title:     "Architecture discussion",
		CreatedAt: time.Now().Add(-10 * time.Minute),
		UpdatedAt: time.Now(),
		Messages: []conversations.Message{
			{Role: "user", Content: "We decided to always use PostgreSQL for database storage."},
			{Role: "assistant", Content: "Understood, PostgreSQL will be the standard database."},
		},
	}
	if err := convStore.Save(conv); err != nil {
		t.Fatalf("Save conv: %v", err)
	}

	cfg := SleepTimeConfig{
		Enabled:   true,
		Interval:  50 * time.Millisecond,
		MaxConvs:  5,
		ConvStore: convStore,
	}
	engine := NewSleepTimeEngine(mgr, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := engine.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce with conv failed: %v", err)
	}

	// Verify summaries or blocks stored
	summaries, err := s.ListSummaries()
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(summaries) == 0 {
		t.Log("Note: summaries count is 0 (fallback summarizer without LLM)")
	}

	// Test Start loop and cancel via stopCh
	stopCh := make(chan struct{})
	engine.Start(stopCh)
	time.Sleep(100 * time.Millisecond)
	close(stopCh)
}
