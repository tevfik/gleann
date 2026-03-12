package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/gleannignore"
)

func cmdBuild(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann build <name> --docs <dir>")
		os.Exit(1)
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		fmt.Fprintf(os.Stderr, "error: index name %q looks like a flag\nusage: gleann index build <name> --docs <dir>\n", name)
		os.Exit(1)
	}
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --docs flag required")
		os.Exit(1)
	}
	buildGraph := hasFlag(args, "--graph")

	config := getConfig(args)
	applySavedConfig(&config, args)

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	embedder := embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(config.EmbeddingProvider),
		Model:       config.EmbeddingModel,
		BaseURL:     config.OllamaHost,
		BatchSize:   config.BatchSize,
		Concurrency: config.Concurrency,
	})

	// Wrap with embedding cache for rebuild efficiency.
	cachedEmbedder := embedding.NewCachedComputer(embedder, embedding.CacheOptions{})

	builder, err := gleann.NewBuilder(config, cachedEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Initialize vault tracker
	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not initialize vault tracker: %v\n", err)
	} else {
		defer tracker.Close()
	}

	// Read documents from directory.
	fmt.Printf("📂 Reading documents from %s...\n", docsDir)
	items, pluginDocs, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "error: no documents found")
		os.Exit(1)
	}

	fmt.Printf("📝 Found %d text chunks\n", len(items))
	fmt.Printf("🔧 Building index %q with model %s...\n", name, config.EmbeddingModel)

	start := time.Now()
	ctx := context.Background()
	if err := builder.Build(ctx, name, items); err != nil {
		fmt.Fprintf(os.Stderr, "error building index: %v\n", err)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("✅ Vector Index %q built: %d passages in %s\n", name, len(items), elapsed.Round(time.Millisecond))

	// Report embedding cache stats.
	if hits, total := cachedEmbedder.Stats(); total > 0 {
		fmt.Printf("💾 Embedding cache: %d/%d hits (%.0f%%)\n", hits, total, cachedEmbedder.HitRate())
	}

	if buildGraph {
		buildGraphIndex(name, docsDir, config.IndexDir, pluginDocs, nil)
	}
}

// cmdRebuild removes an existing index and rebuilds it from scratch.
func cmdRebuild(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann rebuild <name> --docs <dir>")
		os.Exit(1)
	}

	name := args[0]
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

func buildIndex(name, docsDir string, config gleann.Config, embedder gleann.EmbeddingComputer, tracker *vault.Tracker) []*PluginDoc {
	items, pluginDocs, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		return nil
	}
	if len(items) == 0 {
		return pluginDocs
	}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil
	}

	start := time.Now()
	ctx := context.Background()
	if err := builder.Build(ctx, name, items); err != nil {
		fmt.Fprintf(os.Stderr, "error building: %v\n", err)
		return nil
	}
	fmt.Printf("✅ Rebuilt %q: %d passages in %s\n", name, len(items), time.Since(start).Round(time.Millisecond))
	return pluginDocs
}

