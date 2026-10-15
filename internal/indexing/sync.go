package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ErrNoSourceDir is returned by Sync when no DocsDir was given and the
// index has no recorded source directory.
var ErrNoSourceDir = errors.New("no source directory recorded for index")

// SyncOptions configures Sync.
type SyncOptions struct {
	Options

	// Name of an existing index. Required.
	Name string
	// DocsDir overrides the index's recorded source directory.
	DocsDir string
	// Files limits the sync to these paths (relative to DocsDir or
	// absolute). Missing files are treated as deletions. When empty the
	// whole workspace is diffed against the saved manifest / tracker.
	Files []string
	// Graph forces a graph update; it is implied when a graph already
	// exists for the index.
	Graph bool
	// InstantOnly re-builds only the lexical index (implied for indexes
	// created with InstantOnly).
	InstantOnly bool
}

// SyncResult describes a completed sync.
type SyncResult struct {
	Name    string
	DocsDir string
	// UpToDate is true when no changes were detected.
	UpToDate bool
	Changed  []string
	Deleted  []string
	// FullRebuild is true when the incremental update was not possible
	// and the index was rebuilt from scratch.
	FullRebuild bool
	// Update says what the vector update did and cost (mode, passages
	// added/removed, texts embedded, timings). Nil when no vector update ran:
	// up to date, lexical-only, or a full rebuild.
	Update  *gleann.UpdateStats
	Elapsed time.Duration
}

