// Package memory — Sleep-time compute engine.
//
// Inspired by Letta's sleep-time agents, this subsystem runs a background
// goroutine that periodically reflects on recent conversations and
// autonomously updates memory blocks.  It extracts key facts, prunes
// contradictions, and promotes important information — without blocking the
// interactive flow.
//
// Configuration (env vars):
//
//	GLEANN_SLEEPTIME_ENABLED      — "1" or "true" to enable (default: disabled)
//	GLEANN_SLEEPTIME_INTERVAL     — how often to run, Go duration (default: 30m)
//	GLEANN_SLEEPTIME_MAX_CONVS    — max recent conversations per cycle (default: 5)
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// SleepTimeConfig holds configuration for the sleep-time compute engine.
type SleepTimeConfig struct {
	Enabled       bool
	Interval      time.Duration
	MaxConvs      int // Max recent conversations to process per cycle.
	ConvStore     *conversations.Store
	SummarizerCfg SummarizerConfig
}

// DefaultSleepTimeConfig returns the default configuration, reading env vars.
func DefaultSleepTimeConfig() SleepTimeConfig {
	cfg := SleepTimeConfig{
		Enabled:  false,
		Interval: 30 * time.Minute,
		MaxConvs: 5,
	}

	if v := strings.ToLower(os.Getenv("GLEANN_SLEEPTIME_ENABLED")); v == "1" || v == "true" {
		cfg.Enabled = true
	}
	if v := os.Getenv("GLEANN_SLEEPTIME_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	if v := os.Getenv("GLEANN_SLEEPTIME_MAX_CONVS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxConvs = n
		}
	}
	return cfg
}

// SleepTimeEngine runs background reflection on conversations.
type SleepTimeEngine struct {
	manager    *Manager
	summarizer *Summarizer
	convStore  *conversations.Store
	config     SleepTimeConfig

	// cycleMu serialises reflection cycles: the background goroutine and a
	// concurrent REST/CLI call must not run Reflect at the same time. A
	// second caller gets a clear "already running" answer instead of racing
	// on the engine's shared state.
	cycleMu sync.Mutex

	// mu guards lastRun, which a cycle writes while LastRun may be read
	// concurrently from another goroutine.
	mu      sync.Mutex
	lastRun time.Time // Track last processed timestamp.

	// initialDelay is how long Start waits before the first cycle; tests shorten it.
	initialDelay time.Duration
}

// maxConvRetries bounds how many attempts IN TOTAL a failing conversation is
// retried before it is abandoned. The count is persisted per conversation, so
// it survives engine restarts (`gleann memory reflect` creates a fresh engine
// each run) and a permanently failing conversation cannot block the queue
// forever.
const maxConvRetries = 3

// BlockConfirmation records a confirmation a reflection run made to an
// existing block, so a revert can undo exactly that confirmation. Prior*
// capture the block's state before the run; NewUpdatedAt is the timestamp
// Confirm() stamped (zero in a dry run, which only reports the would-confirm).
type BlockConfirmation struct {
	ID             string    `json:"id"`
	PriorConfirms  int       `json:"prior_confirms"`
	PriorUpdatedAt time.Time `json:"prior_updated_at"`
	NewUpdatedAt   time.Time `json:"new_updated_at,omitempty"`
}

// ChangeSet records the diff of a reflection cycle.
type ChangeSet struct {
	RunID             string              `json:"run_id"`
	Timestamp         time.Time           `json:"timestamp"`
	BlocksAdded       []string            `json:"blocks_added"`
	BlocksSuperseded  []string            `json:"blocks_superseded"`
	BlocksConfirmed   []BlockConfirmation `json:"blocks_confirmed,omitempty"`
	AddedDetails      []Block             `json:"added_details,omitempty"`
	SupersededDetails []Block             `json:"superseded_details,omitempty"`
}

const metaKeySleepTimeLastRun = "sleeptime_last_run"

