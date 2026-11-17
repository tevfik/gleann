package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// setupFakeSummarizer returns a test server that replies with a summary and a fact.
func setupFakeSummarizer(t *testing.T, factLine string) (*httptest.Server, SummarizerConfig) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt, _ := req["prompt"].(string)

		var resp string
		if prompt != "" && (strings.Contains(prompt, "Summarize this conversation") || strings.Contains(prompt, "summary")) {
			resp = "Test conversation summary"
		} else {
			resp = factLine
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": resp})
	}))
	t.Cleanup(srv.Close)

	return srv, SummarizerConfig{
		Provider: "ollama",
		Model:    "test-model",
		BaseURL:  srv.URL,
	}
}

// TestSleepTimeEngine_CursorSurvivesRestart verifies that the lastRun cursor persists
// in SQLite memory_meta across restarts, so a new engine created on the same store
// does not re-process already reflected conversations.
func TestSleepTimeEngine_CursorSurvivesRestart(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cursor_mem.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mgr := NewManager(s)
	defer mgr.Close()

	convDir := filepath.Join(tmpDir, "convs")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convStore := conversations.NewStore(convDir)

	conv := &conversations.Conversation{
		ID:        "conv-restart-001",
		Title:     "Backend Stack",
		CreatedAt: time.Now().Add(-5 * time.Minute),
		UpdatedAt: time.Now().Add(-1 * time.Minute),
		Messages: []conversations.Message{
			{Role: "user", Content: "We use Go for all microservices."},
			{Role: "assistant", Content: "Got it, Go for microservices."},
		},
	}
	if err := convStore.Save(conv); err != nil {
		t.Fatalf("Save conv: %v", err)
	}

	_, sumCfg := setupFakeSummarizer(t, "fact: We use Go for all microservices\n")
	cfg := SleepTimeConfig{
		Enabled:       true,
		MaxConvs:      5,
		ConvStore:     convStore,
		SummarizerCfg: sumCfg,
	}

	// 1. Initial engine runs cycle.
	engine1 := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()
	cs1, err := engine1.Reflect(ctx, false)
	if err != nil {
		t.Fatalf("Reflect engine1 failed: %v", err)
	}
	if len(cs1.BlocksAdded) != 1 {
		t.Fatalf("expected 1 block added by engine1, got %d", len(cs1.BlocksAdded))
	}

	// Verify lastRun cursor is saved in memory_meta.
	cursorVal, err := s.Meta(metaKeySleepTimeLastRun)
	if err != nil || cursorVal == "" {
		t.Fatalf("expected cursor in memory_meta, got %q (err: %v)", cursorVal, err)
	}
	if engine1.LastRun().IsZero() {
		t.Fatal("engine1 lastRun is zero")
	}

	// 2. Simulate restart: create a new engine on the same store.
	engine2 := NewSleepTimeEngine(mgr, cfg)
	if engine2.LastRun().IsZero() {
		t.Fatal("engine2 failed to resume lastRun from memory_meta")
	}
	if engine2.LastRun().Unix() != engine1.LastRun().Unix() {
		t.Fatalf("engine2 lastRun = %v, want %v", engine2.LastRun(), engine1.LastRun())
	}

	// 3. engine2 runs cycle: must NOT re-process the old conversation.
	cs2, err := engine2.Reflect(ctx, false)
	if err != nil {
		t.Fatalf("Reflect engine2 failed: %v", err)
	}
	if len(cs2.BlocksAdded) != 0 {
		t.Fatalf("engine2 re-processed old conversation: %d blocks added", len(cs2.BlocksAdded))
	}
}

