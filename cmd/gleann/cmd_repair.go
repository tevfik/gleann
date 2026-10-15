package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/gleann"
)

func printRepairUsage() {
	fmt.Println(`Usage: gleann index repair <name> [--check] [--force]
       gleann index repair --all [--check] [--force]

Check an index for damage and rebuild its vector index from the passages it
already stores. The source files are not needed.

Older versions edited indexes in place when a file was changed, deleted or
renamed. That left gaps in the passage IDs while vectors are addressed by
position, so vectors could end up attached to the wrong passages and searches
returned the wrong text. 'gleann doctor' reports such indexes; this command
repairs them. It also restores a missing vector file and a stale passage count.

Options:
  --check        Only report: change nothing. Exits with status 1 when an index
                 needs repair.
  --force        Rebuild even if the check finds nothing wrong. The check sees
                 gaps in the passage IDs, a stale count and a missing vector
                 file; a vector file that is out of step while the IDs are
                 contiguous looks healthy and only a rebuild is sure to fix it.
  --all          Check/repair every index instead of one.
  --progress <surface>  Progress while passages are re-embedded: plain (default,
                 a line every couple of seconds on stderr), json, none
  --index-dir <dir>  Index storage directory (default: ~/.gleann/indexes)
  -h, --help     Show this help message`)
}

// cmdRepair implements `gleann index repair <name>|--all [--check]`.
func cmdRepair(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		printRepairUsage()
		return
	}
	all := hasFlag(args, "--all")
	checkOnly := hasFlag(args, "--check")
	force := hasFlag(args, "--force")
	if len(args) == 0 || (!all && strings.HasPrefix(args[0], "-")) {
		fmt.Fprintln(os.Stderr, "usage: gleann index repair <name> [--check] [--force]  |  gleann index repair --all [--check] [--force]")
		exitFunc(1)
		return
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	var names []string
	if all {
		metas, err := gleann.ListIndexes(config.IndexDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot list indexes in %s: %v\n", config.IndexDir, err)
			exitFunc(1)
			return
		}
		for _, m := range metas {
			names = append(names, m.Name)
		}
		if len(names) == 0 {
			fmt.Printf("No indexes found in %s.\n", config.IndexDir)
			return
		}
	} else {
		names = []string{args[0]}
	}

	mode, noPlugins := parseIndexMode(args)
	failed, needRepair, repaired, embedded := 0, 0, 0, 0
	var spent time.Duration
	for _, name := range names {
		res, err := indexing.Repair(context.Background(), indexing.RepairOptions{
			Options:   stdOptions(config, mode, noPlugins, false),
			Name:      name,
			CheckOnly: checkOnly,
			Force:     force,
			Progress:  repairProgress(getFlag(args, "--progress"), name),
		})
		switch {
		case errors.Is(err, indexing.ErrNotRepairable):
			failed++
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n   Rebuild it from its source files: gleann index rebuild %s --docs <dir>\n", name, err, name)
		case err != nil:
			failed++
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", name, err)
		case res.Repaired:
			repaired++
			embedded += res.Stats.Embedded
			spent += res.Elapsed
		case !res.Before.NeedsRepair():
			fmt.Printf("✅ %s: healthy (%d passages)\n", name, res.Before.Passages)
		case checkOnly:
			needRepair++
			fmt.Printf("⚠  %s: needs repair\n", name)
			for _, p := range res.Before.Problems {
				fmt.Printf("     • %s\n", p)
			}
		}
	}
	if repaired > 0 && len(names) > 1 {
		fmt.Printf("\nRepaired %d of %d index(es) in %s, %d text(s) re-embedded.\n",
			repaired, len(names), spent.Round(time.Millisecond), embedded)
	}
	if checkOnly && needRepair > 0 {
		fmt.Printf("\n%d index(es) need repair — run: gleann index repair <name>  (or --all)\n", needRepair)
		exitFunc(1)
		return
	}
	if failed > 0 {
		exitFunc(1)
	}
}

// repairProgress renders re-embedding progress on stderr: at most one update
// every progressInterval, plus the final one. A repair with a warm embedding
// cache finishes before the first update; a cold cache makes it as slow as the
// original embedding pass, and then a silent terminal looks like a hang.
func repairProgress(surface, name string) indexing.ProgressFunc {
	if surface == string(gleann.SurfaceNone) {
		return nil
	}
	const progressInterval = 2 * time.Second
	var (
		mu    sync.Mutex
		start = time.Now()
		last  time.Time
	)
	return func(p indexing.Progress) {
		if p.TotalItems <= 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		final := p.ItemsDone >= p.TotalItems
		if !final && now.Sub(last) < progressInterval {
			return
		}
		last = now
		pct := float64(p.ItemsDone) / float64(p.TotalItems) * 100
		eta := 0.0
		if elapsed := now.Sub(start).Seconds(); p.ItemsDone > 0 && !final && elapsed > 0.5 {
			eta = elapsed / float64(p.ItemsDone) * float64(p.TotalItems-p.ItemsDone)
		}
		if surface == string(gleann.SurfaceJSON) {
			fmt.Fprintf(os.Stderr, `{"index":%q,"phase":%q,"items_done":%d,"total_items":%d,"pct":%.1f,"eta_sec":%.0f}`+"\n",
				name, p.Phase, p.ItemsDone, p.TotalItems, pct, eta)
			return
		}
		line := fmt.Sprintf("   %s: embedding %d/%d texts (%.0f%%)", name, p.ItemsDone, p.TotalItems, pct)
		if eta > 0 {
			line += fmt.Sprintf(", about %s left", (time.Duration(eta) * time.Second).Round(time.Second))
		}
		fmt.Fprintln(os.Stderr, line)
	}
}
