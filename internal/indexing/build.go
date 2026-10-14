package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register the vector backends so the library is self-contained.
	_ "github.com/tevfik/gleann/pkg/backends"
)

// ErrNoDocuments is returned by Build when the source directory contains no
// indexable content.
var ErrNoDocuments = errors.New("no documents found")

// BuildOptions configures Build.
type BuildOptions struct {
	Options

	// Name of the index to create. Required.
	Name string
	// DocsDir is the source directory to index. Required.
	DocsDir string

	// InstantOnly builds only the lexical (BM25) index, skipping embeddings.
	InstantOnly bool
	// Graph additionally builds the AST/document graph (treesitter builds).
	Graph bool
	// GraphReport writes GRAPH_REPORT.md into DocsDir after a graph build.
	GraphReport bool

	// Tags, Description and MCPExposed are persisted into the index meta.
	Tags        []string
	Description string
	MCPExposed  *bool

	// CodeModel / DocModel select specialised embedding models. When Mode
	// is ModeAll and both are set, Build produces two indexes,
	// "<Name>-code" (ModeCode, CodeModel) and "<Name>-docs" (ModeDocs,
	// DocModel). With ModeCode / ModeDocs the matching model replaces
	// Config.EmbeddingModel.
	CodeModel string
	DocModel  string

	// Estimate is an optional pre-computed job estimate (used to scale
	// byte-based progress). It is computed when nil.
	Estimate *gleann.JobEstimate
}

// BuildResult describes a completed build.
type BuildResult struct {
	Name    string
	DocsDir string
	// Chunks is the number of passages written.
	Chunks int
	// PluginDocs are the structured documents collected for graph indexing.
	PluginDocs []*PluginDoc
	Elapsed    time.Duration
	// CacheHits / CacheLookups report embedding cache effectiveness.
	CacheHits    int
	CacheLookups int
	// GraphReportPath is set when a graph report was written.
	GraphReportPath string
	Estimate        *gleann.JobEstimate
	// Parts holds the per-index results of a dual-model build.
	Parts []*BuildResult
}

// Build builds a new index (passages, lexical index and — unless
// InstantOnly — vector embeddings), records index metadata, optionally
// builds the code graph, and saves the Merkle manifest used by Sync.
//
// Build is safe for concurrent use; builds of the same index are
// serialised within the process. Cancelling ctx aborts the read and
// embedding phases.
func Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Name == "" {
		return nil, errors.New("index name required")
	}
	if opts.DocsDir == "" {
		return nil, errors.New("source directory required")
	}

	switch opts.mode() {
	case ModeAll:
		if opts.CodeModel != "" && opts.DocModel != "" {
			return buildDual(ctx, opts)
		}
	case ModeCode:
		if opts.CodeModel != "" {
			opts.Config.EmbeddingModel = opts.CodeModel
		}
	case ModeDocs:
		if opts.DocModel != "" {
			opts.Config.EmbeddingModel = opts.DocModel
		}
	}
	return buildOne(ctx, opts)
}

// buildDual builds <name>-code and <name>-docs in two targeted passes.
func buildDual(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	p := opts.printer()
	p.Printf("⚡ Dual-model mode active:\n")
	p.Printf("   💻 Code model: %s\n", opts.CodeModel)
	p.Printf("   📄 Docs model: %s\n\n", opts.DocModel)

	start := time.Now()
	res := &BuildResult{Name: opts.Name, DocsDir: opts.DocsDir}

	code := opts
	code.Name = opts.Name + "-code"
	code.Mode = ModeCode
	code.Config.EmbeddingModel = opts.CodeModel
	code.CodeModel, code.DocModel = "", ""
	p.Printf("📦 Step 1/2: Building code index %q...\n", code.Name)
	codeRes, err := buildOne(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("code index %q: %w", code.Name, err)
	}

	docs := opts
	docs.Name = opts.Name + "-docs"
	docs.Mode = ModeDocs
	docs.Config.EmbeddingModel = opts.DocModel
	docs.CodeModel, docs.DocModel = "", ""
	p.Printf("\n📦 Step 2/2: Building docs index %q...\n", docs.Name)
	docsRes, err := buildOne(ctx, docs)
	if err != nil {
		return nil, fmt.Errorf("docs index %q: %w", docs.Name, err)
	}

	p.Printf("\n🎉 Dual-index build complete for %q!\n", opts.Name)
	p.Printf("   💻 Code index: %s-code\n", opts.Name)
	p.Printf("   📄 Docs index: %s-docs\n", opts.Name)

	res.Parts = []*BuildResult{codeRes, docsRes}
	res.Chunks = codeRes.Chunks + docsRes.Chunks
	res.Elapsed = time.Since(start)
	return res, nil
}

