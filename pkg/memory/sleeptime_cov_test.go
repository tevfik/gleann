package memory

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// sleepEnv is a store, a manager, a conversation directory and an LLM stub
// for sleep-time tests. reply is what the stubbed model answers to the
// extraction prompt; summaries always get a fixed text.
type sleepEnv struct {
	store *Store
	mgr   *Manager
	convs *conversations.Store
	cfg   SleepTimeConfig
	srv   *httptest.Server
}

func newSleepEnv(t *testing.T, reply string) *sleepEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })

	convDir := filepath.Join(dir, "convs")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv, sumCfg := setupFakeSummarizer(t, reply)
	e := &sleepEnv{store: s, mgr: mgr, convs: conversations.NewStore(convDir), srv: srv}
	e.cfg = SleepTimeConfig{Enabled: true, Interval: 10 * time.Millisecond, MaxConvs: 10, ConvStore: e.convs, SummarizerCfg: sumCfg}
	return e
}

func (e *sleepEnv) save(t *testing.T, id string, age time.Duration, msgs ...conversations.Message) {
	t.Helper()
	c := &conversations.Conversation{ID: id, Title: id, CreatedAt: time.Now().Add(-age - time.Minute), UpdatedAt: time.Now().Add(-age), Messages: msgs}
	if err := e.convs.Save(c); err != nil {
		t.Fatal(err)
	}
	// Save stamps UpdatedAt with time.Now(); on a coarse clock (Windows: up to
	// ~16 ms) the next save could share that timestamp, and the engine rightly
	// keeps tied conversations together at a cut. Wait until the clock has
	// moved on, so conversations saved one after another are strictly ordered.
	for !time.Now().After(c.UpdatedAt) {
		time.Sleep(time.Millisecond)
	}
}

func userMsg(s string) conversations.Message { return conversations.Message{Role: "user", Content: s} }

func TestNewSleepTimeEngine_ResumesCursor(t *testing.T) {
	e := newSleepEnv(t, "")
	want := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := e.store.SetMeta(metaKeySleepTimeLastRun, want.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if got := NewSleepTimeEngine(e.mgr, e.cfg).LastRun(); !got.Equal(want) {
		t.Fatalf("LastRun = %v, want %v", got, want)
	}

	// A cursor that does not parse is ignored: the engine starts from zero
	// rather than failing.
	if err := e.store.SetMeta(metaKeySleepTimeLastRun, "yesterday"); err != nil {
		t.Fatal(err)
	}
	if got := NewSleepTimeEngine(e.mgr, e.cfg).LastRun(); !got.IsZero() {
		t.Fatalf("LastRun with a corrupt cursor = %v, want zero", got)
	}
}

func TestSleepTimeEngine_RevertWithoutManager(t *testing.T) {
	engine := NewSleepTimeEngine(nil, SleepTimeConfig{})
	if _, err := engine.Revert("reflect-1"); err == nil || !strings.Contains(err.Error(), "no manager") {
		t.Fatalf("Revert = %v, want a no-manager error", err)
	}
	if _, err := engine.Reflect(context.Background(), false); err == nil || !strings.Contains(err.Error(), "no conversation store") {
		t.Fatalf("Reflect without a conversation store = %v", err)
	}
}

// Start waits for the initial delay, runs a cycle, repeats on the interval
// and stops when the channel closes.
func TestSleepTimeEngine_StartRunsCycles(t *testing.T) {
	e := newSleepEnv(t, "fact: the build uses Go")
	e.save(t, "conv-start-0001", time.Minute, userMsg("we build with Go"))
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	engine.initialDelay = 5 * time.Millisecond

	stop := make(chan struct{})
	engine.Start(stop)
	defer close(stop)

	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, _ := e.store.Meta(metaKeySleepTimeLastRun)
		blocks, _ := e.store.List(TierLong)
		if raw != "" && len(blocks) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reflection cycle ran: cursor %q, %d blocks", raw, len(blocks))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSleepTimeEngine_StartStopsDuringInitialDelay(t *testing.T) {
	e := newSleepEnv(t, "")
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	engine.initialDelay = time.Hour
	stop := make(chan struct{})
	engine.Start(stop)
	close(stop)
	// Nothing observable runs; the goroutine leaving is checked by the race
	// detector and goleak-free exit of the test binary. The disabled engine
	// must not start at all.
	disabled := NewSleepTimeEngine(e.mgr, SleepTimeConfig{})
	disabled.Start(make(chan struct{}))
}

// runCycle logs and survives both outcomes of a cycle.
func TestSleepTimeEngine_RunCycle(t *testing.T) {
	e := newSleepEnv(t, "fact: cycles are logged")
	e.save(t, "conv-cycle-0001", time.Minute, userMsg("hello"))
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	engine.runCycle()
	if raw, _ := e.store.Meta(metaKeySleepTimeLastRun); raw == "" {
		t.Fatal("successful cycle did not advance the cursor")
	}

	// A conversation store that is a plain file makes the cycle fail; the
	// cursor stays where it was.
	before, _ := e.store.Meta(metaKeySleepTimeLastRun)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := e.cfg
	cfg.ConvStore = conversations.NewStore(file)
	failing := NewSleepTimeEngine(e.mgr, cfg)
	if err := failing.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "list conversations") {
		t.Fatalf("RunOnce = %v, want list conversations error", err)
	}
	failing.runCycle()
	if after, _ := e.store.Meta(metaKeySleepTimeLastRun); after != before {
		t.Fatalf("failed cycle moved the cursor from %q to %q", before, after)
	}
}