// TestSleepTimeEngine_DryRunWritesNothing verifies that --dry-run produces the change set
// but writes nothing to the database (no blocks, no summaries, no cursor advance).
func TestSleepTimeEngine_DryRunWritesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "dryrun_mem.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mgr := NewManager(s)
	defer mgr.Close()

	convDir := filepath.Join(tmpDir, "convs")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convStore := conversations.NewStore(convDir)

	conv := &conversations.Conversation{
		ID:        "conv-dryrun-001",
		Title:     "Deployment targets",
		CreatedAt: time.Now().Add(-5 * time.Minute),
		UpdatedAt: time.Now(),
		Messages: []conversations.Message{
			{Role: "user", Content: "Deployments run on AWS ECS."},
			{Role: "assistant", Content: "Noted, AWS ECS."},
		},
	}
	if err := convStore.Save(conv); err != nil {
		t.Fatalf("Save conv: %v", err)
	}

	_, sumCfg := setupFakeSummarizer(t, "fact: Deployments run on AWS ECS\n")
	cfg := SleepTimeConfig{
		Enabled:       true,
		MaxConvs:      5,
		ConvStore:     convStore,
		SummarizerCfg: sumCfg,
	}

	engine := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()

	// Execute with dryRun = true.
	cs, err := engine.Reflect(ctx, true)
	if err != nil {
		t.Fatalf("Reflect dry-run failed: %v", err)
	}

	// The change set describes what would be added.
	if len(cs.BlocksAdded) != 1 {
		t.Fatalf("expected 1 block in change set, got %d", len(cs.BlocksAdded))
	}

	// Verify nothing was written to the store.
	blocks, err := s.List("")
	if err != nil {
		t.Fatalf("List blocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("dry-run wrote blocks to store: got %d blocks", len(blocks))
	}

	summaries, err := s.ListSummaries()
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("dry-run wrote summaries to store: got %d summaries", len(summaries))
	}

	// Change set is not saved in DB.
	if _, err := s.GetChangeSet(cs.RunID); err == nil {
		t.Fatal("dry-run change set was saved to SQLite database")
	}

	// Cursor was not advanced.
	cursorVal, _ := s.Meta(metaKeySleepTimeLastRun)
	if cursorVal != "" {
		t.Fatalf("dry-run advanced cursor: %q", cursorVal)
	}
}

// TestSleepTimeEngine_RevertRemovesOnlyRunBlocks_OriginalsUntouched verifies that
// revert removes exactly the blocks added by the specified reflection run,
// restores nothing else, and leaves original blocks completely untouched.
func TestSleepTimeEngine_RevertRemovesOnlyRunBlocks_OriginalsUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "revert_mem.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mgr := NewManager(s)
	defer mgr.Close()

	// 1. Seed store with original blocks.
	orig1, err := mgr.Remember("Python 3.12 is used for analytics", "python")
	if err != nil {
		t.Fatalf("Remember orig1: %v", err)
	}
	orig2, err := mgr.Remember("Database standard is MySQL", "database")
	if err != nil {
		t.Fatalf("Remember orig2: %v", err)
	}

	convDir := filepath.Join(tmpDir, "convs")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convStore := conversations.NewStore(convDir)

	// Conversation states MySQL is deprecated and PostgreSQL is recommended/used instead.
	conv := &conversations.Conversation{
		ID:        "conv-revert-001",
		Title:     "Database Migration",
		CreatedAt: time.Now().Add(-5 * time.Minute),
		UpdatedAt: time.Now(),
		Messages: []conversations.Message{
			{Role: "user", Content: "Database standard is not MySQL, we use PostgreSQL."},
			{Role: "assistant", Content: "Understood."},
		},
	}
	if err := convStore.Save(conv); err != nil {
		t.Fatalf("Save conv: %v", err)
	}

	// LLM extracts fact that contradicts orig2.
	_, sumCfg := setupFakeSummarizer(t, "decision: Database standard is not MySQL, we use PostgreSQL\n")
	cfg := SleepTimeConfig{
		Enabled:       true,
		MaxConvs:      5,
		ConvStore:     convStore,
		SummarizerCfg: sumCfg,
	}

	engine := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()

	// 2. Execute reflection run.
	cs, err := engine.Reflect(ctx, false)
	if err != nil {
		t.Fatalf("Reflect failed: %v", err)
	}

	if len(cs.BlocksAdded) != 1 {
		t.Fatalf("expected 1 block added, got %d", len(cs.BlocksAdded))
	}
	if len(cs.BlocksSuperseded) != 1 || cs.BlocksSuperseded[0] != orig2.ID {
		t.Fatalf("expected orig2 (%s) in BlocksSuperseded, got %v", orig2.ID, cs.BlocksSuperseded)
	}

	// Verify all 3 blocks exist in store (originals never overwritten or deleted).
	b1, err := s.Get(orig1.ID)
	if err != nil || b1 == nil {
		t.Fatal("orig1 missing after reflection")
	}
	b2, err := s.Get(orig2.ID)
	if err != nil || b2 == nil {
		t.Fatal("orig2 missing after reflection")
	}
	runBlockID := cs.BlocksAdded[0]
	b3, err := s.Get(runBlockID)
	if err != nil || b3 == nil {
		t.Fatal("new reflection block missing after reflection")
	}

	// Verify change set is stored in DB.
	storedCS, err := s.GetChangeSet(cs.RunID)
	if err != nil || storedCS == nil {
		t.Fatalf("change set not found in DB: %v", err)
	}

	// 3. Revert the reflection run.
	revertedCS, err := engine.Revert(cs.RunID)
	if err != nil {
		t.Fatalf("Revert failed: %v", err)
	}
	if revertedCS.RunID != cs.RunID {
		t.Fatalf("reverted wrong run ID: got %s, want %s", revertedCS.RunID, cs.RunID)
	}

	// Verify the newly added block is REMOVED.
	if _, err := s.Get(runBlockID); err == nil {
		t.Fatalf("block %s should have been deleted by revert", runBlockID)
	}

	// Verify original blocks are TOUCHED NEITHER in existence nor in identity.
	b1After, err := s.Get(orig1.ID)
	if err != nil || b1After == nil {
		t.Fatal("orig1 was deleted by revert!")
	}
	b2After, err := s.Get(orig2.ID)
	if err != nil || b2After == nil {
		t.Fatal("orig2 was deleted by revert!")
	}

	// Verify only the 2 original blocks remain in the store.
	remaining, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected exactly 2 remaining blocks, got %d", len(remaining))
	}

	// Verify change set is cleaned up.
	if _, err := s.GetChangeSet(cs.RunID); err == nil {
		t.Fatal("reverted change set was not deleted from DB")
	}
}

