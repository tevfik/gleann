package indexing

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/fsretry"
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
// index, updates the graph and saves the new manifest.
//
// It falls back to a full rebuild only when the delta cannot be applied at
// all: the passage store is missing, unreadable or damaged, or the index is in
// a state the update cannot edit (gleann.ErrRebuildRequired). A transient
// failure — embedder unreachable, plugin extraction, source read error — is
// returned with the index untouched, because a rebuild would repeat it (and
// re-embed the whole corpus); the next sync retries the delta.
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

	// An interrupted full rebuild leaves the index in <dir>.sync-bak and <dir>
	// partial or missing: settle that before reading the metadata.
	if what, err := RecoverSyncBackup(ctx, cfg.IndexDir, name); err != nil {
		return nil, fmt.Errorf("recovering index %q after an interrupted sync: %w", name, err)
	} else if what == "restored" {
		p.Printf("ℹ️  A previous sync of %q was interrupted; its index was restored from the rollback copy\n", name)
	}

	meta, err := gleann.GetIndexMeta(cfg.IndexDir, name)
	if err != nil {
		return nil, fmt.Errorf("index %q not found in %s: %w", name, cfg.IndexDir, err)
	}
	if err := gleann.CheckStoreFormat(cfg.IndexDir, name); err != nil {
		return nil, err
	}

	// The vector index must be edited with the backend that wrote it; the
	// flag/default backend may differ (and would misread the file).
	if meta.Backend != "" && meta.Backend != cfg.Backend {
		cfg.Backend = meta.Backend
		opts.Config.Backend = meta.Backend
	}

	if err := reconcileModel(meta, &opts.Options, p); err != nil {
		return nil, err
	}
	cfg.EmbeddingModel = opts.Config.EmbeddingModel

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

	// A format-2 index whose <name>.db is gone has lost its passages and its
	// code graph: both live in that database. HasCodeGraph no longer recreates
	// an empty store, so remember the loss here. The incremental update then
	// fails with gleann.ErrRebuildRequired, and the full-rebuild fallback
	// restores the store and, when meta.json recorded a graph, the graph.
	basePath := filepath.Join(cfg.IndexDir, name, name)
	_, dbStatErr := os.Stat(basePath + ".db")
	storeMissing := gleann.StoreFormatOf(basePath) == 2 && errors.Is(dbStatErr, fs.ErrNotExist)

	// An index built before meta.json recorded has_graph keeps that fact only
	// in the database. Backfill it here: without it, a later store loss is
	// indistinguishable from an index that never had a graph, and the graph
	// would be silently dropped instead of rebuilt. Every sync (even an
	// up-to-date no-op) upgrades a legacy index this way.
	hasGraph := gleann.HasCodeGraph(cfg.IndexDir, name)
	if !storeMissing && hasGraph && !meta.HasGraph {
		if mErr := gleann.UpdateIndexMeta(cfg.IndexDir, name, func(m *gleann.IndexMeta) {
			m.HasGraph = true
		}); mErr != nil {
			p.Warnf("warning: could not record the code graph in the index metadata: %v\n", mErr)
		} else {
			meta.HasGraph = true
		}
	}

	// Determine if graph indexing is requested or exists.
	buildGraph := opts.Graph || hasGraph
	if storeMissing && !buildGraph {
		// meta.json is the only record left of a graph whose database was
		// lost; rebuild it with the store.
		buildGraph = meta.HasGraph
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
				if !errors.Is(err, fs.ErrNotExist) {
					// Only a vanished file is a deletion. A stat error (an
					// unreadable parent directory, EACCES) must fail the sync:
					// purging the passages would silently unindex a file that
					// still exists.
					return nil, fmt.Errorf("checking %s: %w", absF, err)
				}
				deletedFiles = append(deletedFiles, absF)
			} else {
				changedFiles = append(changedFiles, absF)
			}
		}
		// A directory that was renamed or removed vanishes as one path, but
		// every file indexed under it is gone too.
		deletedFiles = expandVanishedDirs(cfg.IndexDir, name, absDocsDir, deletedFiles)
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

		existingManifest, _ := gleann.LoadIndexManifest(cfg.IndexDir, name)
		if existingManifest != nil {
			diff, _, dErr := gleann.QuickDiffDirectory(existingManifest, absDocsDir, eligiblePaths)
			if dErr == nil {
				if diff.IsEmpty() && !storeMissing {
					if buildGraph {
						// A format-2 graph can hold dirty files even when no
						// passage changed; converge them before "up to date".
						gOpts := GraphOptions{Name: name, DocsDir: absDocsDir, IndexDir: cfg.IndexDir, IncludeSubmodules: opts.IncludeSubmodules}
						if gErr := convergeDirtyFormat2Graph(cfg.IndexDir, name, absDocsDir, gOpts, p); gErr != nil {
							return finish(), fmt.Errorf("%w: %v", ErrGraphUpdate, gErr)
						}
					}
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
	if len(allChanged) == 0 && !storeMissing {
		if buildGraph {
			// A format-2 graph can hold dirty files even when no passage
			// changed (a failed graph write); converge them before
			// reporting the index up to date (RFC 003 section 6.5).
			gOpts := GraphOptions{Name: name, DocsDir: absDocsDir, IndexDir: cfg.IndexDir, IncludeSubmodules: opts.IncludeSubmodules}
			if gErr := convergeDirtyFormat2Graph(cfg.IndexDir, name, absDocsDir, gOpts, p); gErr != nil {
				return finish(), fmt.Errorf("%w: %v", ErrGraphUpdate, gErr)
			}
		}
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

	var graphErr error // set when the vector index was updated but the graph was not
	if meta.LexicalOnly || opts.InstantOnly {
		builder, err := gleann.NewBuilder(cfg, nil)
		if err != nil {
			return nil, fmt.Errorf("creating builder: %w", err)
		}
		builder.SetSourceDir(absDocsDir)
		ro := readOptionsFor(cfg, &opts.Options, tracker, nil)
		items, pDocs, err := readDocuments(ctx, absDocsDir, ro, p)
		if err != nil {
			return nil, fmt.Errorf("reading documents: %w", err)
		}
		if err := builder.BuildLexicalOnly(name, items); err != nil {
			return nil, fmt.Errorf("updating lexical index: %w", err)
		}
		p.Printf("⚡ Lexical index updated in %s\n", time.Since(start).Round(time.Millisecond))
		if builder.DatabaseRecreated() || storeMissing {
			// The database was recreated (damaged, incompatible, or gone); if
			// it held a graph, that graph is rebuilt in full from the sources.
			buildGraph = builder.DatabaseRecreated() || buildGraph || meta.HasGraph
			if buildGraph {
				p.Printf("⚠️  The index database was recreated, so the code graph is rebuilt from the sources.\n")
				graphOpts.ChangedFiles = nil
			}
		}
		if buildGraph {
			graphOpts.PluginDocs = pDocs
			graphErr = buildGraphIndex(graphOpts, p)
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

		var pDocs []*PluginDoc
		var incErr error
		if storeMissing {
			// The store and its manifest are gone: there is nothing to update
			// in place, so go straight to the (rollback-protected) rebuild.
			incErr = fmt.Errorf("the record store of index %q is missing: %w", name, gleann.ErrRebuildRequired)
		} else {
			pDocs, incErr = env.incremental(ctx, name, absDocsDir, allChanged)
		}
		if incErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(incErr, gleann.ErrDimensionMismatch) {
				return nil, incErr // a full rebuild would silently switch models
			}
			if !needsFullRebuild(incErr) {
				// Transient (embedder unreachable, plugin or source read
				// failure) or a backend error: a full rebuild would fail the
				// same way or re-embed the whole corpus. The incremental
				// update touched nothing, so the index stays as it is and the
				// next sync retries.
				return nil, incErr
			}
			p.Warnf("%v, falling back to full rebuild\n", incErr)
			p.Printf("⚠️  Incremental vector update not supported or failed, running full rebuild...\n")
			res.FullRebuild = true
			// A failed rebuild (e.g. the embedder is still down) must not
			// leave a degraded index behind: roll back to the previous one.
			err = withIndexRollbackMeta(cfg.IndexDir, name, p, func(prev *gleann.IndexMeta) error {
				env.prevMeta = prev
				var rerr error
				// requireItems: a corpus that became empty (the reason the
				// empty update ran) must fail here, or the rollback copy is
				// deleted and no index is left behind.
				pDocs, rerr = env.rebuildWith(ctx, name, absDocsDir, storeMissing, true)
				return rerr
			})
			if err != nil {
				return nil, fmt.Errorf("full rebuild (previous index restored): %w", err)
			}
			graphRebuilt := buildGraph || env.dbRecreated
			if (env.dbRecreated || storeMissing) && !opts.Graph {
				if graphRebuilt {
					p.Printf("⚠️  The index database was recreated, so the code graph is rebuilt from the sources.\n")
				} else {
					p.Printf("⚠️  The index database was recreated from the sources.\n")
				}
			}
			if graphRebuilt {
				graphOpts.ChangedFiles = nil
				graphOpts.PluginDocs = pDocs
				graphErr = buildGraphIndex(graphOpts, p)
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
				graphErr = buildGraphIndex(graphOpts, p)
			}
		}
	}

	updateMetaLogged(cfg.IndexDir, name, func(m *gleann.IndexMeta) {
		m.SourceDir = absDocsDir
		m.GleannVersion = gleann.GetGleannVersion()
		m.PluginVersions = gleann.GetInstalledPluginVersions()
	})

	// Without a saved manifest the next sync sees these files as changed again
	// and retries, instead of calling the index up to date over a stale graph.
	if graphErr != nil {
		return finish(), fmt.Errorf("%w: %v", ErrGraphUpdate, graphErr)
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

	// Step 1: Collect sources from the passage store (bbolt for format 1, the
	// SQLite record store for format 2).
	if ps, err := gleann.OpenPassageStore(basePath, true); err == nil {
		if err := ps.Load(); err == nil {
			_ = ps.ForEachPassage(func(p gleann.Passage) error {
				if src, ok := p.Metadata["source"].(string); ok && src != "" {
					sources[src] = true
				}
				return nil
			})
		}
		ps.Close()
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

// withIndexRollback runs fn, which rewrites the index <indexDir>/<name>. The
// existing index directory is moved aside first and restored if fn fails, so a
// failure never replaces a working index with a partial or lexical-only one.
//
// The copy survives a killed process: the next run settles it first
// (recoverSyncBackupLocked) instead of deleting it, and a cross-process guard
// keeps a second gleann from "recovering" an index that is being rebuilt.
func withIndexRollback(indexDir, name string, p *printer, fn func() error) error {
	return withIndexRollbackMeta(indexDir, name, p, func(*gleann.IndexMeta) error { return fn() })
}

// withIndexRollbackMeta is withIndexRollback whose fn also gets the index's
// metadata from before the rewrite, for the rebuild to carry over its user
// fields from its first meta.json on.
func withIndexRollbackMeta(indexDir, name string, p *printer, fn func(prev *gleann.IndexMeta) error) error {
	dir := filepath.Join(indexDir, name)
	bak := SyncBackupPath(indexDir, name)
	release, err := acquireRewriteGuard(indexDir, name)
	if err != nil {
		return err
	}
	defer release()
	if what, err := recoverSyncBackupLocked(indexDir, name); err != nil {
		return err
	} else if what != "" {
		p.Printf("ℹ️  A previous sync of %q was interrupted (rollback copy: %s)\n", name, what)
	}
	var prevMeta *gleann.IndexMeta
	moved := false
	if _, err := os.Stat(dir); err == nil {
		if err := fsretry.Rename(dir, bak); err != nil {
			return fmt.Errorf("preparing rollback copy: %w", err)
		}
		moved = true
		// Read the metadata from the moved copy: a PATCH that landed before
		// the move is in it, and one after the move cannot reach it.
		prevMeta = readBackupMeta(bak, name)
	}
	if err := fn(prevMeta); err != nil {
		if moved {
			_ = os.RemoveAll(dir)
			if rerr := fsretry.Rename(bak, dir); rerr != nil {
				return fmt.Errorf("%w (and restoring the previous index failed: %v; it is in %s)", err, rerr, bak)
			}
		}
		return err
	}
	if prevMeta != nil {
		// Keep what the user attached to the index across the rebuild.
		updateMetaLogged(indexDir, name, func(m *gleann.IndexMeta) {
			// The rebuild wrote its file carrying prevMeta's user fields, so a
			// field that differs now was edited (PATCH) after that and is kept.
			cur := *m
			gleann.MergeUserMeta(m, prevMeta, prevMeta, &cur)
			m.SourceDir = prevMeta.SourceDir // the rebuild's own value is a default, not an edit
			if !prevMeta.CreatedAt.IsZero() {
				m.CreatedAt = prevMeta.CreatedAt
			}
		})
	}
	if moved {
		_ = os.RemoveAll(bak)
	}
	return nil
}

// needsFullRebuild reports whether a failed incremental update can only be
// repaired by rebuilding the index from its sources: the passage store is
// missing, unreadable or damaged, or the backend cannot edit it in place
// (gleann.ErrRebuildRequired).
//
// Everything else is returned to the caller: a transient failure (embedder
// unreachable or failing, plugin extraction, source read error) would fail the
// same way in a rebuild, and re-embedding the whole corpus for it is worse than
// returning the error and letting the next sync retry the delta.
func needsFullRebuild(incErr error) bool {
	return errors.Is(incErr, gleann.ErrRebuildRequired)
}

// SyncChanged applies one batch of file-system events (index watch) to an
// existing index. It is a Sync limited to the given paths, so a watch update
// gets what `index sync` gets: the index's recorded embedding model is
// reconciled (a model chosen on purpose that differs is refused instead of
// mixing vectors), the index is locked, a failed full rebuild is rolled back,
// the graph is updated and the manifest advances. Paths may be relative to
// the process directory, as fsnotify reports them; a path that no longer
// exists is a deletion.
func SyncChanged(ctx context.Context, opts SyncOptions, files []string) (*SyncResult, error) {
	abs := make([]string, 0, len(files))
	for _, f := range files {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if a, err := filepath.Abs(f); err == nil {
			f = a
		}
		abs = append(abs, f)
	}
	if len(abs) == 0 {
		return &SyncResult{Name: opts.Name, UpToDate: true}, nil
	}
	opts.Files = abs
	return Sync(ctx, opts)
}

// expandVanishedDirs adds, for every vanished path in deleted, the indexed
// files that lived under it. A rename or removal of a directory reaches a
// watcher as a single event for the directory itself, which names no file; the
// index's manifest says which files were below it. The manifest is walked once
// and each entry is matched by looking up its ancestor directories in the set
// of vanished paths (a path-boundary match), so the cost is O(entries x depth)
// however many paths were deleted, not O(deleted x entries). Files that still
// exist (moved back, or excluded again) are left to the normal change path. A
// manifest that cannot be read leaves the list as it was: the next full sync
// repairs it.
func expandVanishedDirs(indexDir, name, absDocsDir string, deleted []string) []string {
	if len(deleted) == 0 {
		return deleted
	}
	m, err := gleann.LoadIndexManifest(indexDir, name)
	if err != nil || m == nil || len(m.Entries) == 0 {
		return deleted
	}
	return expandVanishedFrom(absDocsDir, deleted, m.Entries)
}

// expandVanishedFrom is expandVanishedDirs over an already loaded manifest.
func expandVanishedFrom(absDocsDir string, deleted []string, entries map[string]gleann.ManifestEntry) []string {
	return expandVanishedCounted(absDocsDir, deleted, entries, nil)
}

// expandStats counts the work of one expansion; tests pass one to assert the
// operation count instead of wall-clock time.
type expandStats struct {
	entries int // manifest entries examined
	lookups int // ancestor-map lookups
	stats   int // os.Stat calls
}

func expandVanishedCounted(absDocsDir string, deleted []string, entries map[string]gleann.ManifestEntry, st *expandStats) []string {
	have := make(map[string]bool, len(deleted))
	vanished := make(map[string]bool, len(deleted)) // slash-relative paths
	for _, d := range deleted {
		have[d] = true
		rel, err := filepath.Rel(absDocsDir, d)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		vanished[filepath.ToSlash(rel)] = true
	}
	if len(vanished) == 0 {
		return deleted
	}
	out := deleted
	for key := range entries {
		if st != nil {
			st.entries++
		}
		if !underVanished(key, vanished, st) {
			continue
		}
		abs := filepath.Join(absDocsDir, filepath.FromSlash(key))
		if have[abs] {
			continue
		}
		if st != nil {
			st.stats++
		}
		if _, err := os.Stat(abs); err == nil || !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		have[abs] = true
		out = append(out, abs)
	}
	sort.Strings(out[len(deleted):])
	return out
}

// underVanished reports whether a strict ancestor directory of the
// slash-separated key is in vanished.
func underVanished(key string, vanished map[string]bool, st *expandStats) bool {
	for i := len(key) - 1; i > 0; i-- {
		if key[i] != '/' {
			continue
		}
		if st != nil {
			st.lookups++
		}
		if vanished[key[:i]] {
			return true
		}
	}
	return false
}
