package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// v3 repair tests (second independent review, memory-fixes-v2.gemini.md).
// BLOCKER: a conversation updated mid-cycle — after its snapshot was
// resolved but before the cursor advanced to cycleMax — must stay eligible
// on the next cycle instead of being stranded behind the cursor forever.

// TestDefect_MidCycleUpdateStrandedBehindCursor is the unit-level repro of
// the review's 10:00 / 10:15 / 10:30 scenario: convA was resolved at 10:00,
// updated on disk to 10:15 while the cycle was still running, and the cursor
// advanced to convB's 10:30. 10:15 is before the cursor, so only the
// "updated since resolved" rule can rescue it.
func TestDefect_MidCycleUpdateStrandedBehindCursor(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tMid := t0.Add(15 * time.Minute)
	t1 := t0.Add(30 * time.Minute)

	conv := &conversations.Conversation{ID: "conv-mid-unit", UpdatedAt: tMid}
	prog := map[string]convProgress{"conv-mid-unit": {ResolvedAt: t0}}

	if !convEligible(conv, prog, t1) {
		t.Fatalf("mid-cycle update stranded: UpdatedAt=%v, ResolvedAt=%v, lastRun=%v; a conversation updated since it was resolved must be eligible even before the cursor",
			tMid, t0, t1)
	}

	// Resolved at exactly this version: still skipped.
	conv.UpdatedAt = t0
	if convEligible(conv, prog, t1) {
		t.Fatal("conversation resolved at exactly its current version must stay skipped")
	}

	// Updated after the cursor: eligible (unchanged cursor path).
	conv.UpdatedAt = t1.Add(time.Minute)
	if !convEligible(conv, prog, t1) {
		t.Fatal("conversation newer than the cursor must be eligible")
	}
}

// TestDefect_MidCycleConversationUpdateReflectedNextCycle drives full Reflect
// cycles. While cycle 1 extracts convA (snapshot 10:00), the fake extractor
// appends a new chat message and re-stamps convA to 10:15 on disk. Cycle 1
// resolves the 10:00 snapshot and advances the cursor to convB's 10:30;
// cycle 2 must still pick convA up and extract the new version.
func TestDefect_MidCycleConversationUpdateReflectedNextCycle(t *testing.T) {
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

	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tMid := t0.Add(15 * time.Minute)
	t1 := t0.Add(30 * time.Minute)

	// Save re-stamps UpdatedAt, so pin each conversation's version by
	// rewriting its JSON file (same technique as the v2 repair tests).
	saveAt := func(id, content string, ts time.Time) {
		c := &conversations.Conversation{ID: id, Title: id, Messages: []conversations.Message{userMsg(content)}}
		if err := convStore.Save(c); err != nil {
			t.Fatal(err)
		}
		c.UpdatedAt = ts
		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(convDir, id+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saveAt("conv-mid-a", "alpha marker: the deploy target is AWS ECS", t0)
	saveAt("conv-mid-b", "beta marker: the deploy target is AWS ECS", t1)

	var aCalls, bCalls int32
	var mutateOnce sync.Once
	sumCfg := summarizerStub(t, "fact: the deploy target is AWS ECS\n", func(prompt string) bool {
		switch {
		case strings.Contains(prompt, "alpha marker"):
			atomic.AddInt32(&aCalls, 1)
			// Mid-cycle chat update: a new message lands while convA's
			// 10:00 snapshot is being extracted.
			mutateOnce.Do(func() {
				c := &conversations.Conversation{ID: "conv-mid-a", Title: "conv-mid-a",
					Messages: []conversations.Message{
						userMsg("alpha marker: the deploy target is AWS ECS"),
						userMsg("alpha marker: correction, the deploy target is GCP"),
					}}
				if err := convStore.Save(c); err != nil {
					t.Error(err)
					return
				}
				c.UpdatedAt = tMid
				data, err := json.MarshalIndent(c, "", "  ")
				if err != nil {
					t.Error(err)
					return
				}
				if err := os.WriteFile(filepath.Join(convDir, "conv-mid-a.json"), data, 0o644); err != nil {
					t.Error(err)
				}
			})
		case strings.Contains(prompt, "beta marker"):
			atomic.AddInt32(&bCalls, 1)
		}
		return false // extraction succeeds
	})
	cfg := SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg}
	engine := NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()

	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if got := engine.LastRun(); !got.Equal(t1) {
		t.Fatalf("cursor after cycle 1 = %v, want cycleMax %v", got, t1)
	}
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	if got := atomic.LoadInt32(&aCalls); got != 2 {
		t.Fatalf("mid-cycle update stranded behind the cursor: convA extracted %d times, want 2 (the 10:15 version must be reflected in cycle 2)", got)
	}
	if got := atomic.LoadInt32(&bCalls); got != 1 {
		t.Fatalf("convB extracted %d times, want 1 (already resolved at its current version)", got)
	}
	// Cycle 3: the 10:15 version is now resolved; nothing re-runs.
	if _, err := engine.Reflect(ctx, false); err != nil {
		t.Fatalf("cycle 3: %v", err)
	}
	if got := atomic.LoadInt32(&aCalls); got != 2 {
		t.Fatalf("convA extracted %d times after cycle 3, want 2 (no endless reprocessing)", got)
	}
}
