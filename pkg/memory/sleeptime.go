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

// NewSleepTimeEngine creates a new sleep-time engine.
func NewSleepTimeEngine(mgr *Manager, cfg SleepTimeConfig) *SleepTimeEngine {
	return &SleepTimeEngine{
		manager:    mgr,
		summarizer: NewSummarizer(cfg.SummarizerCfg),
		convStore:  cfg.ConvStore,
		config:     cfg,
	}
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

func (e *SleepTimeEngine) processCycle(ctx context.Context) error {
	if e.convStore == nil {
		return fmt.Errorf("no conversation store configured")
	}

	// Get recent conversations.
	convs, err := e.convStore.List()
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}

	// Filter to conversations updated since last run.
	var recent []*conversations.Conversation
	for i := range convs {
		if e.lastRun.IsZero() || convs[i].UpdatedAt.After(e.lastRun) {
			recent = append(recent, &convs[i])
		}
	}

	// Limit to MaxConvs most recent.
	if len(recent) > e.config.MaxConvs {
		recent = recent[:e.config.MaxConvs]
	}

	if len(recent) == 0 {
		log.Println("sleep-time: no new conversations to process")
		e.lastRun = time.Now()
		return nil
	}

	log.Printf("sleep-time: processing %d conversation(s)", len(recent))

	for _, conv := range recent {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 1. Generate summary and store it.
		summary, err := e.summarizer.SummarizeConversation(ctx, conv)
		if err != nil {
			log.Printf("sleep-time: summarize %s: %v", conversations.ShortID(conv.ID), err)
			continue
		}
		if err := e.manager.Store().SaveSummary(summary); err != nil {
			log.Printf("sleep-time: save summary %s: %v", conversations.ShortID(conv.ID), err)
		}

		// 2. Extract memories (facts, preferences, decisions).
		blocks, err := e.summarizer.ExtractMemories(ctx, conv)
		if err != nil {
			log.Printf("sleep-time: extract memories %s: %v", conversations.ShortID(conv.ID), err)
			continue
		}

		for i := range blocks {
			// Tag with conversation and sleep-time source.
			blocks[i].Source = "sleep_time"
			blocks[i].Tags = append(blocks[i].Tags, "sleep_time", "conversation:"+conversations.ShortID(conv.ID))
			if conv.ID != "" {
				if blocks[i].Metadata == nil {
					blocks[i].Metadata = make(map[string]string)
				}
				blocks[i].Metadata["conversation_id"] = conv.ID
			}

			if err := e.manager.Store().Add(&blocks[i]); err != nil {
				log.Printf("sleep-time: add block: %v", err)
			}
		}

		log.Printf("sleep-time: %s → %d memories extracted", conversations.ShortID(conv.ID), len(blocks))
	}

	e.lastRun = time.Now()
	return nil
}
