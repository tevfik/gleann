package main

// CLI front-end for the index build / sync / watch pipeline. The pipeline
// itself lives in internal/indexing; this file only parses flags, renders
// terminal output (pre-flight estimate, decision gate, progress pacer) and
// maps errors to exit codes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

var exitFunc = os.Exit

// IndexMode represents the filtering mode for indexing.
type IndexMode = indexing.Mode

const (
	IndexModeAll  = indexing.ModeAll
	IndexModeCode = indexing.ModeCode
	IndexModeDocs = indexing.ModeDocs
)

func parseIndexMode(args []string) (IndexMode, bool) {
	modeStr := strings.ToLower(getFlag(args, "--mode"))
	if modeStr == "" {
		if hasFlag(args, "--code-only") || hasFlag(args, "--code") {
			modeStr = "code"
		} else if hasFlag(args, "--docs-only") {
			modeStr = "docs"
		} else {
			modeStr = "all"
		}
	}
	noPlugins := hasFlag(args, "--no-plugins") || modeStr == "code"
	return IndexMode(modeStr), noPlugins
}

// stdOptions returns pipeline options that print to the process's current
// stdout/stderr (looked up at call time so tests can redirect them).
func stdOptions(config gleann.Config, mode IndexMode, noPlugins, includeSubmodules bool) indexing.Options {
	return indexing.Options{
		Config:            config,
		Mode:              mode,
		NoPlugins:         noPlugins,
		IncludeSubmodules: includeSubmodules,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	}
}

func printBuildUsage() {
	fmt.Println(`Usage: gleann index build <name> --docs <dir> [options]

Build a vector index (and optional AST code graph) from documents or source code.

Arguments:
  <name>                  Name of the index to create

Options:
  --docs <dir>            Source directory containing files to index (required)
  --graph                 Build AST-based code graph using tree-sitter & Kùzu
  --signatures            Index only signatures & docstrings for code (70-80% faster embedding)
  --instant               Build instant lexical BM25 index only (skips vector embeddings)
  --lexical-only          Alias for --instant
  --code-model <m>        Model for code indexing (triggers dual-index build if --doc-model is also set)
  --doc-model <m>         Model for documentation indexing (alias: --docs-model)
  --mode <code|docs|all>  Index mode:
                            code - fast source code & AST graph only
                            docs - documents only (pdf, docx, etc.)
                            all  - index everything (default)
  --dry-run               Estimate workload & Graham makespan bounds without indexing
  --max-minutes <n>       Maximum acceptable minutes before halting for decision gate (Exit 4)
  --approve <action>      Decision approval: proceed, fast, or cancel
  --yes, -y               Auto-approve decision gate (proceed)
  --progress <surface>    Progress reporting surface: plain (default), json, none
  --include-submodules    Include Git submodule directories (default: excluded)
  --tag <tag>             Assign governance tag(s) (comma-separated or multiple)
  --desc <description>    Human-readable description for semantic MCP routing
  --mcp                   Expose index to MCP tools (default: true)
  --no-plugins            Disable external document extraction plugins
  --multimodal-model <m>  Model for media files (images, audio, video)
  --host <url>            Ollama server address (default: saved config / OLLAMA_HOST)
  --ollama-api-key <key>  Optional Ollama API key (auth proxy / hosted Ollama; env: OLLAMA_API_KEY)
  --no-report             Skip automatic GRAPH_REPORT.md generation
  --no-agents             Skip automatic AGENTS.md generation
  -h, --help              Show this help message

Examples:
  gleann index build core --docs ./src --graph --mode code
  gleann index build docs --docs ./documentation --tag docs,work
  gleann index build repo --docs ./src --code-model granite-30m --doc-model bge-small
  gleann index build repo --docs ./src --max-minutes 5 --approve fast`)
}

