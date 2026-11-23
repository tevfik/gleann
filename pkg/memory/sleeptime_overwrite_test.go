package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// Tests for audit item 4.4-fixes: reflection must not overwrite the text of a
// block it did not create, must process conversations oldest first across
// cycles, and must retry a conversation whose LLM call failed instead of
// losing it. All stores are temporary (t.TempDir), all LLM calls go to
// httptest servers; nothing touches the real home directory or network.

// A near-duplicate extraction (the user's text plus extra words) must only
// confirm the user's block, never replace its wording — before or after a
// revert of the reflection run.
func TestSleepTimeEngine_NearDuplicateKeepsUserText(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	e := newSleepEnv(t, "fact: Gleann uses pure Go for the default build now\n")
	const userText = "Gleann uses pure Go for default build"
	orig, err := e.mgr.Remember(userText, "architecture")
	if err != nil {
		t.Fatal(err)
	}
	e.save(t, "conv-overwrite-001", time.Minute, userMsg("Gleann uses pure Go for the default build now."))

	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.BlocksAdded) != 0 {
		t.Fatalf("near duplicate recorded as added: %v", cs.BlocksAdded)
	}
	after, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatalf("user block missing after reflection: %v", err)
	}
	if after.Content != userText {
		t.Errorf("reflection overwrote the user's block: %q became %q", userText, after.Content)
	}
	if after.Confirms < 1 {
		t.Errorf("near duplicate did not confirm the user's block: Confirms = %d", after.Confirms)
	}

	if _, err := engine.Revert(cs.RunID); err != nil {
		t.Fatal(err)
	}
	afterRevert, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatalf("revert deleted the user's block: %v", err)
	}
	if afterRevert.Content != userText {
		t.Errorf("user text not restored after revert: %q, want %q", afterRevert.Content, userText)
	}
	// The revert must also undo the confirmation the run made (review
	// section 5: this assertion was missing). A fresh Remember starts at 0.
	if afterRevert.Confirms != orig.Confirms {
		t.Errorf("revert did not undo the confirmation: Confirms = %d, want %d", afterRevert.Confirms, orig.Confirms)
	}
}

// With more conversations than MaxConvs, cycles must process the oldest
// first and the cursor must stop at the last conversation actually
// processed, so later cycles reach the rest instead of skipping them.
func TestSleepTimeEngine_OldestFirstAcrossCycles(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	e := newSleepEnv(t, "fact: the team standard is Go")
	e.cfg.MaxConvs = 5

	// Save stamps UpdatedAt = time.Now(), so saving in order makes conv 0
	// the oldest and conv 11 the newest; capture the real timestamps.
	updatedAt := make([]time.Time, 12)
	for i := 0; i < 12; i++ {
		c := &conversations.Conversation{
			ID:       fmt.Sprintf("conv-old-%02d", i),
			Title:    fmt.Sprintf("conv-old-%02d", i),
			Messages: []conversations.Message{userMsg("we standardise on Go")},
		}
		if err := e.convs.Save(c); err != nil {
			t.Fatal(err)
		}
		updatedAt[i] = c.UpdatedAt
	}

	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	ctx := context.Background()
	for cycle := 1; cycle <= 3; cycle++ {
		if _, err := engine.Reflect(ctx, false); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		sums, err := e.store.ListSummaries()
		if err != nil {
			t.Fatal(err)
		}
		want := cycle * 5
		if cycle == 3 {
			want = 12
		}
		if len(sums) != want {
			t.Fatalf("cycle %d: %d summaries, want %d (older conversations must not be skipped)", cycle, len(sums), want)
		}
		if cycle == 1 {
			for _, sum := range sums {
				var idx int
				if _, err := fmt.Sscanf(sum.ConversationID, "conv-old-%02d", &idx); err != nil || idx >= 5 {
					t.Errorf("cycle 1 processed %s, want only the five oldest (conv-old-00..04)", sum.ConversationID)
				}
			}
		}
	}
	if got := engine.LastRun(); !got.Equal(updatedAt[11]) {
		t.Errorf("cursor = %v, want the newest processed conversation's UpdatedAt %v", got, updatedAt[11])
	}
}

