package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/memory"
)

func TestCmdMemoryReflectCLI(t *testing.T) {
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, "mem")
	convDir := filepath.Join(tmpDir, ".gleann", "conversations")

	t.Setenv("HOME", tmpDir)
	t.Setenv("GLEANN_MEMORY_DIR", memDir)
	t.Setenv("GLEANN_CONVERSATIONS_DIR", convDir)

	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convStore := conversations.NewStore(convDir)

	conv := &conversations.Conversation{
		ID:        "conv-cli-reflect-001",
		Title:     "Language Choice",
		CreatedAt: time.Now().Add(-10 * time.Minute),
		UpdatedAt: time.Now(),
		Messages: []conversations.Message{
			{Role: "user", Content: "We standardise on Go 1.25 for all backend services."},
			{Role: "assistant", Content: "Understood, Go 1.25 standard."},
		},
	}
	if err := convStore.Save(conv); err != nil {
		t.Fatalf("Save conv: %v", err)
	}

	// Fake LLM server
	fakeLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt, _ := req["prompt"].(string)

		var resp string
		if prompt != "" && (strings.Contains(prompt, "Summarize this conversation") || strings.Contains(prompt, "summary")) {
			resp = "Standardised on Go 1.25 for backend."
		} else {
			resp = "fact: We standardise on Go 1.25 for all backend services\n"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": resp})
	}))
	defer fakeLLM.Close()
	t.Setenv("OLLAMA_HOST", fakeLLM.URL)

	// 1. Dry run: writes nothing.
	cmdMemory([]string{"reflect", "--dry-run"})

	s, err := memory.OpenStore(filepath.Join(memDir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	blocks, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("dry-run wrote blocks: got %d, want 0", len(blocks))
	}

	// 2. Reflect cycle: applies changes and saves change set.
	cmdMemory([]string{"reflect"})

	blocks, err = s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("reflect did not add block: got %d, want 1", len(blocks))
	}
	addedBlock := blocks[0]

	// Find run ID from added block's metadata.
	runID := addedBlock.Metadata["reflection_run_id"]
	if runID == "" {
		t.Fatal("added block missing reflection_run_id metadata")
	}

	// Verify change set can be fetched from store.
	cs, err := s.GetChangeSet(runID)
	if err != nil || cs == nil {
		t.Fatalf("change set not found: %v", err)
	}

	// 3. Revert: removes exactly the run's blocks.
	cmdMemory([]string{"reflect", "--revert", runID})

	blocksAfter, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocksAfter) != 0 {
		t.Fatalf("revert did not delete block: got %d blocks remaining", len(blocksAfter))
	}

	// 4. Unknown flag rejected with exit status 2.
	if got := runExit(t, func() { cmdMemory([]string{"reflect", "--bogus"}) }); got != 2 {
		t.Errorf("gleann memory reflect --bogus: exit status %d, want 2", got)
	}

	// 5. Conflicting flags rejected with exit status 1.
	if got := runExit(t, func() { cmdMemory([]string{"reflect", "--dry-run", "--revert", "run-123"}) }); got != 1 {
		t.Errorf("gleann memory reflect --dry-run --revert: exit status %d, want 1", got)
	}
}