func buildOne(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	p := opts.printer()
	cfg := opts.Config
	name, docsDir := opts.Name, opts.DocsDir
	mode := opts.mode()

	unlock, err := lockIndex(ctx, cfg.IndexDir, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	est := opts.Estimate
	if est == nil {
		est, err = gleann.EstimateJob(docsDir, runtime.NumCPU())
		if err != nil {
			return nil, fmt.Errorf("estimating job: %w", err)
		}
	}
	prog := newProgressEmitter(opts.Progress)

	switch mode {
	case ModeCode:
		p.Printf("⚡ Index mode: CODE (fast code & AST graph, skipping office doc plugins)\n")
	case ModeDocs:
		p.Printf("📄 Index mode: DOCUMENTS (office docs only, skipping raw code)\n")
	}

	// Multimodal model for indexing media files (images, audio, video).
	mmProcessor := NewMultimodalProcessor(cfg.OllamaHost, cfg.APIKeyForProvider("ollama"), opts.multimodalModel(), p.Printf)

	var cached *embedding.CachedComputer
	if !opts.InstantOnly {
		var cleanup func()
		cached, cleanup, err = resolveEmbedder(ctx, &opts.Options, &cfg, p)
		defer cleanup()
		if err != nil {
			return nil, fmt.Errorf("initializing embedder: %w", err)
		}
	}

	var builderEmb gleann.EmbeddingComputer
	if cached != nil {
		builderEmb = cached
	}
	builder, err := gleann.NewBuilder(cfg, builderEmb)
	if err != nil {
		return nil, err
	}
	builder.SetProgressCallback(func(phase string, done, total int) {
		approxBytes := int64(done) * est.TotalBytes / int64(max(total, 1))
		prog.emit(phase, done, total, approxBytes, est.TotalBytes, done == total)
	})

	tracker, release := acquireTracker(&opts.Options, p, "warning: could not initialize vault tracker: %v\n")
	defer release()

	// Read documents from directory.
	prog.emit("read_documents", 0, est.FileCount, 0, est.TotalBytes, true)
	p.Printf("📂 Reading documents from %s...\n", docsDir)
	items, pluginDocs, err := readDocuments(ctx, docsDir, readOptionsFor(cfg, &opts.Options, tracker, mmProcessor), p)
	if err != nil {
		return nil, fmt.Errorf("reading documents: %w", err)
	}
	if len(items) == 0 {
		return nil, ErrNoDocuments
	}

	p.Printf("📝 Found %d text chunks\n", len(items))
	if cfg.ChunkConfig.SignaturesOnly {
		p.Printf("✂️  Signatures-only chunking enabled (body implementations omitted for fast embedding)\n")
	}

	start := time.Now()
	if opts.InstantOnly {
		p.Printf("⚡ Building instant lexical index %q (skipping vector embeddings)...\n", name)
		if err := builder.BuildLexicalOnly(name, items); err != nil {
			return nil, fmt.Errorf("building lexical index: %w", err)
		}
		p.Printf("✅ Instant lexical index ready for %q (%d chunks) in %s\n", name, len(items), time.Since(start).Round(time.Millisecond))
	} else {
		p.Printf("⚡ Phase 1: Storing instant lexical passages for %q...\n", name)
		ids, err := builder.BuildPassages(name, items)
		if err != nil {
			return nil, fmt.Errorf("building passages: %w", err)
		}
		p.Printf("   ✅ Passages ready (%d chunks) — lexical BM25 search immediately active.\n", len(ids))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.Printf("🧠 Phase 2: Computing vector embeddings with model %s...\n", cfg.EmbeddingModel)
		prog.emit("vector_index", 0, len(items), est.TotalBytes, est.TotalBytes, true)
		if err := builder.BuildVectors(ctx, name, items, ids); err != nil {
			return nil, fmt.Errorf("building vector index: %w", err)
		}
	}

	_ = gleann.UpdateIndexMeta(cfg.IndexDir, name, func(meta *gleann.IndexMeta) {
		meta.SourceDir = docsDir
		for _, t := range opts.Tags {
			t = strings.TrimSpace(t)
			if t != "" && !meta.HasTag(t) {
				meta.Tags = append(meta.Tags, strings.TrimPrefix(t, "@"))
			}
		}
		if opts.Description != "" {
			meta.Description = opts.Description
		}
		if opts.MCPExposed != nil {
			exp := *opts.MCPExposed
			meta.MCPExposed = &exp
		}
	})

	res := &BuildResult{
		Name:       name,
		DocsDir:    docsDir,
		Chunks:     len(items),
		PluginDocs: pluginDocs,
		Elapsed:    time.Since(start),
		Estimate:   est,
	}
	p.Printf("✅ Vector Index %q built: %d passages in %s\n", name, len(items), res.Elapsed.Round(time.Millisecond))

	// Report embedding cache stats.
	if cached != nil {
		if hits, total := cached.Stats(); total > 0 {
			res.CacheHits, res.CacheLookups = hits, total
			p.Printf("💾 Embedding cache: %d/%d hits (%.0f%%)\n", hits, total, cached.HitRate())
		}
	}

	if opts.Graph {
		buildGraphIndex(GraphOptions{
			Name:              name,
			DocsDir:           docsDir,
			IndexDir:          cfg.IndexDir,
			PluginDocs:        pluginDocs,
			IncludeSubmodules: opts.IncludeSubmodules,
		}, p)
		if opts.GraphReport {
			graphReportPath := filepath.Join(docsDir, "GRAPH_REPORT.md")
			if err := GenerateGraphReport(name, cfg.IndexDir, docsDir, graphReportPath); err == nil {
				res.GraphReportPath = graphReportPath
				p.Printf("📊 GRAPH_REPORT.md generated in %s\n", graphReportPath)
			}
		}
	}

	// Build and persist Manifest for instant diffing and Merkle tree verification.
	saveManifestFromItems(cfg.IndexDir, name, docsDir, items)

	return res, nil
}

func manifestPath(indexDir, name string) string {
	return filepath.Join(indexDir, name, name+".manifest.json")
}

func saveManifestFromItems(indexDir, name, docsDir string, items []gleann.Item) {
	var manifestFiles []string
	seenFiles := make(map[string]bool)
	for _, it := range items {
		if src, ok := it.Metadata["source"].(string); ok && src != "" && !seenFiles[src] {
			seenFiles[src] = true
			if !filepath.IsAbs(src) {
				src = filepath.Join(docsDir, src)
			}
			manifestFiles = append(manifestFiles, src)
		}
	}
	if mf, mErr := gleann.BuildManifest(name, docsDir, manifestFiles); mErr == nil {
		_ = gleann.SaveManifest(manifestPath(indexDir, name), mf)
	}
}

// pipelineEnv bundles the resolved resources shared by the rebuild and
// incremental helpers.
type pipelineEnv struct {
	cfg      gleann.Config
	embedder gleann.EmbeddingComputer
	tracker  *vault.Tracker
	opts     *Options
	p        *printer
}

// RebuildIndex re-reads docsDir and rebuilds the index in one shot (used by
// watch mode and as Sync's fallback). opts.Embedder / opts.Tracker are used
// when set; otherwise they are resolved for the duration of the call.
func RebuildIndex(ctx context.Context, name, docsDir string, opts Options) ([]*PluginDoc, error) {
	env, cleanup, err := newPipelineEnv(ctx, &opts, true)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return env.rebuild(ctx, name, docsDir)
}

// UpdateIncremental re-chunks only changedFiles (existing files are
// re-added, missing ones removed) and applies the delta to the index. It
// returns an error when the backend cannot apply deltas, in which case the
// caller should fall back to RebuildIndex.
func UpdateIncremental(ctx context.Context, name, docsDir string, changedFiles []string, opts Options) ([]*PluginDoc, error) {
	env, cleanup, err := newPipelineEnv(ctx, &opts, true)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return env.incremental(ctx, name, docsDir, changedFiles)
}

func newPipelineEnv(ctx context.Context, opts *Options, needEmbedder bool) (*pipelineEnv, func(), error) {
	p := opts.printer()
	env := &pipelineEnv{cfg: opts.Config, opts: opts, p: p}
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	if needEmbedder {
		if c, ok := opts.Embedder.(*embedding.CachedComputer); ok {
			env.embedder = c
		} else {
			cached, stop, err := resolveEmbedder(ctx, opts, &env.cfg, p)
			if err != nil {
				stop()
				return nil, func() {}, fmt.Errorf("initializing embedder: %w", err)
			}
			cleanups = append(cleanups, stop)
			env.embedder = cached
		}
	}
	t, release := acquireTracker(opts, p, "warning: could not initialize vault tracker: %v\n")
	cleanups = append(cleanups, release)
	env.tracker = t
	return env, cleanup, nil
}

func (e *pipelineEnv) readOptions() ReadOptions {
	mm := NewMultimodalProcessor(e.cfg.OllamaHost, e.cfg.APIKeyForProvider("ollama"), e.opts.multimodalModel(), e.p.Printf)
	return readOptionsFor(e.cfg, e.opts, e.tracker, mm)
}

func (e *pipelineEnv) rebuild(ctx context.Context, name, docsDir string) ([]*PluginDoc, error) {
	items, pluginDocs, err := readDocuments(ctx, docsDir, e.readOptions(), e.p)
	if err != nil {
		return nil, fmt.Errorf("reading documents: %w", err)
	}
	if len(items) == 0 {
		return pluginDocs, nil
	}

	builder, err := gleann.NewBuilder(e.cfg, e.embedder)
	if err != nil {
		return nil, err
	}

	// A rebuild recreates meta.json; carry over user-managed fields.
	prev, prevErr := gleann.GetIndexMeta(e.cfg.IndexDir, name)

	start := time.Now()
	if err := builder.Build(ctx, name, items); err != nil {
		return nil, fmt.Errorf("building: %w", err)
	}
	_ = gleann.UpdateIndexMeta(e.cfg.IndexDir, name, func(meta *gleann.IndexMeta) {
		meta.SourceDir = docsDir
		if prevErr == nil && prev != nil {
			if !prev.CreatedAt.IsZero() {
				meta.CreatedAt = prev.CreatedAt
			}
			meta.Tags, meta.Description = prev.Tags, prev.Description
			meta.MCPExposed, meta.AutoWatch = prev.MCPExposed, prev.AutoWatch
		}
	})
	e.p.Printf("✅ Rebuilt %q: %d passages in %s\n", name, len(items), time.Since(start).Round(time.Millisecond))
	return pluginDocs, nil
}

func (e *pipelineEnv) incremental(ctx context.Context, name, docsDir string, changedFiles []string) ([]*PluginDoc, error) {
	// Classify changes: existing files need re-chunking, missing files are deletions.
	var existingFiles []string
	var removeSources []string
	for _, f := range changedFiles {
		relPath, _ := filepath.Rel(docsDir, f)
		if _, err := os.Stat(f); err != nil {
			removeSources = append(removeSources, relPath)
		} else {
			// File exists (new or modified) — remove old passages, then re-add.
			existingFiles = append(existingFiles, f)
			removeSources = append(removeSources, relPath)
		}
	}

	items, pluginDocs, records, err := readDocumentsForFiles(ctx, docsDir, existingFiles, e.readOptions(), e.p)
	if err != nil {
		return nil, fmt.Errorf("incremental: reading changed files: %w", err)
	}

	if len(items) == 0 && len(removeSources) == 0 {
		if e.tracker != nil && len(records) > 0 {
			_ = e.tracker.BatchUpsertRecords(ctx, records)
		}
		return pluginDocs, nil
	}

	builder, err := gleann.NewBuilder(e.cfg, e.embedder)
	if err != nil {
		return nil, fmt.Errorf("incremental: creating builder: %w", err)
	}

	if err := builder.UpdateIndex(ctx, name, items, removeSources); err != nil {
		// UpdateIndex may fail if backend doesn't support removal (e.g., FAISS).
		return nil, fmt.Errorf("incremental update failed: %w", err)
	}

	if e.tracker != nil && len(records) > 0 {
		_ = e.tracker.BatchUpsertRecords(ctx, records)
	}
	return pluginDocs, nil
}