// metaKeySleepTimeProgress stores per-conversation reflection progress in the
// memory_meta key/value table (no schema migration). It is a JSON map from
// conversation ID to convProgress.
const metaKeySleepTimeProgress = "sleeptime_conv_progress"

// convProgress is the persisted reflection state of one conversation.
// ResolvedAt is the UpdatedAt of the version that was resolved (processed,
// skipped for good, or abandoned); a zero ResolvedAt with Attempts in
// [1, maxConvRetries) means a retry is still pending.
type convProgress struct {
	Attempts   int       `json:"attempts"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

// NewSleepTimeEngine creates a new sleep-time engine.
func NewSleepTimeEngine(mgr *Manager, cfg SleepTimeConfig) *SleepTimeEngine {
	engine := &SleepTimeEngine{
		manager:    mgr,
		summarizer: NewSummarizer(cfg.SummarizerCfg),
		convStore:  cfg.ConvStore,
		config:     cfg,

		initialDelay: 2 * time.Minute,
	}
	if mgr != nil && mgr.Store() != nil {
		if raw, err := mgr.Store().Meta(metaKeySleepTimeLastRun); err == nil && raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				engine.lastRun = t
			} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
				engine.lastRun = t
			}
		}
	}
	return engine
}

// LastRun returns the timestamp of the last processed conversation.
func (e *SleepTimeEngine) LastRun() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastRun
}

func (e *SleepTimeEngine) updateLastRun(t time.Time) {
	e.mu.Lock()
	e.lastRun = t
	e.mu.Unlock()
	if e.manager != nil && e.manager.Store() != nil {
		_ = e.manager.Store().SetMeta(metaKeySleepTimeLastRun, t.Format(time.RFC3339Nano))
	}
}

// loadProgress reads the persisted per-conversation progress. A missing or
// corrupt record (e.g. an old database) yields an empty map, not an error.
func (e *SleepTimeEngine) loadProgress() map[string]convProgress {
	prog := make(map[string]convProgress)
	if e.manager == nil || e.manager.Store() == nil {
		return prog
	}
	raw, err := e.manager.Store().Meta(metaKeySleepTimeProgress)
	if err != nil || raw == "" {
		return prog
	}
	if err := json.Unmarshal([]byte(raw), &prog); err != nil {
		return make(map[string]convProgress)
	}
	return prog
}

func (e *SleepTimeEngine) saveProgress(prog map[string]convProgress) {
	if e.manager == nil || e.manager.Store() == nil {
		return
	}
	data, err := json.Marshal(prog)
	if err != nil {
		return
	}
	_ = e.manager.Store().SetMeta(metaKeySleepTimeProgress, string(data))
}

// convEligible reports whether conv should be processed this cycle.
//   - A conversation already resolved at exactly this version is skipped.
//   - A conversation updated since it was resolved is ALWAYS eligible, even
//     when its new timestamp lands behind the cursor: a chat message can
//     arrive mid-cycle, after the snapshot was resolved but before the
//     cursor advanced to the cycle's newest conversation.
//   - A pending retry (failed, not yet abandoned) is ALWAYS eligible, so it
//     survives the cursor moving past it and the clock going backwards.
//   - Otherwise (never seen) it is eligible when it is at or after the
//     cursor. "At" rescues a conversation tied with the cursor timestamp;
//     "after" respects an old cursor from a prior version.
func convEligible(conv *conversations.Conversation, prog map[string]convProgress, lastRun time.Time) bool {
	p, seen := prog[conv.ID]
	if seen && !p.ResolvedAt.IsZero() && p.ResolvedAt.Equal(conv.UpdatedAt) {
		return false
	}
	if seen && !p.ResolvedAt.IsZero() && !p.ResolvedAt.Equal(conv.UpdatedAt) {
		return true
	}
	if seen && p.Attempts > 0 && p.Attempts < maxConvRetries && p.ResolvedAt.IsZero() {
		return true
	}
	return lastRun.IsZero() || !conv.UpdatedAt.Before(lastRun)
}

// recordFailure persists a failed processing attempt of conv and decides
// whether it is abandoned. It never stops the cycle: other conversations are
// still processed, because retry eligibility is per conversation, not gated on
// the cursor. After maxConvRetries attempts IN TOTAL the conversation is
// abandoned (marked resolved at this version) so it cannot block the queue.
func (e *SleepTimeEngine) recordFailure(prog map[string]convProgress, conv *conversations.Conversation, stage string, err error) {
	log.Printf("sleep-time: %s %s: %v", conversations.ShortID(conv.ID), stage, err)
	p := prog[conv.ID]
	// A conversation updated since it was last resolved earns fresh retries.
	if !p.ResolvedAt.IsZero() && !p.ResolvedAt.Equal(conv.UpdatedAt) {
		p.Attempts = 0
	}
	p.Attempts++
	if p.Attempts >= maxConvRetries {
		p.ResolvedAt = conv.UpdatedAt
		log.Printf("sleep-time: giving up on %s after %d attempts", conversations.ShortID(conv.ID), p.Attempts)
	} else {
		p.ResolvedAt = time.Time{}
		log.Printf("sleep-time: will retry %s next cycle (attempt %d/%d)", conversations.ShortID(conv.ID), p.Attempts, maxConvRetries)
	}
	prog[conv.ID] = p
}

// markResolved records that conv was resolved at its current version.
func markResolved(prog map[string]convProgress, conv *conversations.Conversation) {
	p := prog[conv.ID]
	p.ResolvedAt = conv.UpdatedAt
	prog[conv.ID] = p
}

// Revert reverts a reflection run by ID, deleting the blocks it added.
func (e *SleepTimeEngine) Revert(runID string) (*ChangeSet, error) {
	if e.manager == nil {
		return nil, fmt.Errorf("no manager configured")
	}
	return e.manager.RevertReflection(runID)
}

// Start launches the background goroutine. It stops when stopCh is closed.
func (e *SleepTimeEngine) Start(stopCh <-chan struct{}) {
	if !e.config.Enabled || e.convStore == nil {
		return
	}

	log.Printf("sleep-time engine started (interval: %v, max_convs: %d)", e.config.Interval, e.config.MaxConvs)

	go func() {
		// Initial delay: wait before the first run (2 minutes by default).
		select {
		case <-stopCh:
			return
		case <-time.After(e.initialDelay):
		}

		e.runCycle()

		ticker := time.NewTicker(e.config.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				log.Println("sleep-time engine stopped")
				return
			case <-ticker.C:
				e.runCycle()
			}
		}
	}()
}

// RunOnce executes a single sleep-time cycle synchronously. Useful for testing.
func (e *SleepTimeEngine) RunOnce(ctx context.Context) error {
	return e.processCycle(ctx)
}

func (e *SleepTimeEngine) runCycle() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Println("sleep-time: starting reflection cycle...")
	if err := e.processCycle(ctx); err != nil {
		log.Printf("sleep-time: cycle error: %v", err)
		return
	}
	log.Println("sleep-time: reflection cycle complete")
}

// Reflect executes a reflection cycle, returning the change set.
// If dryRun is true, it identifies blocks to add, supersede and confirm
// without writing to the store. Only one cycle runs at a time: a concurrent
// call returns an "already running" error rather than racing on shared state.
func (e *SleepTimeEngine) Reflect(ctx context.Context, dryRun bool) (*ChangeSet, error) {
	if e.convStore == nil {
		return nil, fmt.Errorf("no conversation store configured")
	}
	if !e.cycleMu.TryLock() {
		return nil, fmt.Errorf("sleep-time: reflection already running")
	}
	defer e.cycleMu.Unlock()

	convs, err := e.convStore.List()
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}

	lastRun := e.LastRun()
	prog := e.loadProgress()

	var recent []*conversations.Conversation
	for i := range convs {
		if convEligible(&convs[i], prog, lastRun) {
			recent = append(recent, &convs[i])
		}
	}

	// List returns newest first; reflect oldest first so a backlog larger
	// than MaxConvs is worked through in order over several cycles instead
	// of stranding its older half behind the cursor.
	sort.SliceStable(recent, func(i, j int) bool {
		return recent[i].UpdatedAt.Before(recent[j].UpdatedAt)
	})
	if len(recent) > e.config.MaxConvs {
		n := e.config.MaxConvs
		// Keep conversations tied at the cut timestamp together: the
		// cursor moves to that timestamp, so a tie left behind would
		// never be selected again.
		for n >= 1 && n < len(recent) && recent[n].UpdatedAt.Equal(recent[n-1].UpdatedAt) {
			n++
		}
		recent = recent[:n]
	}

	runID := fmt.Sprintf("reflect-%d", time.Now().UnixNano())
	cs := &ChangeSet{
		RunID:            runID,
		Timestamp:        time.Now(),
		BlocksAdded:      make([]string, 0),
		BlocksSuperseded: make([]string, 0),
	}

	if len(recent) == 0 {
		log.Println("sleep-time: no new conversations to process")
		// Nothing was processed: the cursor stays where it is.
		return cs, nil
	}

	log.Printf("sleep-time: processing %d conversation(s)", len(recent))

	// cycleMax is the newest UpdatedAt among the conversations resolved this
	// cycle (processed, skipped for good, or abandoned). The cursor advances
	// there — never to time.Now(), and never backwards.
	var cycleMax time.Time
	processedAny := false

	for _, conv := range recent {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// A conversation with only system messages can never be
		// summarized; skip it for good instead of retrying it forever.
		if !hasTranscript(conv) {
			log.Printf("sleep-time: %s has no messages to summarize, skipping", conversations.ShortID(conv.ID))
			if !dryRun {
				markResolved(prog, conv)
			}
			if conv.UpdatedAt.After(cycleMax) {
				cycleMax = conv.UpdatedAt
			}
			processedAny = true
			continue
		}

		failed := false
		var blocks []Block

		// 1. Generate summary and store it (unless dry-run).
		summary, err := e.summarizer.SummarizeConversation(ctx, conv)
		if err != nil {
			if !dryRun {
				e.recordFailure(prog, conv, "summarize", err)
			} else {
				log.Printf("sleep-time: %s summarize: %v", conversations.ShortID(conv.ID), err)
			}
			failed = true
		}
		if !failed {
			if !dryRun && e.manager != nil && e.manager.Store() != nil {
				if err := e.manager.Store().SaveSummary(summary); err != nil {
					log.Printf("sleep-time: save summary %s: %v", conversations.ShortID(conv.ID), err)
				}
			}

			// 2. Extract memories (facts, preferences, decisions).
			blocks, err := e.summarizer.ExtractMemories(ctx, conv)
			if err != nil {
				if !dryRun {
					e.recordFailure(prog, conv, "extract memories", err)
				} else {
					log.Printf("sleep-time: %s extract memories: %v", conversations.ShortID(conv.ID), err)
				}
				failed = true
			}
			if !failed {
				e.reflectBlocks(conv, blocks, runID, dryRun, cs)
			}
		}

		if !failed {
			if !dryRun {
				markResolved(prog, conv)
			}
			if conv.UpdatedAt.After(cycleMax) {
				cycleMax = conv.UpdatedAt
			}
			processedAny = true
			log.Printf("sleep-time: %s → %d memories extracted", conversations.ShortID(conv.ID), len(blocks))
		}
	}

	if !dryRun {
		// Drop progress for conversations that no longer exist so the
		// record does not grow without bound.
		present := make(map[string]bool, len(convs))
		for i := range convs {
			present[convs[i].ID] = true
		}
		for id := range prog {
			if !present[id] {
				delete(prog, id)
			}
		}
		e.saveProgress(prog)
		if e.manager != nil && e.manager.Store() != nil {
			if err := e.manager.Store().SaveChangeSet(cs); err != nil {
				log.Printf("sleep-time: save change set: %v", err)
			}
		}
		if processedAny && cycleMax.After(lastRun) {
			e.updateLastRun(cycleMax)
		}
	}

	return cs, nil
}

// reflectBlocks stores (or, in a dry run, previews) the memories extracted
// from one conversation, recording additions, supersessions and confirmations
// in the change set. Per-item write failures are logged and do not abort the
// conversation or the cycle.
func (e *SleepTimeEngine) reflectBlocks(conv *conversations.Conversation, blocks []Block, runID string, dryRun bool, cs *ChangeSet) {
	for i := range blocks {
		blocks[i].Source = "sleep_time"
		blocks[i].Tags = append(blocks[i].Tags, "sleep_time", "conversation:"+conversations.ShortID(conv.ID))
		if blocks[i].Metadata == nil {
			blocks[i].Metadata = make(map[string]string)
		}
		if conv.ID != "" {
			blocks[i].Metadata["conversation_id"] = conv.ID
		}
		blocks[i].Metadata["reflection_run_id"] = runID
		if blocks[i].Tier == "" {
			blocks[i].Tier = TierLong
		}
		if blocks[i].ID == "" {
			blocks[i].ID = generateBlockID(&blocks[i])
		}

		// Check contradictions against existing knowledge to determine superseded blocks.
		if e.manager != nil {
			contradictions := e.manager.checkContradictions(&blocks[i])
			for _, c := range contradictions {
				found := false
				for _, sid := range cs.BlocksSuperseded {
					if sid == c.ID {
						found = true
						break
					}
				}
				if !found {
					cs.BlocksSuperseded = append(cs.BlocksSuperseded, c.ID)
					cs.SupersededDetails = append(cs.SupersededDetails, *c)
				}
			}
		}

		// A block is new only if the store holds nothing it would merge
		// into: neither the same ID nor an exact or near duplicate (Add
		// folds those into the existing block). Recording a merged block
		// as added would let RevertReflection delete the user's own block.
		origID := blocks[i].ID
		isNew := true
		var existing *Block
		if e.manager != nil && e.manager.Store() != nil {
			dup, err := e.manager.Store().FindDuplicate(&blocks[i])
			if err != nil || dup != nil {
				isNew = false
			}
			existing = dup
		}

		if existing != nil {
			// Reflection never replaces the text of a block it did not
			// create: a duplicate only confirms the stored block, so there
			// is no overwritten wording a revert would have to restore. The
			// confirmation is recorded so a revert can undo it (and a dry
			// run can report it).
			conf := BlockConfirmation{ID: existing.ID, PriorConfirms: existing.Confirms, PriorUpdatedAt: existing.UpdatedAt}
			if !dryRun && e.manager != nil {
				existing.Confirm()
				conf.NewUpdatedAt = existing.UpdatedAt
				if err := e.manager.Store().Update(existing); err != nil {
					log.Printf("sleep-time: confirm block %s: %v", conversations.ShortID(existing.ID), err)
					continue
				}
			}
			cs.BlocksConfirmed = append(cs.BlocksConfirmed, conf)
		} else if isNew {
			if !dryRun && e.manager != nil {
				saved, err := e.manager.RememberBlock(&blocks[i])
				if err != nil {
					log.Printf("sleep-time: remember block: %v", err)
					continue
				}
				// Add rewrites the block to the one it merged into, so a
				// merge shows up as a different ID than the one computed above.
				if saved.ID == origID {
					cs.BlocksAdded = append(cs.BlocksAdded, saved.ID)
					cs.AddedDetails = append(cs.AddedDetails, *saved)
				}
			} else if dryRun {
				cs.BlocksAdded = append(cs.BlocksAdded, blocks[i].ID)
				cs.AddedDetails = append(cs.AddedDetails, blocks[i])
			}
		}
	}
}

// hasTranscript reports whether conv has at least one non-system message,
// the only input SummarizeConversation can work with.
func hasTranscript(conv *conversations.Conversation) bool {
	for _, m := range conv.Messages {
		if m.Role != "system" {
			return true
		}
	}
	return false
}

func (e *SleepTimeEngine) processCycle(ctx context.Context) error {
	_, err := e.Reflect(ctx, false)
	return err
}