// TestSleepTimeEngine_DeduplicationPreservesOriginalOnRevert verifies that if reflection
// reinforces an existing memory block (dedup), the block is considered an original block
// and is NOT deleted when reverting.
func TestSleepTimeEngine_DeduplicationPreservesOriginalOnRevert(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "dedup_mem.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mgr := NewManager(s)
	defer mgr.Close()

	// Seed original block.
	orig, err := mgr.Remember("Gleann uses pure Go for default build", "architecture")
	if err != nil {
		t.Fatal(err)
	}

	convDir := filepath.Join(tmpDir, "convs")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convStore := conversations.NewStore(convDir)

	conv := &conversations.Conversation{
		ID:        "conv-dedup-001",
		Title:     "Build discussion",
		CreatedAt: time.Now().Add(-5 * time.Minute),
		UpdatedAt: time.Now(),
		Messages: []conversations.Message{
			{Role: "user", Content: "Remember that Gleann uses pure Go for default build."},
		},
	}
	_ = convStore.Save(conv)

	// Exact duplicate.
	_, sumCfg := setupFakeSummarizer(t, "fact: Gleann uses pure Go for default build\n")
	cfg := SleepTimeConfig{
		Enabled:       true,
		MaxConvs:      5,
		ConvStore:     convStore,
		SummarizerCfg: sumCfg,
	}

	engine := NewSleepTimeEngine(mgr, cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}

	// Deduplicated block was not newly added by the run.
	if len(cs.BlocksAdded) != 0 {
		t.Fatalf("expected 0 blocks added for duplicate, got %d", len(cs.BlocksAdded))
	}

	// Revert should leave orig intact.
	_, err = engine.Revert(cs.RunID)
	if err != nil {
		t.Fatal(err)
	}

	remaining, err := s.List("")
	if err != nil || len(remaining) != 1 {
		t.Fatalf("orig block was deleted or corrupted, count=%d", len(remaining))
	}
	if remaining[0].ID != orig.ID {
		t.Fatalf("remaining block ID mismatch: got %s, want %s", remaining[0].ID, orig.ID)
	}
}