// Sync incrementally synchronises an existing index with its source
// directory: it detects added/modified/deleted files (Merkle manifest fast
// path, vault tracker fallback), applies the delta to the vector or lexical
// index (falling back to a full rebuild when the backend cannot apply
// deltas), updates the graph and saves the new manifest.
func Sync(ctx context.Context, opts SyncOptions) (*SyncResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Name == "" {
		return nil, errors.New("index name required")
	}
	p := opts.printer()
	cfg := opts.Config
	name := opts.Name
	mode := opts.mode()

	switch mode {
	case ModeCode:
		p.Printf("⚡ Sync mode: CODE (fast code & AST graph, skipping office doc plugins)\n")
	case ModeDocs:
		p.Printf("📄 Sync mode: DOCUMENTS (office docs only, skipping raw code)\n")
	}

	meta, err := gleann.GetIndexMeta(cfg.IndexDir, name)
	if err != nil {
		return nil, fmt.Errorf("index %q not found in %s: %w", name, cfg.IndexDir, err)
	}

	// The vector index must be edited with the backend that wrote it; the
	// flag/default backend may differ (and would misread the file).
	if meta.Backend != "" && meta.Backend != cfg.Backend {
		cfg.Backend = meta.Backend
		opts.Config.Backend = meta.Backend
	}

	docsDir := opts.DocsDir
	if docsDir == "" {
		docsDir = meta.SourceDir
	}
	if docsDir == "" {
		return nil, fmt.Errorf("%w: %q", ErrNoSourceDir, name)
	}
	absDocsDir, err := filepath.Abs(docsDir)
	if err != nil {
		absDocsDir = docsDir
	}

	unlock, err := lockIndex(ctx, cfg.IndexDir, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// Determine if graph indexing is requested or exists.
	buildGraph := opts.Graph
	if !buildGraph {
		// KuzuDB stores the graph as a single file (newer releases) or a
		// directory (older ones); either means a graph exists.
		if _, err := os.Stat(filepath.Join(cfg.IndexDir, name+"_graph")); err == nil {
			buildGraph = true
		}
	}

	res := &SyncResult{Name: name, DocsDir: absDocsDir}
	start := time.Now()
	finish := func() *SyncResult {
		res.Elapsed = time.Since(start)
		return res
	}
	p.Printf("🔄 Synchronizing index %q with %s...\n", name, absDocsDir)

	tracker, release := acquireTracker(&opts.Options, p, "warning: could not initialize vault tracker: %v\n")
	defer release()

	var changedFiles, deletedFiles, eligiblePaths []string
	mPath := manifestPath(cfg.IndexDir, name)
	var newManifest *gleann.Manifest

	if len(opts.Files) > 0 {
		for _, f := range opts.Files {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			absF := f
			if !filepath.IsAbs(absF) {
				absF = filepath.Join(absDocsDir, f)
			}
			if _, err := os.Stat(absF); err != nil {
				deletedFiles = append(deletedFiles, absF)
			} else {
				changedFiles = append(changedFiles, absF)
			}
		}
	} else {
		pluginManager, closePlugins := openPluginManager(opts.NoPlugins, mode)
		mmProcessor := NewMultimodalProcessor(cfg.OllamaHost, cfg.APIKeyForProvider("ollama"), opts.multimodalModel(), p.Printf)
		eligibleEntries, walkErr := CollectEligibleFiles(ctx, absDocsDir, CollectOptions{
			Mode:              mode,
			IncludeSubmodules: opts.IncludeSubmodules,
			PluginManager:     pluginManager,
			NativeExtractor:   gleann.NewNativeExtractor(),
			Multimodal:        mmProcessor,
		})
		closePlugins()
		if walkErr != nil {
			return nil, fmt.Errorf("scanning workspace: %w", walkErr)
		}

		eligiblePaths = make([]string, len(eligibleEntries))
		for i, e := range eligibleEntries {
			eligiblePaths[i] = e.Path
		}

		existingManifest, _ := gleann.LoadManifest(mPath)
		if existingManifest != nil {
			diff, nm, dErr := gleann.QuickDiffDirectory(existingManifest, absDocsDir, eligiblePaths)
			if dErr == nil {
				newManifest = nm
				if diff.IsEmpty() {
					rootShort := existingManifest.RootHash
					if len(rootShort) > 12 {
						rootShort = rootShort[:12]
					}
					p.Printf("⚡ Index %q is already up to date (Merkle root %s verified in %s). No changes detected.\n",
						name, rootShort, time.Since(start).Round(time.Millisecond))
					res.UpToDate = true
					return finish(), nil
				}
				for _, a := range diff.Added {
					changedFiles = append(changedFiles, filepath.Join(absDocsDir, a))
				}
				for _, m := range diff.Modified {
					changedFiles = append(changedFiles, filepath.Join(absDocsDir, m))
				}
				for _, d := range diff.Deleted {
					deletedFiles = append(deletedFiles, filepath.Join(absDocsDir, d))
				}
			}
		}

		if existingManifest == nil || (len(changedFiles) == 0 && len(deletedFiles) == 0) {
			if tracker != nil {
				basePath := filepath.Join(cfg.IndexDir, name, name)
				indexTime := meta.UpdatedAt
				if indexTime.IsZero() {
					indexTime = meta.CreatedAt
				}
				bootstrapTrackerFromIndex(ctx, tracker, absDocsDir, basePath, eligiblePaths, indexTime, p)

				cFiles, dFiles, dErr := tracker.DetectChangedFiles(ctx, absDocsDir, eligiblePaths)
				if dErr != nil {
					p.Warnf("warning: change detection failed (%v), checking all files\n", dErr)
					changedFiles = eligiblePaths
				} else {
					changedFiles = cFiles
					deletedFiles = dFiles
				}
			} else {
				changedFiles = eligiblePaths
			}
		}
	}

	if len(deletedFiles) > 0 && mode != ModeAll {
		var realDeleted []string
		for _, df := range deletedFiles {
			if _, err := os.Stat(df); err == nil {
				// File still exists on disk; check if it was only excluded by the active mode filter.
				ext := strings.ToLower(filepath.Ext(df))
				if mode == ModeDocs && (IsCodeExtension(ext) || !IsOfficeDocExtension(ext)) {
					continue // Preserve code files when syncing docs
				}
				if mode == ModeCode && (IsOfficeDocExtension(ext) || !IsCodeExtension(ext)) {
					continue // Preserve office documents when syncing code
				}
			}
			realDeleted = append(realDeleted, df)
		}
		deletedFiles = realDeleted
	}
	res.Changed, res.Deleted = changedFiles, deletedFiles

	allChanged := make([]string, 0, len(changedFiles)+len(deletedFiles))
	allChanged = append(allChanged, changedFiles...)
	allChanged = append(allChanged, deletedFiles...)
	if len(allChanged) == 0 {
		p.Printf("⚡ Index %q is already up to date. No changes detected in %s.\n", name, time.Since(start).Round(time.Millisecond))
		res.UpToDate = true
		return finish(), nil
	}

	p.Printf("📦 Detected %d changed/new file(s) and %d deleted file(s)\n", len(changedFiles), len(deletedFiles))
	if len(changedFiles) > 0 && len(changedFiles) <= 5 {
		for _, cf := range changedFiles {
			rel, _ := filepath.Rel(absDocsDir, cf)
			p.Printf("  ↳ %s\n", rel)
		}
	}

	graphOpts := GraphOptions{
		Name:              name,
		DocsDir:           absDocsDir,
		IndexDir:          cfg.IndexDir,
		ChangedFiles:      allChanged,
		IncludeSubmodules: opts.IncludeSubmodules,
	}

	if meta.LexicalOnly || opts.InstantOnly {
		builder, err := gleann.NewBuilder(cfg, nil)
		if err != nil {
			return nil, fmt.Errorf("creating builder: %w", err)
		}
		ro := readOptionsFor(cfg, &opts.Options, tracker, nil)
		items, pDocs, err := readDocuments(ctx, absDocsDir, ro, p)
		if err != nil {
			return nil, fmt.Errorf("reading documents: %w", err)
		}
		if err := builder.BuildLexicalOnly(name, items); err != nil {
			return nil, fmt.Errorf("updating lexical index: %w", err)
		}
		p.Printf("⚡ Lexical index updated in %s\n", time.Since(start).Round(time.Millisecond))
		if buildGraph {
			graphOpts.PluginDocs = pDocs
			buildGraphIndex(graphOpts, p)
		}
	} else {
		envOpts := opts.Options
		envOpts.Tracker = tracker
		envOpts.NoTracker = tracker == nil
		env, cleanup, err := newPipelineEnv(ctx, &envOpts, true)
		if err != nil {
			return nil, err
		}
		defer cleanup()

		pDocs, incErr := env.incremental(ctx, name, absDocsDir, allChanged)
		if incErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			p.Warnf("%v, falling back to full rebuild\n", incErr)
			p.Printf("⚠️  Incremental vector update not supported or failed, running full rebuild...\n")
			res.FullRebuild = true
			pDocs, err = env.rebuild(ctx, name, absDocsDir)
			if err != nil {
				return nil, fmt.Errorf("full rebuild: %w", err)
			}
			if buildGraph {
				graphOpts.ChangedFiles = nil
				graphOpts.PluginDocs = pDocs
				buildGraphIndex(graphOpts, p)
			}
		} else {
			up := env.lastUpdate
			res.Update = &up
			p.Printf("⚡ Vector index updated in %s\n", time.Since(start).Round(time.Millisecond))
			if tracker != nil && len(deletedFiles) > 0 {
				for _, f := range deletedFiles {
					_ = tracker.RemovePath(ctx, f)
				}
			}
			if buildGraph {
				graphOpts.PluginDocs = pDocs
				buildGraphIndex(graphOpts, p)
			}
		}
	}

	_ = gleann.UpdateIndexMeta(cfg.IndexDir, name, func(m *gleann.IndexMeta) {
		m.SourceDir = absDocsDir
	})

	if newManifest != nil {
		_ = gleann.SaveManifest(mPath, newManifest)
	} else if len(eligiblePaths) > 0 {
		if mf, mErr := gleann.BuildManifest(name, absDocsDir, eligiblePaths); mErr == nil {
			_ = gleann.SaveManifest(mPath, mf)
		}
	}

	p.Printf("✅ Sync complete for %q in %s (%d files processed)\n", name, time.Since(start).Round(time.Millisecond), len(allChanged))
	return finish(), nil
}