func cmdBuild(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		printBuildUsage()
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			return
		}
		exitFunc(1)
		return
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		fmt.Fprintf(os.Stderr, "error: index name %q looks like a flag\nusage: gleann index build <name> --docs <dir>\n", name)
		exitFunc(1)
		return
	}
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --docs flag required")
		exitFunc(1)
		return
	}
	buildGraph := hasFlag(args, "--graph")

	mode, noPlugins := parseIndexMode(args)

	dryRun := hasFlag(args, "--dry-run")

	codeModel := getFlag(args, "--code-model")
	if codeModel == "" {
		codeModel = getFlag(args, "--code-embedding-model")
	}
	docModel := getFlag(args, "--doc-model")
	if docModel == "" {
		docModel = getFlag(args, "--docs-model")
	}
	if docModel == "" {
		docModel = getFlag(args, "--doc-embedding-model")
	}

	// Dual-model mode: If both code and doc models are specified (and mode is 'all'),
	// automatically build <name>-code and <name>-docs in two targeted passes.
	// Each pass goes through the full CLI flow (estimate, decision gate, ...).
	if !dryRun && mode == IndexModeAll && codeModel != "" && docModel != "" {
		fmt.Printf("⚡ Dual-model mode active:\n")
		fmt.Printf("   💻 Code model: %s\n", codeModel)
		fmt.Printf("   📄 Docs model: %s\n\n", docModel)

		fmt.Printf("📦 Step 1/2: Building code index %q...\n", name+"-code")
		cmdBuild(dualPassArgs(args, name, name+"-code", "code", codeModel))

		fmt.Printf("\n📦 Step 2/2: Building docs index %q...\n", name+"-docs")
		cmdBuild(dualPassArgs(args, name, name+"-docs", "docs", docModel))

		fmt.Printf("\n🎉 Dual-index build complete for %q!\n", name)
		fmt.Printf("   💻 Code index: %s-code\n", name)
		fmt.Printf("   📄 Docs index: %s-docs\n", name)
		fmt.Printf("   🔍 Search both concurrently: gleann search %s \"<query>\"\n", name)
		return
	}

	if mode == IndexModeCode && codeModel != "" && getFlag(args, "--model") == "" {
		args = append(args, "--model", codeModel)
	} else if mode == IndexModeDocs && docModel != "" && getFlag(args, "--model") == "" {
		args = append(args, "--model", docModel)
	}
	maxMinutesStr := getFlag(args, "--max-minutes")
	var maxMinutes float64
	if maxMinutesStr != "" {
		if val, err := strconv.ParseFloat(maxMinutesStr, 64); err == nil {
			maxMinutes = val
		}
	}
	approve := getFlag(args, "--approve")
	if approve == "" && (hasFlag(args, "--yes") || hasFlag(args, "-y")) {
		approve = "proceed"
	}
	jsonOutput := hasFlag(args, "--json")
	progressSurface := gleann.ProgressSurface(getFlag(args, "--progress"))
	pacer := gleann.NewPacer(progressSurface, os.Stderr)

	// Pre-flight job estimation using Graham (1969) list scheduling bounds
	est, err := gleann.EstimateJob(docsDir, runtime.NumCPU())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error estimating job: %v\n", err)
		exitFunc(1)
		return
	}

	if dryRun {
		if jsonOutput {
			data, _ := json.MarshalIndent(est, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("📊 Pre-flight Job Estimate for %s:\n", docsDir)
			fmt.Printf("   Files: %d\n", est.FileCount)
			fmt.Printf("   Total Size: %s\n", formatSize(est.TotalBytes))
			fmt.Printf("   Sampled Read Rate: %.1f MB/s\n", est.SampledRate/(1024*1024))
			fmt.Printf("   Workers: %d\n", est.Workers)
			fmt.Printf("   Graham Makespan Bounds: %.2fs - %.2fs (%.2fm - %.2fm)\n",
				est.EstimatedSecLow, est.EstimatedSecHigh,
				est.EstimatedSecLow/60.0, est.EstimatedSecHigh/60.0)
		}
		return
	}

	gate := gleann.EvaluateDecisionGate(est, maxMinutes, approve)
	if gate.Triggered {
		data, _ := json.MarshalIndent(gate, "", "  ")
		fmt.Println(string(data))
		exitFunc(4)
		return
	}

	if gate.Action == gleann.GateActionCancel {
		fmt.Println("Indexing cancelled by approval flag.")
		return
	}

	if gate.Action == gleann.GateActionFast {
		fmt.Println("⚡ Approval mode 'fast' selected: switching to code-only indexing.")
		mode = IndexModeCode
		noPlugins = true
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		exitFunc(1)
		return
	}

	tagsFlag := getFlag(args, "--tags")
	if tagsFlag == "" {
		tagsFlag = getFlag(args, "--tag")
	}
	var tags []string
	if tagsFlag != "" {
		tags = strings.Split(tagsFlag, ",")
	}
	var mcpExposed *bool
	if mcpFlag := getFlag(args, "--mcp"); mcpFlag != "" {
		exp := mcpFlag == "true" || mcpFlag == "1" || mcpFlag == "yes"
		mcpExposed = &exp
	}

	opts := indexing.BuildOptions{
		Options:     stdOptions(config, mode, noPlugins, hasFlag(args, "--include-submodules")),
		Name:        name,
		DocsDir:     docsDir,
		InstantOnly: hasFlag(args, "--instant") || hasFlag(args, "--lexical-only"),
		Graph:       buildGraph,
		GraphReport: hasFlag(args, "--report") || hasFlag(args, "--graph-report"),
		Tags:        tags,
		Description: getFlag(args, "--desc"),
		MCPExposed:  mcpExposed,
		Estimate:    est,
	}
	opts.MultimodalModel = getFlag(args, "--multimodal-model")
	opts.Progress = func(p indexing.Progress) {
		pacer.Tick(p.Phase, p.ItemsDone, p.TotalItems, p.BytesDone, p.TotalBytes, p.Force)
	}

	if _, err := indexing.Build(context.Background(), opts); err != nil {
		if errors.Is(err, indexing.ErrNoDocuments) {
			fmt.Fprintln(os.Stderr, "error: no documents found")
		} else {
			pacer.Done(false, 1, est.FileCount, est.TotalBytes)
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		exitFunc(1)
		return
	}

	if hasFlag(args, "--agents") || hasFlag(args, "--write-agents") {
		agentsPath := filepath.Join(docsDir, "AGENTS.md")
		content := getAgentsMDContent(name)
		if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err == nil {
			fmt.Printf("🤖 AGENTS.md generated/updated in %s\n", agentsPath)
		}
	}

	pacer.Done(true, 0, est.FileCount, est.TotalBytes)
}

// dualPassArgs rewrites build args for one pass of a dual-model build:
// the index name is replaced and --model/--mode/--code-model/--doc-model
// are swapped for the pass-specific mode and model.
func dualPassArgs(args []string, name, passName, passMode, passModel string) []string {
	out := make([]string, 0, len(args)+4)
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			out = append(out, passName)
		} else if args[i] == "--model" || args[i] == "--mode" ||
			strings.HasPrefix(args[i], "--code-model") ||
			strings.HasPrefix(args[i], "--doc-model") ||
			strings.HasPrefix(args[i], "--docs-model") {
			i++ // skip flag and value
		} else {
			out = append(out, args[i])
		}
	}
	return append(out, "--mode", passMode, "--model", passModel)
}