func TestReflect_MaxConvsAndCancellation(t *testing.T) {
	e := newSleepEnv(t, "NONE")
	for i := 0; i < 4; i++ {
		e.save(t, fmt.Sprintf("conv-max-%04d", i), time.Duration(i)*time.Minute, userMsg("hi"))
	}
	e.cfg.MaxConvs = 2
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	if _, err := engine.Reflect(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	sums, err := e.store.ListSummaries()
	if err != nil || len(sums) != 2 {
		t.Fatalf("summaries = %d, %v; MaxConvs=2 must cap the cycle at two conversations", len(sums), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fresh := NewSleepTimeEngine(e.mgr, e.cfg)
	fresh.lastRun = time.Time{}
	if _, err := fresh.Reflect(ctx, false); err == nil || ctx.Err() == nil {
		t.Fatalf("Reflect with a cancelled context = %v", err)
	}
}

// A conversation that cannot be summarized is skipped without aborting the
// rest of the cycle.
func TestReflect_SkipsUnsummarizableConversation(t *testing.T) {
	e := newSleepEnv(t, "fact: the other conversation still counts")
	e.save(t, "conv-empty-0001", 2*time.Minute, conversations.Message{Role: "system", Content: "only a system prompt"})
	e.save(t, "conv-real-00001", time.Minute, userMsg("real content"))
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.BlocksAdded) != 1 {
		t.Fatalf("BlocksAdded = %v, want the real conversation's single fact", cs.BlocksAdded)
	}
}

// When the model is unreachable the summary falls back to the first user
// message, but memory extraction fails and the conversation yields no blocks.
func TestReflect_ExtractionFailureKeepsFallbackSummary(t *testing.T) {
	e := newSleepEnv(t, "")
	e.save(t, "conv-down-00001", time.Minute, userMsg("remember the deploy target"))
	e.srv.Close()
	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.BlocksAdded) != 0 {
		t.Fatalf("BlocksAdded = %v with the model down", cs.BlocksAdded)
	}
	sums, _ := e.store.ListSummaries()
	if len(sums) != 1 || sums[0].Content != "remember the deploy target" {
		t.Fatalf("summaries = %+v, want the fallback summary", sums)
	}
}

// Two extracted facts that contradict the same stored block list it once.
func TestReflect_SupersededListedOnce(t *testing.T) {
	e := newSleepEnv(t, "decision: Database standard is not MySQL anymore\ndecision: Database standard is not MySQL for reporting")
	old, err := e.mgr.Remember("Database standard is MySQL", "database")
	if err != nil {
		t.Fatal(err)
	}
	e.save(t, "conv-contra-0001", time.Minute, userMsg("we are leaving MySQL"))
	cs, err := NewSleepTimeEngine(e.mgr, e.cfg).Reflect(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.BlocksSuperseded) != 1 || cs.BlocksSuperseded[0] != old.ID || len(cs.SupersededDetails) != 1 {
		t.Fatalf("superseded = %v (%d details), want %s once", cs.BlocksSuperseded, len(cs.SupersededDetails), old.ID)
	}
	if len(cs.BlocksAdded) != 2 {
		t.Fatalf("BlocksAdded = %v, want both new facts in the dry run", cs.BlocksAdded)
	}
}

// Write failures during a cycle are logged per item; the cycle still
// completes and reports what could be done.
func TestReflect_StoreWriteFailures(t *testing.T) {
	t.Run("summary and block writes fail", func(t *testing.T) {
		e := newSleepEnv(t, "fact: nothing can be stored")
		e.save(t, "conv-wfail-0001", time.Minute, userMsg("anything"))
		abortOn(t, e.store, "INSERT", "summaries")
		abortOn(t, e.store, "INSERT", "blocks")
		cs, err := NewSleepTimeEngine(e.mgr, e.cfg).Reflect(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs.BlocksAdded) != 0 {
			t.Fatalf("BlocksAdded = %v although every block write was aborted", cs.BlocksAdded)
		}
		if blocks, _ := e.store.List(TierLong); len(blocks) != 0 {
			t.Fatalf("%d blocks stored despite the aborted insert", len(blocks))
		}
	})
	t.Run("change set write fails", func(t *testing.T) {
		e := newSleepEnv(t, "fact: the change set cannot be saved")
		e.save(t, "conv-wfail-0002", time.Minute, userMsg("anything"))
		abortOn(t, e.store, "INSERT", "reflection_runs")
		cs, err := NewSleepTimeEngine(e.mgr, e.cfg).Reflect(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs.BlocksAdded) != 1 {
			t.Fatalf("BlocksAdded = %v, want the block despite the lost change set", cs.BlocksAdded)
		}
		if _, err := e.store.GetChangeSet(cs.RunID); err == nil {
			t.Fatal("change set found although its insert was aborted")
		}
	})
}
