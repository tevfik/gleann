package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// Repair tests for the independent review of the memory-fixes patch. Each of
// these reproduces a defect in the first patch and must stay green after the
// repair. All stores are temporary (t.TempDir) and all LLM calls go to
// httptest servers; nothing touches the real home directory or network.

// F3 — Reflect can run from the background cycle and from a REST/CLI call at
// once. The first patch left convFailures and lastRun unprotected (a data
// race) and let two cycles run concurrently. A concurrent Reflect must be
// rejected with a clear "already running" answer instead of racing.
func TestSleepTimeEngine_ConcurrentReflectSerialized(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })
	convStore := conversations.NewStore(filepath.Join(dir, "convs"))
	if err := convStore.Save(&conversations.Conversation{
		ID: "conv-race-0001", Title: "Race",
		Messages: []conversations.Message{userMsg("the deploy target is AWS")},
	}); err != nil {
		t.Fatal(err)
	}

	// The first extraction call blocks until released; later calls fail fast.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt, _ := req["prompt"].(string)
		if strings.Contains(prompt, "Extract important facts") {
			first := false
			once.Do(func() { first = true; close(entered) })
			if first {
				<-release
			}
			http.Error(w, "model overloaded", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": "Test conversation summary"})
	}))
	t.Cleanup(srv.Close)
	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore,
		SummarizerCfg: SummarizerConfig{Provider: "ollama", Model: "test-model", BaseURL: srv.URL}}
	engine := NewSleepTimeEngine(mgr, cfg)

	var wg sync.WaitGroup
	var err1 error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err1 = engine.Reflect(context.Background(), false)
	}()
	<-entered // the first Reflect is now mid-cycle

	done := make(chan error, 1)
	go func() {
		_, e := engine.Reflect(context.Background(), false)
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil || !strings.Contains(e.Error(), "already running") {
			t.Errorf("concurrent Reflect = %v, want an 'already running' error", e)
		}
	case <-time.After(5 * time.Second):
		t.Error("concurrent Reflect blocked instead of returning 'already running'")
	}
	close(release)
	wg.Wait()
	if err1 != nil {
		t.Fatalf("first Reflect: %v", err1)
	}
}

// F3 — several concurrent Reflect calls on one engine must not race on the
// engine's shared state (the first patch wrote convFailures and lastRun from
// every caller without synchronization). Run with -race.
func TestSleepTimeEngine_ConcurrentReflectDataRace(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })
	convStore := conversations.NewStore(filepath.Join(dir, "convs"))
	if err := convStore.Save(&conversations.Conversation{
		ID: "conv-race-0002", Title: "Race",
		Messages: []conversations.Message{userMsg("the deploy target is AWS")},
	}); err != nil {
		t.Fatal(err)
	}
	// Extraction always fails fast, so every caller reaches the shared
	// failure-count state.
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool { return true })
	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}
	engine := NewSleepTimeEngine(mgr, cfg)

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				_, _ = engine.Reflect(context.Background(), false)
			}
		}()
	}
	wg.Wait()
}

// F4 — a failed conversation whose UpdatedAt equals a processed one's must be
// retried, not stranded by a cursor that moved to the shared timestamp.
func TestDefect_EqualTimestampDropsFailedConversation(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })
	convDir := filepath.Join(dir, "convs")
	convStore := conversations.NewStore(convDir)

	var poisonCalls int32
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool {
		if strings.Contains(prompt, "poison marker") {
			atomic.AddInt32(&poisonCalls, 1)
			return true
		}
		return false
	})

	// Save re-stamps UpdatedAt, so force both conversations to the SAME
	// timestamp by rewriting their JSON files. The good one sorts first.
	shared := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	saveAt := func(id, content string) {
		c := &conversations.Conversation{ID: id, Title: id, Messages: []conversations.Message{userMsg(content)}}
		if err := convStore.Save(c); err != nil {
			t.Fatal(err)
		}
		c.UpdatedAt = shared
		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(convDir, id+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saveAt("conv-eq-00-good", "the deploy target is AWS ECS")
	saveAt("conv-eq-01-poison", "poison marker: extraction always fails here")

	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}
	engine := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()
	// Cycle 1 processes the good conversation (cursor -> shared) and fails the
	// poison one. Cycle 2 must still retry the poison conversation.
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&poisonCalls); got < 2 {
		t.Fatalf("poison extraction called %d times, want >=2: a failed conversation with the same UpdatedAt as a processed one must be retried", got)
	}
}

// F4 — clock going backwards must not strand a failed conversation either: a
// pending retry stays eligible even when its timestamp is before the cursor.
func TestDefect_ClockBackwardsDoesNotStrandFailedConversation(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })
	convDir := filepath.Join(dir, "convs")
	convStore := conversations.NewStore(convDir)

	var poisonCalls int32
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool {
		if strings.Contains(prompt, "poison marker") {
			atomic.AddInt32(&poisonCalls, 1)
			return true
		}
		return false
	})
	// A newer conversation that succeeds, and an older one (clock went
	// backwards) that fails.
	now := time.Now().UTC().Truncate(time.Millisecond)
	saveAt := func(id, content string, ts time.Time) {
		c := &conversations.Conversation{ID: id, Title: id, Messages: []conversations.Message{userMsg(content)}}
		if err := convStore.Save(c); err != nil {
			t.Fatal(err)
		}
		c.UpdatedAt = ts
		data, _ := json.MarshalIndent(c, "", "  ")
		if err := os.WriteFile(filepath.Join(convDir, id+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saveAt("conv-back-new", "the deploy target is AWS ECS", now)
	saveAt("conv-back-old", "poison marker: extraction always fails here", now.Add(-time.Hour))

	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}
	engine := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&poisonCalls); got < 2 {
		t.Fatalf("poison extraction called %d times, want >=2: a failed conversation older than the cursor (clock backwards) must still be retried", got)
	}
}