// cmdRebuild removes an existing index and rebuilds it from scratch.
func cmdRebuild(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			fmt.Println(`Usage: gleann index rebuild <name> --docs <dir> [options]

Remove and completely rebuild an index from scratch. Supports all flags from 'index build'.

Options:
  --docs <dir>            Source directory containing files to index (required)
  --graph                 Build AST-based code graph using tree-sitter & Kùzu
  --mode <code|docs|all>  Index mode (code, docs, all)
  -h, --help              Show this help message`)
			return
		}
		fmt.Fprintln(os.Stderr, "usage: gleann rebuild <name> --docs <dir>")
		os.Exit(1)
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		fmt.Fprintf(os.Stderr, "error: index name %q looks like a flag\nusage: gleann index rebuild <name> --docs <dir>\n", name)
		os.Exit(1)
	}
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --docs flag required")
		os.Exit(1)
	}

	config := getConfig(args)

	// Step 1: Remove existing index (ignore error if it doesn't exist)
	fmt.Printf("🗑️  Removing existing index %q...\n", name)
	if err := gleann.RemoveIndex(config.IndexDir, name); err != nil {
		fmt.Printf("   (no existing index to remove: %v)\n", err)
	} else {
		fmt.Printf("   ✅ Removed.\n")
	}

	// Step 2: Build fresh
	fmt.Printf("🔨 Rebuilding index %q from %s...\n", name, docsDir)
	cmdBuild(args)
}

