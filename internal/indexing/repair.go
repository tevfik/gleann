package indexing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ErrNotRepairable is returned when an index's passage store is missing or
// unreadable: the vector index can only be rebuilt from passages, so such an
// index has to be rebuilt from its source files (gleann index rebuild).
var ErrNotRepairable = errors.New("index cannot be repaired from its passage store")

// RepairOptions configures Repair.
type RepairOptions struct {
	Options
	Name string
	// CheckOnly reports the index's health without changing anything.
	CheckOnly bool
	// Progress, if set, receives updates while the passages are re-embedded
	// (phase "vector_index"); a cold embedding cache makes that the long part.
	Progress ProgressFunc
	// Force rebuilds the vector index even when the check finds nothing wrong.
	// The check sees gaps in the passage IDs, a stale count and a missing
	// vector file; a vector file that is out of step while the IDs are
	// contiguous looks healthy, and only a rebuild is sure to fix it.
	Force bool
}

// RepairResult describes what Repair found and did.
type RepairResult struct {
	Name string
	// Before is the health of the index when Repair started.
	Before *gleann.IndexHealth
	// Repaired reports whether the index was rebuilt (never true for CheckOnly).
	Repaired bool
	// Stats is what the rebuild cost: passages, texts re-embedded, timings.
	// Zero when nothing was rebuilt.
	Stats gleann.UpdateStats
	// CacheHits and CacheLookups are the embedding cache's counts for this
	// repair; with a warm cache a repair costs almost nothing, with a cold one
	// every passage is embedded again.
	CacheHits, CacheLookups int
	Elapsed                 time.Duration
}

// Repair checks an index and, unless CheckOnly is set, repairs it: the vector
// index is rebuilt from the passage store, which renumbers the passages
// densely. Passages are kept as they are, so the source files are not needed.
// Embeddings come from the embedding cache where the texts are unchanged.
//
// It repairs indexes that older versions left with gaps in their passage IDs
// (vectors attached to the wrong passages) or without a vector file.
func Repair(ctx context.Context, opts RepairOptions) (*RepairResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Name == "" {
		return nil, errors.New("index name required")
	}
	cfg := opts.Config
	name := opts.Name

	unlock, err := lockIndex(ctx, cfg.IndexDir, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	start := time.Now()
	meta, err := gleann.GetIndexMeta(cfg.IndexDir, name)
	if err != nil {
		return nil, fmt.Errorf("index %q not found in %s: %w", name, cfg.IndexDir, err)
	}
	health, err := gleann.CheckIndexHealth(cfg.IndexDir, name)
	if err != nil {
		return nil, err
	}
	res := &RepairResult{Name: name, Before: health, Elapsed: time.Since(start)}
	// Nothing to rebuild from in an index without passages, and a lexical-only
	// index has no vectors: --force has no work there.
	forced := opts.Force && health.Repairable && health.Passages > 0 && meta.VectorReady
	if (!health.NeedsRepair() && !forced) || opts.CheckOnly {
		return res, nil
	}
	if !health.Repairable {
		return res, fmt.Errorf("%w: %s", ErrNotRepairable, health.Problems[0])
	}

	// A lexical-only index has no vectors to rebuild; only its recorded count
	// can be stale.
	if !meta.VectorReady {
		if err := gleann.UpdateIndexMeta(cfg.IndexDir, name, func(m *gleann.IndexMeta) {
			m.NumPassages = health.Passages
			m.UpdatedAt = time.Now()
		}); err != nil {
			return res, fmt.Errorf("update metadata: %w", err)
		}
		res.Repaired, res.Elapsed = true, time.Since(start)
		return res, nil
	}

	// The vector file must be rebuilt with the backend the index was built with.
	o := opts.Options
	if meta.Backend != "" {
		o.Config.Backend = meta.Backend
	}
	env, cleanup, err := newPipelineEnv(ctx, &o, true)
	if err != nil {
		return res, err
	}
	defer cleanup()
	builder, err := gleann.NewBuilder(env.cfg, env.embedder)
	if err != nil {
		return res, fmt.Errorf("creating builder: %w", err)
	}
	prog := newProgressEmitter(opts.Progress)
	builder.SetProgressCallback(func(phase string, done, total int) {
		prog.emit(phase, done, total, 0, 0, done == total)
	})
	why := "forced rebuild"
	if health.NeedsRepair() {
		why = health.Problems[0]
	}
	env.p.Printf("🔧 Repairing %q (%d passages): %s\n", name, health.Passages, why)
	hits0, total0, _ := cacheCounters(env.embedder)
	stats, err := builder.Compact(ctx, name)
	res.Stats = stats
	if hits, total, ok := cacheCounters(env.embedder); ok {
		res.CacheHits, res.CacheLookups = hits-hits0, total-total0
	}
	if err != nil {
		return res, fmt.Errorf("rebuilding vector index: %w", err)
	}
	if after, err := gleann.CheckIndexHealth(cfg.IndexDir, name); err != nil {
		return res, err
	} else if after.NeedsRepair() {
		return res, fmt.Errorf("index %q is still unhealthy after repair: %s", name, after.Problems[0])
	}
	res.Repaired, res.Elapsed = true, time.Since(start)
	env.p.Printf("✅ Repaired %q in %s: %d passages, %d text(s) embedded in %s\n",
		name, fmtDur(res.Elapsed), stats.Passages, stats.Embedded, fmtDur(stats.EmbedTime))
	if res.CacheLookups > 0 {
		env.p.Printf("💾 Embedding cache: %d/%d hits (%.0f%%)\n", res.CacheHits, res.CacheLookups, float64(res.CacheHits)/float64(res.CacheLookups)*100)
	}
	return res, nil
}