func readDocuments(dir string, chunkSize, chunkOverlap int, tracker *vault.Tracker) ([]gleann.Item, []*PluginDoc, error) {
	type fileEntry struct {
		path string
		info os.FileInfo
	}

	binaryExts := map[string]bool{
		".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
		".exe": true, ".bin": true, ".dll": true, ".so": true, ".dylib": true, ".o": true, ".a": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".svg": true, ".webp": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".flv": true, ".wav": true, ".flac": true, ".ogg": true,
		".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
		".db": true, ".sqlite": true, ".sqlite3": true,
		".pyc": true, ".class": true, ".jar": true, ".war": true,
		".iso": true, ".img": true, ".dmg": true, ".deb": true, ".rpm": true,
		".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	}

	// Load plugins once and manage their lifecycles
	pluginManager, _ := gleann.NewPluginManager()
	if pluginManager != nil {
		defer pluginManager.Close()
	}

	// Load .gleannignore patterns (empty matcher if no file).
	ignoreMatcher := gleannignore.Load(dir)

	// Phase 1: collect eligible file paths (serial walk is fast — just syscalls).
	var files []fileEntry
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)

		// Compute path relative to root dir for .gleannignore matching.
		relPath, _ := filepath.Rel(dir, path)

		if info.IsDir() {
			if strings.HasPrefix(base, ".") && path != dir {
				return filepath.SkipDir
			}
			if base == "node_modules" || base == "vendor" || base == "dist" || base == "build" || base == ".next" {
				return filepath.SkipDir
			}
			// Check .gleannignore for directories.
			if relPath != "." && ignoreMatcher.Match(relPath, true) {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(base, ".") {
			return nil
		}

		// Check .gleannignore for files.
		if ignoreMatcher.Match(relPath, false) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))

		// Check if a plugin can extract this document.
		hasPlugin := false
		if pluginManager != nil && pluginManager.FindDocumentExtractor(ext) != nil {
			hasPlugin = true
		}

		if !hasPlugin && binaryExts[ext] {
			return nil
		}
		if !hasPlugin && info.Size() > 1<<20 { // >1MB (plugins can bypass this if they want to handle big docs)
			return nil
		}

		files = append(files, fileEntry{path: path, info: info})
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	if len(files) == 0 {
		return nil, nil, nil
	}

	// Phase 2: parallel read + chunk.
	nWorkers := runtime.NumCPU()
	if nWorkers > 16 {
		nWorkers = 16
	}
	if nWorkers < 1 {
		nWorkers = 1
	}

	type result struct {
		items []gleann.Item
		err   error
	}

	jobCh := make(chan fileEntry, len(files))
	resCh := make(chan result, len(files))

	// Collected plugin results for deferred graph indexing.
	var pluginDocs []*PluginDoc
	var pluginDocsMu sync.Mutex

	for i := 0; i < nWorkers; i++ {
		go func() {
			// Each worker gets its own splitter instances (they are not thread-safe).
			splitter := chunking.NewSentenceSplitter(chunkSize, chunkOverlap)
			codeSplitter := chunking.NewCodeChunker(chunkSize, chunkOverlap)
			mdChunker := chunking.NewMarkdownChunker(chunkSize, chunkOverlap)

			for fe := range jobCh {
				ext := strings.ToLower(filepath.Ext(fe.path))
				var data []byte
				var err error

				// If a plugin handles this extension, use structured extraction.
				if pluginManager != nil {
					if plugin := pluginManager.FindDocumentExtractor(ext); plugin != nil {
						pResult, perr := pluginManager.ProcessStructured(plugin, fe.path)
						if perr != nil {
							fmt.Fprintf(os.Stderr, "Warning: plugin %s failed to extract %s: %v\n", plugin.Name, filepath.Base(fe.path), perr)
							resCh <- result{err: nil}
							continue
						}

						relPath, _ := filepath.Rel(dir, fe.path)

						// Convert plugin result → StructuredDocument → context-aware chunks.
						doc := pluginResultToDoc(pResult)
						mdChunks := mdChunker.ChunkDocument(doc)

						// Fallback: if structured extraction produced no sections but
						// raw markdown is available (e.g. markitdown backend), use
						// the markdown chunker's heading-based parser instead.
						if len(mdChunks) == 0 && pResult.Markdown != "" {
							mdChunks = mdChunker.ChunkMarkdown(pResult.Markdown, relPath)
						}

						var items []gleann.Item
						for _, ch := range mdChunks {
							ch.Metadata["source"] = relPath
							items = append(items, gleann.Item{
								Text:     ch.Text,
								Metadata: ch.Metadata,
							})
						}

						// Save plugin result for graph indexing (if --graph is active).
						pluginDocsMu.Lock()
						pluginDocs = append(pluginDocs, &PluginDoc{
							Result:     pResult,
							SourcePath: relPath,
						})
						pluginDocsMu.Unlock()

						resCh <- result{items: items}
						continue
					}
				}

				if data == nil {
					data, err = os.ReadFile(fe.path)
					if err != nil {
						resCh <- result{err: nil} // skip unreadable
						continue
					}
				}

				// Skip binary content (null bytes).
				check := data
				if len(check) > 512 {
					check = check[:512]
				}
				if bytes.ContainsRune(check, 0) {
					resCh <- result{}
					continue
				}

				text := string(data)
				if len(strings.TrimSpace(text)) == 0 {
					resCh <- result{}
					continue
				}

				relPath, _ := filepath.Rel(dir, fe.path)
				metadata := map[string]any{"source": relPath}

				if tracker != nil {
					h := sha256.Sum256(data)
					hash := hex.EncodeToString(h[:])
					if err := tracker.UpsertRecord(context.Background(), hash, fe.path, fe.info.ModTime().Unix(), fe.info.Size()); err == nil {
						metadata["hash"] = hash
					}
				}

				var rawChunks []chunking.Chunk

				// Markdown files get heading-aware chunking + graph structure.
				if chunking.IsMarkdownFile(fe.path) {
					mdChunks := mdChunker.ChunkMarkdown(text, relPath)
					if len(mdChunks) > 0 {
						var items []gleann.Item
						for _, ch := range mdChunks {
							items = append(items, gleann.Item{
								Text:     ch.Text,
								Metadata: ch.Metadata,
							})
						}

						// Also produce graph-ready PluginResult for KuzuDB.
						sections := chunking.ParseMarkdownHeadings(text)
						if len(sections) > 0 {
							wordCount := len(strings.Fields(text))
							pResult := markdownToPluginResult(sections, relPath, wordCount, text)
							pluginDocsMu.Lock()
							pluginDocs = append(pluginDocs, &PluginDoc{
								Result:     pResult,
								SourcePath: relPath,
							})
							pluginDocsMu.Unlock()
						}

						resCh <- result{items: items}
						continue
					}
					// No headings found — fall through to code/sentence chunking.
				}

				if chunking.IsCodeFile(fe.path) {
					rawChunks = codeSplitter.ChunkWithMetadata(text, metadata)
				} else {
					rawChunks = splitter.ChunkWithMetadata(text, metadata)
				}

				var chunks []gleann.Item
				for _, rc := range rawChunks {
					chunks = append(chunks, gleann.Item{
						Text:     rc.Text,
						Metadata: rc.Metadata,
					})
				}
				resCh <- result{items: chunks}
			}
		}()
	}

	// Send all files to workers.
	for _, f := range files {
		jobCh <- f
	}
	close(jobCh)

	// Collect results.
	var allItems []gleann.Item
	for range files {
		r := <-resCh
		allItems = append(allItems, r.items...)
	}

	return allItems, pluginDocs, nil
}

func cmdWatch(args []string) {
	if len(args) < 1 {
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

	embedder := embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(config.EmbeddingProvider),
		Model:       config.EmbeddingModel,
		BaseURL:     config.OllamaHost,
		BatchSize:   config.BatchSize,
		Concurrency: config.Concurrency,
	})

	// Wrap with embedding cache for rebuild efficiency.
	cachedEmbedder := embedding.NewCachedComputer(embedder, embedding.CacheOptions{})

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

	// Initial build.
	pluginDocs := buildIndex(name, docsDir, config, cachedEmbedder, tracker)

	if buildGraph {
		buildGraphIndex(name, docsDir, config.IndexDir, pluginDocs, nil)
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

			fmt.Printf("🔄 %d file(s) changed, rebuilding index %q...\n", len(files), name)
			pDocs := buildIndex(name, docsDir, config, cachedEmbedder, tracker)

			if buildGraph {
				buildGraphIndex(name, docsDir, config.IndexDir, pDocs, files)
			}

			// drain any queued up builds during sleep
			select {
			case <-buildRequested:
			default:
			}
		}
	}
}