func cmdWatch(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			fmt.Println(`Usage: gleann index watch <name> --docs <dir> [options]

Watch a source directory and automatically re-index on file changes.

Options:
  --docs <dir>            Source directory to watch (required)
  --graph                 Also update AST call graph
  --mode <code|docs|all>  Index mode: code, docs, all (default: all)
  --interval <seconds>    Polling interval (default: 5)
  -h, --help              Show this help message`)
			return
		}
		fmt.Fprintln(os.Stderr, "usage: gleann index watch <name> --docs <dir> [--graph] [--interval 5]")
		os.Exit(1)
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		fmt.Fprintf(os.Stderr, "error: index name %q looks like a flag\nusage: gleann index watch <name> --docs <dir>\n", name)
		os.Exit(1)
	}
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --docs flag required")
		os.Exit(1)
	}

	buildGraph := hasFlag(args, "--graph")

	intervalStr := getFlag(args, "--interval")
	interval := 5 * time.Second
	if intervalStr != "" {
		var secs int
		fmt.Sscanf(intervalStr, "%d", &secs)
		if secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	// Wrap with embedding cache for rebuild efficiency.
	cachedEmbedder := embedding.NewCachedComputer(newEmbedder(config), embedding.CacheOptions{})

	fmt.Printf("👁️  Watching %s for changes via fsnotify (debounce: %s)\n", docsDir, interval)
	fmt.Printf("   Index: %s\n", name)
	fmt.Println("   Press Ctrl+C to stop.")

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Initialize Vault Tracker & Watcher
	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing vault tracker: %v\n", err)
		os.Exit(1)
	}
	defer tracker.Close()

	watcher, err := vault.NewWatcher(tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing vault watcher: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()

	mode, noPlugins := parseIndexMode(args)
	includeSubmodules := hasFlag(args, "--include-submodules")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := stdOptions(config, mode, noPlugins, includeSubmodules)
	opts.Embedder = cachedEmbedder
	opts.Tracker = tracker

	rebuild := func() []*PluginDoc {
		pDocs, err := indexing.RebuildIndex(ctx, name, docsDir, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		return pDocs
	}

	// Initial build.
	pluginDocs := rebuild()
	if buildGraph {
		buildGraphIndex(name, docsDir, config.IndexDir, pluginDocs, nil, includeSubmodules)
	}

	// Accumulate changed file paths from fsnotify events.
	var changedMu sync.Mutex
	changedFiles := make(map[string]bool)

	buildRequested := make(chan struct{}, 1)
	watcher.OnChange = func(event fsnotify.Event) {
		changedMu.Lock()
		changedFiles[event.Name] = true
		changedMu.Unlock()
		select {
		case buildRequested <- struct{}{}:
		default:
		}
	}

	if err := watcher.AddDirectory(docsDir); err != nil {
		fmt.Fprintf(os.Stderr, "error adding watch dir: %v\n", err)
		os.Exit(1)
	}

	watcher.Start(ctx)

	// Rate-limited rebuilder loop
	for {
		select {
		case <-stop:
			fmt.Println("\nStopping watcher...")
			return
		case <-buildRequested:
			// Wait for debounce interval to coalesce changes
			time.Sleep(interval)

			// Collect changed files accumulated during debounce window.
			changedMu.Lock()
			files := make([]string, 0, len(changedFiles))
			for f := range changedFiles {
				files = append(files, f)
			}
			changedFiles = make(map[string]bool) // reset
			changedMu.Unlock()

			fmt.Printf("🔄 %d file(s) changed, updating index %q...\n", len(files), name)
			start := time.Now()
			pDocs, err := indexing.UpdateIncremental(ctx, name, docsDir, files, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v, falling back to full rebuild\n", err)
				pDocs = rebuild()
			} else {
				fmt.Printf("⚡ Incremental update complete in %s\n", time.Since(start).Round(time.Millisecond))
			}

			if buildGraph {
				buildGraphIndex(name, docsDir, config.IndexDir, pDocs, files, includeSubmodules)
			}

			// drain any queued up builds during sleep
			select {
			case <-buildRequested:
			default:
			}
		}
	}
}

// cmdSync performs an on-demand incremental synchronization of an index.
// It checks which files have been modified, added, or deleted in the workspace,
// updating both the vector index and the AST code graph.
//
// Usage:
//
//	gleann index sync <name> [--docs <dir>] [--files <file1,file2>] [--graph]
func cmdSync(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Fprintln(os.Stderr, "usage: gleann index sync <name> [--docs <dir>] [--files <file1,file2>] [--graph] [--mode code|docs|all] [--no-plugins] [--progress plain|json|none]")
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			return
		}
		exitFunc(1)
		return
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		fmt.Fprintf(os.Stderr, "error: index name %q looks like a flag\nusage: gleann index sync <name> [--docs <dir>]\n", name)
		exitFunc(1)
		return
	}

	mode, noPlugins := parseIndexMode(args)
	args = withModeModel(args, mode)

	config := getConfig(args)
	applySavedConfig(&config, args)

	instant := hasFlag(args, "--instant") || hasFlag(args, "--lexical-only")

	// Embedder (and, for the llamacpp provider, an embedded llama-server)
	// is resolved by the library only if a vector update is actually needed.
	var files []string
	if filesFlag := getFlag(args, "--files"); filesFlag != "" {
		files = strings.Split(filesFlag, ",")
	}

	stdOpts := stdOptions(config, mode, noPlugins, hasFlag(args, "--include-submodules"))
	stdOpts.ModelExplicit = modelExplicit(args)
	stdOpts.Progress = embedProgress(getFlag(args, "--progress"), name)
	opts := indexing.SyncOptions{
		Options:     stdOpts,
		Name:        name,
		DocsDir:     getFlag(args, "--docs"),
		Files:       files,
		Graph:       hasFlag(args, "--graph"),
		InstantOnly: instant,
	}
	opts.MultimodalModel = getFlag(args, "--multimodal-model")

	if _, err := indexing.Sync(context.Background(), opts); err != nil {
		if errors.Is(err, indexing.ErrNoSourceDir) {
			fmt.Fprintf(os.Stderr, "error: --docs flag required (index %q does not have a recorded source_dir)\n", name)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		exitFunc(1)
		return
	}
}