// F5 — retry counts must persist across restarts. `gleann memory reflect`
// creates a fresh engine each run; a conversation that always fails must be
// abandoned after 3 attempts IN TOTAL and must not block later conversations.
func TestDefect_RestartResetsRetryCount_BlocksForever(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })
	convStore := conversations.NewStore(filepath.Join(dir, "convs"))

	var poisonCalls int32
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool {
		if strings.Contains(prompt, "poison marker") {
			atomic.AddInt32(&poisonCalls, 1)
			return true
		}
		return false
	})
	// Poison is older (saved first), healthy newer.
	if err := convStore.Save(&conversations.Conversation{
		ID: "conv-poison-001", Title: "Poison",
		Messages: []conversations.Message{userMsg("poison marker: extraction always fails here")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := convStore.Save(&conversations.Conversation{
		ID: "conv-healthy-001", Title: "Healthy",
		Messages: []conversations.Message{userMsg("healthy marker: the deploy target is AWS ECS")},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}
	ctx := context.Background()
	// A fresh engine every cycle simulates repeated `gleann memory reflect`.
	for cycle := 1; cycle <= 4; cycle++ {
		engine := NewSleepTimeEngine(mgr, cfg)
		if _, err := engine.Reflect(ctx, false); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
	}
	blocks, err := s.List(TierLong)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("healthy conversation never processed: a failing conversation blocks the queue across restarts")
	}
	if got := atomic.LoadInt32(&poisonCalls); got != 3 {
		t.Fatalf("poison extraction calls = %d, want exactly 3 (abandoned after 3 total attempts persisted across restarts)", got)
	}
}

// F5 — an old database that has the cursor but no per-conversation progress
// record must still open and reflect (no schema migration required).
func TestSleepTimeEngine_OldDatabaseWithoutProgressOpens(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	e := newSleepEnv(t, "fact: the build uses Go")
	// Simulate an old database: a cursor exists, but no progress key.
	if err := e.store.SetMeta(metaKeySleepTimeLastRun, time.Now().Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	e.save(t, "conv-old-db-0001", time.Minute, userMsg("we build with Go"))
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatalf("old database without a progress record failed to reflect: %v", err)
	}
	if len(cs.BlocksAdded) != 1 {
		t.Fatalf("BlocksAdded = %v, want the single fact from the old database", cs.BlocksAdded)
	}
}

// F6 — RevertReflection must undo the confirmations the run made, not only
// delete the blocks it added.
func TestDefect_RevertReflectionDoesNotRevertConfirmations(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	e := newSleepEnv(t, "fact: Gleann uses pure Go for the default build now\n")
	const userText = "Gleann uses pure Go for default build"
	orig, err := e.mgr.Remember(userText, "architecture")
	if err != nil {
		t.Fatal(err)
	}
	priorConfirms := orig.Confirms
	priorUpdatedAt := orig.UpdatedAt
	e.save(t, "conv-conf-0001", time.Minute, userMsg("Gleann uses pure Go for the default build now."))

	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Confirms <= priorConfirms {
		t.Fatalf("reflection did not confirm the near duplicate: Confirms = %d", after.Confirms)
	}

	if _, err := engine.Revert(cs.RunID); err != nil {
		t.Fatal(err)
	}
	reverted, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatalf("revert deleted the user's block: %v", err)
	}
	if reverted.Confirms != priorConfirms {
		t.Errorf("revert did not undo the confirmation: Confirms = %d, want %d", reverted.Confirms, priorConfirms)
	}
	if !reverted.UpdatedAt.Equal(priorUpdatedAt) {
		t.Errorf("revert did not restore UpdatedAt: %v, want %v", reverted.UpdatedAt, priorUpdatedAt)
	}
}

// F6 — a dry run must report the blocks it WOULD confirm, not hide them.
func TestSleepTimeEngine_DryRunReportsConfirmations(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	e := newSleepEnv(t, "fact: Gleann uses pure Go for the default build now\n")
	const userText = "Gleann uses pure Go for default build"
	orig, err := e.mgr.Remember(userText, "architecture")
	if err != nil {
		t.Fatal(err)
	}
	priorConfirms := orig.Confirms
	e.save(t, "conv-dry-0001", time.Minute, userMsg("Gleann uses pure Go for the default build now."))

	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), true) // dry run
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.BlocksConfirmed) != 1 || cs.BlocksConfirmed[0].ID != orig.ID {
		t.Fatalf("dry run did not report the would-confirm: %+v", cs.BlocksConfirmed)
	}
	if cs.BlocksConfirmed[0].PriorConfirms != priorConfirms {
		t.Errorf("recorded PriorConfirms = %d, want %d", cs.BlocksConfirmed[0].PriorConfirms, priorConfirms)
	}
	if len(cs.BlocksAdded) != 0 {
		t.Errorf("dry run reported additions for a near duplicate: %v", cs.BlocksAdded)
	}
	// A dry run must not actually confirm or write anything.
	after, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Confirms != priorConfirms {
		t.Errorf("dry run mutated Confirms: %d, want %d", after.Confirms, priorConfirms)
	}
}
