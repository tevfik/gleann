// Package server — background maintenance scheduler.
//
// When the gleann server runs (serve mode), this goroutine wakes up at a
// configurable interval and calls RunMaintenance() on the BBolt memory store.
// That promotes old medium-term blocks to long-term, prunes expired entries,
// and trims stale conversation summaries.
//
// Configuration (env vars):
//
//	GLEANN_MAINTENANCE_INTERVAL_H — how often to run maintenance (default 24h)
//	GLEANN_MAINTENANCE_ENABLED    — "0" or "false" to disable (default enabled)
package server

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/memory"
)

// schedulerConfig is resolved once at startup.
type schedulerConfig struct {
	enabled  bool
	interval time.Duration
}

func newSchedulerConfig() schedulerConfig {
	cfg := schedulerConfig{
		enabled:  true,
		interval: 24 * time.Hour,
	}

	if v := strings.ToLower(os.Getenv("GLEANN_MAINTENANCE_ENABLED")); v == "0" || v == "false" {
		cfg.enabled = false
	}
	if v := os.Getenv("GLEANN_MAINTENANCE_INTERVAL_H"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			cfg.interval = time.Duration(float64(time.Hour) * n)
		}
	}
	return cfg
}

// startMaintenanceScheduler launches a background goroutine that periodically
// calls RunMaintenance on the server's block memory manager.  It stops when
// stopCh is closed.
func startMaintenanceScheduler(mgr *memory.Manager, stopCh <-chan struct{}) {
	cfg := newSchedulerConfig()
	if !cfg.enabled || mgr == nil {
		return
	}

	log.Printf("memory maintenance scheduler started (interval: %v)", cfg.interval)

	go func() {
		// Run once shortly after server starts to catch any missed maintenance.
		initialDelay := 5 * time.Minute
		select {
		case <-stopCh:
			return
		case <-time.After(initialDelay):
		}

		runOnce(mgr)

		ticker := time.NewTicker(cfg.interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				log.Println("memory maintenance scheduler stopped")
				return
			case <-ticker.C:
				runOnce(mgr)
			}
		}
	}()
}

func runOnce(mgr *memory.Manager) {
	log.Println("running memory maintenance (promote medium→long, prune expired)...")
	if err := mgr.RunMaintenance(); err != nil {
		log.Printf("memory maintenance error: %v", err)
		return
	}
	log.Println("memory maintenance completed")
}

// startSleepTimeEngine launches the sleep-time compute engine that
// asynchronously reflects on conversations and updates memory blocks.
// Controlled by GLEANN_SLEEPTIME_ENABLED (default: disabled).
func startSleepTimeEngine(mgr *memory.Manager, stopCh <-chan struct{}) {
	cfg := memory.DefaultSleepTimeConfig()
	if !cfg.Enabled {
		return
	}

	cfg.ConvStore = conversations.DefaultStore()

	engine := memory.NewSleepTimeEngine(mgr, cfg)
	engine.Start(stopCh)
}
