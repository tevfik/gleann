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
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
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
	lastRun    time.Time // Track last processed timestamp.
}

// ChangeSet records the diff of a reflection cycle.
type ChangeSet struct {
	RunID             string    `json:"run_id"`
	Timestamp         time.Time `json:"timestamp"`
	BlocksAdded       []string  `json:"blocks_added"`
	BlocksSuperseded  []string  `json:"blocks_superseded"`
	AddedDetails      []Block   `json:"added_details,omitempty"`
	SupersededDetails []Block   `json:"superseded_details,omitempty"`
}

const metaKeySleepTimeLastRun = "sleeptime_last_run"

// NewSleepTimeEngine creates a new sleep-time engine.
func NewSleepTimeEngine(mgr *Manager, cfg SleepTimeConfig) *SleepTimeEngine {
	engine := &SleepTimeEngine{
		manager:    mgr,
		summarizer: NewSummarizer(cfg.SummarizerCfg),
		convStore:  cfg.ConvStore,
		config:     cfg,
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
	return e.lastRun
}

func (e *SleepTimeEngine) updateLastRun(t time.Time) {
	e.lastRun = t
	if e.manager != nil && e.manager.Store() != nil {
		_ = e.manager.Store().SetMeta(metaKeySleepTimeLastRun, t.Format(time.RFC3339Nano))
	}
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
		// Initial delay: wait 2 minutes before first run.
		select {
		case <-stopCh:
			return
		case <-time.After(2 * time.Minute):
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
// If dryRun is true, it identifies blocks to add and supersede without writing to the store.
func (e *SleepTimeEngine) Reflect(ctx context.Context, dryRun bool) (*ChangeSet, error) {
	if e.convStore == nil {
		return nil, fmt.Errorf("no conversation store configured")
	}

	convs, err := e.convStore.List()
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}

	var recent []*conversations.Conversation
	for i := range convs {
		if e.lastRun.IsZero() || convs[i].UpdatedAt.After(e.lastRun) {
			recent = append(recent, &convs[i])
		}
	}

	if len(recent) > e.config.MaxConvs {
		recent = recent[:e.config.MaxConvs]
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
		if !dryRun {
			e.updateLastRun(time.Now())
		}
		return cs, nil
	}

	log.Printf("sleep-time: processing %d conversation(s)", len(recent))

	for _, conv := range recent {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// 1. Generate summary and store it (unless dry-run).
		summary, err := e.summarizer.SummarizeConversation(ctx, conv)
		if err != nil {
			log.Printf("sleep-time: summarize %s: %v", conversations.ShortID(conv.ID), err)
			continue
		}
		if !dryRun && e.manager != nil && e.manager.Store() != nil {
			if err := e.manager.Store().SaveSummary(summary); err != nil {
				log.Printf("sleep-time: save summary %s: %v", conversations.ShortID(conv.ID), err)
			}
		}

		// 2. Extract memories (facts, preferences, decisions).
		blocks, err := e.summarizer.ExtractMemories(ctx, conv)
		if err != nil {
			log.Printf("sleep-time: extract memories %s: %v", conversations.ShortID(conv.ID), err)
			continue
		}

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
				contradictions := e.manager.checkContradictions(blocks[i].Content)
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

			// Check if this is a newly added block or deduplicated on existing.
			var existing *Block
			if e.manager != nil && e.manager.Store() != nil {
				existing, _ = e.manager.Store().Get(blocks[i].ID)
			}
			isNew := (existing == nil)

			if !dryRun && e.manager != nil {
				saved, err := e.manager.RememberBlock(&blocks[i])
				if err != nil {
					log.Printf("sleep-time: remember block: %v", err)
					continue
				}
				if isNew {
					cs.BlocksAdded = append(cs.BlocksAdded, saved.ID)
					cs.AddedDetails = append(cs.AddedDetails, *saved)
				}
			} else if dryRun {
				if isNew {
					cs.BlocksAdded = append(cs.BlocksAdded, blocks[i].ID)
					cs.AddedDetails = append(cs.AddedDetails, blocks[i])
				}
			}
		}

		log.Printf("sleep-time: %s → %d memories extracted", conversations.ShortID(conv.ID), len(blocks))
	}

	if !dryRun {
		if e.manager != nil && e.manager.Store() != nil {
			if err := e.manager.Store().SaveChangeSet(cs); err != nil {
				log.Printf("sleep-time: save change set: %v", err)
			}
		}
		e.updateLastRun(time.Now())
	}

	return cs, nil
}

func (e *SleepTimeEngine) processCycle(ctx context.Context) error {
	_, err := e.Reflect(ctx, false)
	return err
}