// ── Thin wrappers kept for the rest of package main and its tests ─────

// fileEntry represents a file path and its info for indexing.
type fileEntry struct {
	path string
	info os.FileInfo
}

// collectEligibleFiles walks dir and returns files eligible for indexing.
// See indexing.CollectEligibleFiles.
func collectEligibleFiles(dir string, pluginManager *gleann.PluginManager, nativeExtractor *gleann.NativeExtractor, mmProcessor *multimodal.Processor, mode IndexMode, includeSubmodules bool) ([]fileEntry, error) {
	entries, err := indexing.CollectEligibleFiles(context.Background(), dir, indexing.CollectOptions{
		Mode:              mode,
		IncludeSubmodules: includeSubmodules,
		PluginManager:     pluginManager,
		NativeExtractor:   nativeExtractor,
		Multimodal:        mmProcessor,
	})
	out := make([]fileEntry, len(entries))
	for i, e := range entries {
		out[i] = fileEntry{path: e.Path, info: e.Info}
	}
	return out, err
}

// buildGraphIndex builds the AST + document graph for an index, printing
// to stdout/stderr. No-op (with a warning) without -tags treesitter.
func buildGraphIndex(name, docsDir, indexDir string, pluginDocs []*PluginDoc, changedFiles []string, includeSubmodules bool) {
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:              name,
		DocsDir:           docsDir,
		IndexDir:          indexDir,
		PluginDocs:        pluginDocs,
		ChangedFiles:      changedFiles,
		IncludeSubmodules: includeSubmodules,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	})
}

// generateGraphReportFile generates a Markdown graph report to outputPath.
func generateGraphReportFile(indexName, indexDir, docsDir, outputPath string) error {
	return indexing.GenerateGraphReport(indexName, indexDir, docsDir, outputPath)
}