// bootstrapTrackerFromIndex populates the tracker with all files that
// already exist in the index or were present before the index was created.
// This prevents existing large indexes from being treated as completely
// unindexed when tracker state is missing or out of sync.
func bootstrapTrackerFromIndex(ctx context.Context, tracker *vault.Tracker, docsDir, basePath string, eligiblePaths []string, indexTime time.Time, p *printer) {
	sources := make(map[string]bool)

	// Step 1: Collect sources from passages.db if present.
	passagesPath := basePath + ".passages.db"
	if _, err := os.Stat(passagesPath); err == nil {
		pm := gleann.NewReadOnlyPassageManager(basePath)
		if err := pm.Load(); err == nil {
			_ = pm.ForEachPassage(func(p gleann.Passage) error {
				if src, ok := p.Metadata["source"].(string); ok && src != "" {
					sources[src] = true
				}
				return nil
			})
			pm.Close()
		}
	}

	// Step 2: Identify missing files that were already present when the index was built.
	var missingPaths []string
	for src := range sources {
		fullPath := src
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(docsDir, src)
		}
		if rec, _ := tracker.GetRecordByPath(ctx, fullPath); rec == nil {
			missingPaths = append(missingPaths, fullPath)
		}
	}

	if !indexTime.IsZero() {
		for _, path := range eligiblePaths {
			if rec, _ := tracker.GetRecordByPath(ctx, path); rec == nil {
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					if info.ModTime().Before(indexTime) || info.ModTime().Equal(indexTime) {
						missingPaths = append(missingPaths, path)
					}
				}
			}
		}
	}

	if len(missingPaths) == 0 {
		return
	}

	// De-duplicate missingPaths
	pathMap := make(map[string]bool, len(missingPaths))
	var uniquePaths []string
	for _, mp := range missingPaths {
		if !pathMap[mp] {
			pathMap[mp] = true
			uniquePaths = append(uniquePaths, mp)
		}
	}

	p.Printf("🔍 Synchronizing file tracker with %d existing file(s)...\n", len(uniquePaths))
	start := time.Now()

	jobs := make(chan string, len(uniquePaths))
	results := make(chan *vault.FileRecord, len(uniquePaths))

	var wg sync.WaitGroup
	for i := 0; i < workerCount(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				info, err := os.Stat(path)
				if err != nil || info.IsDir() {
					results <- nil
					continue
				}
				results <- makeFileRecord(path, info, nil)
			}
		}()
	}

	for _, up := range uniquePaths {
		jobs <- up
	}
	close(jobs)

	wg.Wait()
	close(results)

	var records []vault.FileRecord
	for rec := range results {
		if rec != nil && rec.Hash != "" {
			records = append(records, *rec)
		}
	}

	if len(records) > 0 {
		_ = tracker.BatchUpsertRecords(ctx, records)
		p.Printf("✅ Tracker synchronized with %d existing file(s) in %s\n", len(records), time.Since(start).Round(time.Millisecond))
	}
}