// summarizerStub serves summary and extraction prompts; extractFail decides
// per extraction prompt whether the model call fails.
func summarizerStub(t *testing.T, fact string, extractFail func(prompt string) bool) SummarizerConfig {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt, _ := req["prompt"].(string)
		if strings.Contains(prompt, "Extract important facts") && extractFail != nil && extractFail(prompt) {
			http.Error(w, "model overloaded", http.StatusServiceUnavailable)
			return
		}
		resp := "Test conversation summary"
		if strings.Contains(prompt, "Extract important facts") {
			resp = fact
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": resp})
	}))
	t.Cleanup(srv.Close)
	return SummarizerConfig{Provider: "ollama", Model: "test-model", BaseURL: srv.URL}
}

// A conversation whose extraction call fails once must be retried on the
// next cycle (also after an engine restart), not skipped forever by a cursor
// that jumped past it.
func TestSleepTimeEngine_FailedConversationRetried(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })

	var extractCalls int32
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool {
		return atomic.AddInt32(&extractCalls, 1) == 1
	})
	convStore := conversations.NewStore(filepath.Join(dir, "convs"))
	if err := convStore.Save(&conversations.Conversation{
		ID: "conv-retry-001", Title: "Deploy",
		Messages: []conversations.Message{userMsg("the deploy target is AWS ECS")},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}

	engine1 := NewSleepTimeEngine(mgr, cfg)
	cs1, err := engine1.Reflect(context.Background(), false)
	if err != nil {
		t.Fatalf("cycle with a failing LLM call must not abort: %v", err)
	}
	if len(cs1.BlocksAdded) != 0 {
		t.Fatalf("failed conversation yielded blocks: %v", cs1.BlocksAdded)
	}
	if !engine1.LastRun().IsZero() {
		t.Errorf("cursor advanced past a failed conversation: %v", engine1.LastRun())
	}

	// Restart: a fresh engine on the same store must retry and succeed.
	engine2 := NewSleepTimeEngine(mgr, cfg)
	cs2, err := engine2.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs2.BlocksAdded) != 1 {
		t.Fatalf("failed conversation was not retried: %d blocks added on the next cycle", len(cs2.BlocksAdded))
	}
}

// Retries are bounded and persisted: a conversation that keeps failing is
// abandoned after maxConvRetries attempts IN TOTAL, and a failure no longer
// blocks the rest of the cycle — the healthy conversation is processed even
// though the older poison one fails. A fresh engine (restart) must not reset
// the count (covered by TestDefect_RestartResetsRetryCount_BlocksForever).
func TestSleepTimeEngine_BoundedRetriesThenMovesOn(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })

	var poisonCalls int32
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool {
		if strings.Contains(prompt, "poison marker") {
			atomic.AddInt32(&poisonCalls, 1)
			return true
		}
		return false
	})
	convStore := conversations.NewStore(filepath.Join(dir, "convs"))
	poison := &conversations.Conversation{
		ID: "conv-poison-001", Title: "Poison",
		Messages: []conversations.Message{userMsg("poison marker: extraction always fails here")},
	}
	if err := convStore.Save(poison); err != nil {
		t.Fatal(err)
	}
	healthy := &conversations.Conversation{
		ID: "conv-healthy-001", Title: "Healthy",
		Messages: []conversations.Message{userMsg("healthy marker: the deploy target is AWS ECS")},
	}
	if err := convStore.Save(healthy); err != nil {
		t.Fatal(err)
	}
	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}

	engine := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()
	// Cycle 1: the healthy conversation is processed even though the older
	// poison one fails; a failure does not stop the rest of the cycle.
	cs, err := engine.Reflect(ctx, false)
	if err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if len(cs.BlocksAdded) != 1 {
		t.Fatalf("cycle 1: the healthy conversation must be processed, got %v", cs.BlocksAdded)
	}
	// Cycles 2 and 3 retry the poison conversation (attempts 2 and 3); it is
	// abandoned after the third total attempt and yields nothing.
	for cycle := 2; cycle <= 3; cycle++ {
		cs, err := engine.Reflect(ctx, false)
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(cs.BlocksAdded) != 0 {
			t.Fatalf("cycle %d: the failing conversation must yield no blocks, got %v", cycle, cs.BlocksAdded)
		}
	}
	// A fourth cycle must not call the model for the abandoned conversation.
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&poisonCalls); got != 3 {
		t.Fatalf("poison extraction calls = %d, want exactly 3 (abandoned after the retry bound)", got)
	}
	if got := engine.LastRun(); !got.Equal(healthy.UpdatedAt) {
		t.Errorf("cursor = %v, want the healthy conversation's UpdatedAt %v", got, healthy.UpdatedAt)
	}
}
