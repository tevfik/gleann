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
	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/gleannignore"
)

// IndexMode represents the filtering mode for indexing.
type IndexMode string

const (
	IndexModeAll  IndexMode = "all"
	IndexModeCode IndexMode = "code"
	IndexModeDocs IndexMode = "docs"
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

func printBuildUsage() {
	fmt.Println(`Usage: gleann index build <name> --docs <dir> [options]

Build a vector index (and optional AST code graph) from documents or source code.

Arguments:
  <name>                  Name of the index to create

Options:
  --docs <dir>            Source directory containing files to index (required)
  --graph                 Build AST-based code graph using tree-sitter & Kùzu
  --mode <code|docs|all>  Index mode:
                            code - fast source code & AST graph only
                            docs - documents only (pdf, docx, etc.)
                            all  - index everything (default)
  --tag <tag>             Assign governance tag(s) (comma-separated or multiple)
  --desc <description>    Human-readable description for semantic MCP routing
  --mcp                   Expose index to MCP tools (default: true)
  --no-plugins            Disable external document extraction plugins
  --multimodal-model <m>  Model for media files (images, audio, video)
  --no-report             Skip automatic GRAPH_REPORT.md generation
  --no-agents             Skip automatic AGENTS.md generation
  -h, --help              Show this help message

Examples:
  gleann index build core --docs ./src --graph --mode code
  gleann index build docs --docs ./documentation --tag docs,work`)
}

func cmdBuild(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		printBuildUsage()
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			return
		}
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

	mode, noPlugins := parseIndexMode(args)
	if mode == IndexModeCode {
		fmt.Printf("⚡ Index mode: CODE (fast code & AST graph, skipping office doc plugins)\n")
	} else if mode == IndexModeDocs {
		fmt.Printf("📄 Index mode: DOCUMENTS (office docs only, skipping raw code)\n")
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	// Multimodal model for indexing media files (images, audio, video).
	mmModel := getFlag(args, "--multimodal-model")
	if mmModel == "" {
		mmModel = config.MultimodalModel
	}
	mmProcessor := initMultimodalProcessor(config.OllamaHost, mmModel)

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
	items, pluginDocs, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker, mmProcessor, mode, noPlugins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "error: no documents found")
		os.Exit(1)
	}

	fmt.Printf("📝 Found %d text chunks\n", len(items))
	fmt.Printf("🔧 Building index %q (backend: %s) with model %s...\n", name, config.Backend, config.EmbeddingModel)

	start := time.Now()
	ctx := context.Background()
	if err := builder.Build(ctx, name, items); err != nil {
		fmt.Fprintf(os.Stderr, "error building index: %v\n", err)
		os.Exit(1)
	}

	tagsFlag := getFlag(args, "--tags")
	if tagsFlag == "" {
		tagsFlag = getFlag(args, "--tag")
	}
	descFlag := getFlag(args, "--desc")
	mcpFlag := getFlag(args, "--mcp")

	_ = gleann.UpdateIndexMeta(config.IndexDir, name, func(meta *gleann.IndexMeta) {
		meta.SourceDir = docsDir
		if tagsFlag != "" {
			for _, t := range strings.Split(tagsFlag, ",") {
				t = strings.TrimSpace(t)
				if t != "" && !meta.HasTag(t) {
					meta.Tags = append(meta.Tags, strings.TrimPrefix(t, "@"))
				}
			}
		}
		if descFlag != "" {
			meta.Description = descFlag
		}
		if mcpFlag != "" {
			exp := (mcpFlag == "true" || mcpFlag == "1" || mcpFlag == "yes")
			meta.MCPExposed = &exp
		}
	})


	elapsed := time.Since(start)
	fmt.Printf("✅ Vector Index %q built: %d passages in %s\n", name, len(items), elapsed.Round(time.Millisecond))

	// Report embedding cache stats.
	if hits, total := cachedEmbedder.Stats(); total > 0 {
		fmt.Printf("💾 Embedding cache: %d/%d hits (%.0f%%)\n", hits, total, cachedEmbedder.HitRate())
	}

	if buildGraph {
		buildGraphIndex(name, docsDir, config.IndexDir, pluginDocs, nil)
		if !hasFlag(args, "--no-report") {
			graphReportPath := filepath.Join(docsDir, "GRAPH_REPORT.md")
			if err := generateGraphReportFile(name, config.IndexDir, docsDir, graphReportPath); err == nil {
				fmt.Printf("📊 GRAPH_REPORT.md automatically generated in %s\n", graphReportPath)
			}
		}
	}

	if !hasFlag(args, "--no-agents") {
		agentsPath := filepath.Join(docsDir, "AGENTS.md")
		content := getAgentsMDContent(name)
		if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err == nil {
			fmt.Printf("🤖 AGENTS.md automatically generated/updated in %s\n", agentsPath)
		}
	}
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

func buildIndex(name, docsDir string, config gleann.Config, embedder gleann.EmbeddingComputer, tracker *vault.Tracker, mode IndexMode, noPlugins bool) []*PluginDoc {
	mmProcessor := initMultimodalProcessor(config.OllamaHost, config.MultimodalModel)
	items, pluginDocs, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker, mmProcessor, mode, noPlugins)
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
	_ = gleann.UpdateIndexMeta(config.IndexDir, name, func(meta *gleann.IndexMeta) {
		meta.SourceDir = docsDir
	})
	fmt.Printf("✅ Rebuilt %q: %d passages in %s\n", name, len(items), time.Since(start).Round(time.Millisecond))
	return pluginDocs
}

func readDocuments(dir string, chunkSize, chunkOverlap int, tracker *vault.Tracker, mmProcessor *multimodal.Processor, mode IndexMode, noPlugins bool) ([]gleann.Item, []*PluginDoc, error) {
	// Load plugins once and manage their lifecycles only when plugins are allowed
	var pluginManager *gleann.PluginManager
	if !noPlugins && mode != IndexModeCode {
		pluginManager, _ = gleann.NewPluginManager()
		if pluginManager != nil {
			defer pluginManager.Close()
			pluginManager.ResolveMultimodalPluginEnv()
		}
	}

	// Native extractor: pure-Go fallback for PDF, DOCX, XLSX, PPTX, CSV, HTML.
	nativeExtractor := gleann.NewNativeExtractor()

	// Phase 1: collect eligible file paths (serial walk is fast — just syscalls).
	files, walkErr := collectEligibleFiles(dir, pluginManager, nativeExtractor, mmProcessor, mode)
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
				pluginSucceeded := false
				if pluginManager != nil && !(ext == ".pdf" && mmProcessor != nil) {
					if plugin := pluginManager.FindDocumentExtractor(ext); plugin != nil {
						pResult, perr := pluginManager.ProcessStructured(plugin, fe.path)
						if perr != nil {
							fmt.Fprintf(os.Stderr, "Warning: plugin %s failed to extract %s: %v (fallback to native)\n", plugin.Name, filepath.Base(fe.path), perr)
							// Fall through to native extractor
						} else {
							pluginSucceeded = true

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
				}

				// Multimodal PDF Vision RAG extraction
				if !pluginSucceeded && ext == ".pdf" && mmProcessor != nil {
					pdfCfg := multimodal.DefaultPDFConfig()
					analysis, perr := mmProcessor.AnalyzePDF(fe.path, pdfCfg)
					if perr == nil && analysis != nil {
						relPath, _ := filepath.Rel(dir, fe.path)
						var items []gleann.Item
						var fullMd strings.Builder

						for _, page := range analysis.Pages {
							var pageMd strings.Builder
							fmt.Fprintf(&pageMd, "--- Page %d ---\n", page.PageNum)
							if page.Description != "" {
								fmt.Fprintln(&pageMd, page.Description)
							}
							if page.MarkerText != "" {
								fmt.Fprintln(&pageMd, page.MarkerText)
							}
							if page.Tables != nil {
								for _, t := range page.Tables.Tables {
									fmt.Fprintln(&pageMd, t.Markdown)
								}
							}
							fullMd.WriteString(pageMd.String() + "\n")

							// Chunk this page specifically
							mdChunks := mdChunker.ChunkMarkdown(pageMd.String(), relPath)
							if len(mdChunks) == 0 {
								items = append(items, gleann.Item{
									Text:     pageMd.String(),
									Metadata: map[string]any{"source": relPath, "extractor": "vision_rag", "page": page.PageNum, "has_image": true},
								})
							} else {
								for _, ch := range mdChunks {
									ch.Metadata["source"] = relPath
									ch.Metadata["extractor"] = "vision_rag"
									ch.Metadata["page"] = page.PageNum
									ch.Metadata["has_image"] = true
									items = append(items, gleann.Item{
										Text:     ch.Text,
										Metadata: ch.Metadata,
									})
								}
							}
						}

						pResult := gleann.MarkdownToPluginResult(fullMd.String(), relPath)
						if pResult != nil && (len(pResult.Nodes) > 0 || pResult.Markdown != "") {
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
					// Fallback to native if AnalyzePDF fails
					fmt.Fprintf(os.Stderr, "Warning: vision extraction failed for %s, falling back to native text: %v\n", filepath.Base(fe.path), perr)
				}

				// Try native extractor for binary document formats (PDF, DOCX, etc.).
				// (Only if plugin didn't succeed)
				if !pluginSucceeded && nativeExtractor.CanHandle(ext) {
					md, nerr := nativeExtractor.Extract(fe.path)
					if nerr != nil {
						fmt.Fprintf(os.Stderr, "Warning: native extraction failed for %s: %v\n", filepath.Base(fe.path), nerr)
						resCh <- result{}
						continue
					}
					if md = strings.TrimSpace(md); md == "" {
						resCh <- result{}
						continue
					}

					relPath, _ := filepath.Rel(dir, fe.path)
					mdChunks := mdChunker.ChunkMarkdown(md, relPath)

					var items []gleann.Item
					if len(mdChunks) == 0 {
						// Split plain text with SentenceSplitter instead of creating giant single chunks
						textChunks := splitter.Chunk(md)
						if len(textChunks) == 0 {
							textChunks = []string{md}
						}
						for idx, tc := range textChunks {
							items = append(items, gleann.Item{
								Text: tc,
								Metadata: map[string]any{
									"source":       relPath,
									"extractor":    "native",
									"chunk_index":  idx,
									"total_chunks": len(textChunks),
								},
							})
						}
					} else {
						for _, ch := range mdChunks {
							ch.Metadata["source"] = relPath
							ch.Metadata["extractor"] = "native"
							items = append(items, gleann.Item{
								Text:     ch.Text,
								Metadata: ch.Metadata,
							})
						}
					}

					// Generate graph-ready PluginResult from extracted markdown
					// so native-extracted documents also feed the knowledge graph.
					pResult := gleann.MarkdownToPluginResult(md, relPath)
					if pResult != nil && (len(pResult.Nodes) > 0 || pResult.Markdown != "") {
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

				// Try multimodal processing for media files (images, audio, video).
				if mmProcessor != nil && mmProcessor.CanProcess(fe.path) {
					mr := mmProcessor.ProcessFile(fe.path)
					if mr.Error != nil {
						fmt.Fprintf(os.Stderr, "Warning: multimodal processing failed for %s: %v\n", filepath.Base(fe.path), mr.Error)
						resCh <- result{}
						continue
					}
					if desc := strings.TrimSpace(mr.Description); desc != "" {
						relPath, _ := filepath.Rel(dir, fe.path)
						mediaType := "image"
						switch mr.MediaType {
						case multimodal.MediaTypeAudio:
							mediaType = "audio"
						case multimodal.MediaTypeVideo:
							mediaType = "video"
						}
						items := []gleann.Item{{
							Text: desc,
							Metadata: map[string]any{
								"source":     relPath,
								"extractor":  "multimodal",
								"media_type": mediaType,
							},
						}}
						fmt.Printf("🎨 Multimodal: %s → %d chars\n", filepath.Base(fe.path), len(desc))
						resCh <- result{items: items}
					} else {
						resCh <- result{}
					}
					continue
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

// fileEntry represents a file path and its info for indexing.
type fileEntry struct {
	path string
	info os.FileInfo
}

// binaryExts lists file extensions that should be skipped unless a plugin or
// native extractor can handle them.
var binaryExts = map[string]bool{
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

// collectEligibleFiles walks dir and returns files eligible for indexing,
// respecting .gleannignore, hidden dirs, and binary extensions.
func collectEligibleFiles(dir string, pluginManager *gleann.PluginManager, nativeExtractor *gleann.NativeExtractor, mmProcessor *multimodal.Processor, mode IndexMode) ([]fileEntry, error) {
	ignoreMatcher := gleannignore.Load(dir)

	var files []fileEntry
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		relPath, _ := filepath.Rel(dir, path)

		if info.IsDir() {
			if strings.HasPrefix(base, ".") && path != dir {
				return filepath.SkipDir
			}
			if base == "node_modules" || base == "vendor" || base == "dist" || base == "build" || base == ".next" || base == ".venv" || base == "__pycache__" {
				return filepath.SkipDir
			}
			if relPath != "." && ignoreMatcher.Match(relPath, true) {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(base, ".") {
			return nil
		}
		if ignoreMatcher.Match(relPath, false) {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))

		switch mode {
		case IndexModeCode:
			// Code mode: source code, configs, and documentation text.
			// Office docs, binary files, and massive data dumps (>1MB) are skipped.
			if isOfficeDocExtension(ext) || binaryExts[ext] || info.Size() > 1<<20 {
				return nil
			}
			if !isCodeExtension(ext) && !isDocumentationExtension(ext) {
				return nil
			}
		case IndexModeDocs:
			// Docs mode: office docs and documentation text. Pure code files are skipped.
			if (!isOfficeDocExtension(ext) && !isDocumentationExtension(ext)) || info.Size() > 10<<20 {
				return nil
			}
		case IndexModeAll, "":
			hasPlugin := pluginManager != nil && pluginManager.FindDocumentExtractor(ext) != nil
			hasNative := nativeExtractor.CanHandle(ext)
			hasMultimodal := mmProcessor != nil && mmProcessor.CanProcess(path)

			if !hasPlugin && !hasNative && !hasMultimodal && binaryExts[ext] {
				return nil
			}
			if !hasPlugin && !hasNative && !hasMultimodal && info.Size() > 1<<20 {
				return nil
			}
		}

		files = append(files, fileEntry{path: path, info: info})
		return nil
	})
	return files, err
}

// readDocumentsForFiles reads and chunks only the specified files.
// This is used for incremental indexing in watch mode where only changed files
// need processing — much faster than re-reading the entire directory.
func readDocumentsForFiles(dir string, filePaths []string, chunkSize, chunkOverlap int, tracker *vault.Tracker, mmProcessor *multimodal.Processor, mode IndexMode, noPlugins bool) ([]gleann.Item, []*PluginDoc, error) {
	if len(filePaths) == 0 {
		return nil, nil, nil
	}

	var pluginManager *gleann.PluginManager
	if !noPlugins && mode != IndexModeCode {
		pluginManager, _ = gleann.NewPluginManager()
		if pluginManager != nil {
			defer pluginManager.Close()
			pluginManager.ResolveMultimodalPluginEnv()
		}
	}
	nativeExtractor := gleann.NewNativeExtractor()
	ignoreMatcher := gleannignore.Load(dir)

	splitter := chunking.NewSentenceSplitter(chunkSize, chunkOverlap)
	codeSplitter := chunking.NewCodeChunker(chunkSize, chunkOverlap)
	mdChunker := chunking.NewMarkdownChunker(chunkSize, chunkOverlap)

	var allItems []gleann.Item
	var pluginDocs []*PluginDoc

	for _, filePath := range filePaths {
		info, err := os.Stat(filePath)
		if err != nil || info.IsDir() {
			continue
		}

		base := filepath.Base(filePath)
		relPath, _ := filepath.Rel(dir, filePath)

		if strings.HasPrefix(base, ".") {
			continue
		}
		if ignoreMatcher.Match(relPath, false) {
			continue
		}

		ext := strings.ToLower(filepath.Ext(filePath))

		// Apply mode filtering
		switch mode {
		case IndexModeCode:
			if isOfficeDocExtension(ext) || binaryExts[ext] {
				continue
			}
			if !isCodeExtension(ext) && !isDocumentationExtension(ext) {
				continue
			}
		case IndexModeDocs:
			if !isOfficeDocExtension(ext) && !isDocumentationExtension(ext) {
				continue
			}
		}

		// Plugin extraction.
		if pluginManager != nil && !(ext == ".pdf" && mmProcessor != nil) {
			if plugin := pluginManager.FindDocumentExtractor(ext); plugin != nil {
				pResult, perr := pluginManager.ProcessStructured(plugin, filePath)
				if perr == nil {
					doc := pluginResultToDoc(pResult)
					mdChunks := mdChunker.ChunkDocument(doc)
					if len(mdChunks) == 0 && pResult.Markdown != "" {
						mdChunks = mdChunker.ChunkMarkdown(pResult.Markdown, relPath)
					}
					for _, ch := range mdChunks {
						ch.Metadata["source"] = relPath
						allItems = append(allItems, gleann.Item{Text: ch.Text, Metadata: ch.Metadata})
					}
					pluginDocs = append(pluginDocs, &PluginDoc{Result: pResult, SourcePath: relPath})
					continue
				}
			}
		}

		// Multimodal PDF Vision RAG extraction
		if ext == ".pdf" && mmProcessor != nil {
			pdfCfg := multimodal.DefaultPDFConfig()
			analysis, perr := mmProcessor.AnalyzePDF(filePath, pdfCfg)
			if perr == nil && analysis != nil {
				var fullMd strings.Builder

				for _, page := range analysis.Pages {
					var pageMd strings.Builder
					fmt.Fprintf(&pageMd, "--- Page %d ---\n", page.PageNum)
					if page.Description != "" {
						fmt.Fprintln(&pageMd, page.Description)
					}
					if page.MarkerText != "" {
						fmt.Fprintln(&pageMd, page.MarkerText)
					}
					if page.Tables != nil {
						for _, t := range page.Tables.Tables {
							fmt.Fprintln(&pageMd, t.Markdown)
						}
					}
					fullMd.WriteString(pageMd.String() + "\n")

					// Chunk this page specifically
					mdChunks := mdChunker.ChunkMarkdown(pageMd.String(), relPath)
					if len(mdChunks) == 0 {
						allItems = append(allItems, gleann.Item{
							Text:     pageMd.String(),
							Metadata: map[string]any{"source": relPath, "extractor": "vision_rag", "page": page.PageNum, "has_image": true},
						})
					} else {
						for _, ch := range mdChunks {
							ch.Metadata["source"] = relPath
							ch.Metadata["extractor"] = "vision_rag"
							ch.Metadata["page"] = page.PageNum
							ch.Metadata["has_image"] = true
							allItems = append(allItems, gleann.Item{
								Text:     ch.Text,
								Metadata: ch.Metadata,
							})
						}
					}
				}

				pResult := gleann.MarkdownToPluginResult(fullMd.String(), relPath)
				if pResult != nil && (len(pResult.Nodes) > 0 || pResult.Markdown != "") {
					pluginDocs = append(pluginDocs, &PluginDoc{
						Result:     pResult,
						SourcePath: relPath,
					})
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: incremental vision extraction failed for %s, falling back to native text: %v\n", filepath.Base(filePath), perr)
		}

		// Native extraction (PDF, DOCX, etc.).
		if nativeExtractor.CanHandle(ext) {
			md, nerr := nativeExtractor.Extract(filePath)
			if nerr == nil {
				md = strings.TrimSpace(md)
				if md != "" {
					mdChunks := mdChunker.ChunkMarkdown(md, relPath)
					if len(mdChunks) == 0 {
						textChunks := splitter.Chunk(md)
						if len(textChunks) == 0 {
							textChunks = []string{md}
						}
						for idx, tc := range textChunks {
							allItems = append(allItems, gleann.Item{
								Text: tc,
								Metadata: map[string]any{
									"source":       relPath,
									"extractor":    "native",
									"chunk_index":  idx,
									"total_chunks": len(textChunks),
								},
							})
						}
					} else {
						for _, ch := range mdChunks {
							ch.Metadata["source"] = relPath
							ch.Metadata["extractor"] = "native"
							allItems = append(allItems, gleann.Item{Text: ch.Text, Metadata: ch.Metadata})
						}
					}
					pResult := gleann.MarkdownToPluginResult(md, relPath)
					if pResult != nil && (len(pResult.Nodes) > 0 || pResult.Markdown != "") {
						pluginDocs = append(pluginDocs, &PluginDoc{Result: pResult, SourcePath: relPath})
					}
				}
			}
			continue
		}

		// Multimodal processing (images, audio, video).
		if mmProcessor != nil && mmProcessor.CanProcess(filePath) {
			mr := mmProcessor.ProcessFile(filePath)
			if mr.Error == nil {
				if desc := strings.TrimSpace(mr.Description); desc != "" {
					mediaType := "image"
					switch mr.MediaType {
					case multimodal.MediaTypeAudio:
						mediaType = "audio"
					case multimodal.MediaTypeVideo:
						mediaType = "video"
					}
					allItems = append(allItems, gleann.Item{
						Text:     desc,
						Metadata: map[string]any{"source": relPath, "extractor": "multimodal", "media_type": mediaType},
					})
				}
			}
			continue
		}

		// Text file: read, detect binary, chunk.
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		check := data
		if len(check) > 512 {
			check = check[:512]
		}
		if bytes.ContainsRune(check, 0) {
			continue
		}

		text := string(data)
		if len(strings.TrimSpace(text)) == 0 {
			continue
		}

		metadata := map[string]any{"source": relPath}

		if tracker != nil {
			h := sha256.Sum256(data)
			hash := hex.EncodeToString(h[:])
			if err := tracker.UpsertRecord(context.Background(), hash, filePath, info.ModTime().Unix(), info.Size()); err == nil {
				metadata["hash"] = hash
			}
		}

		// Markdown files get heading-aware chunking.
		if chunking.IsMarkdownFile(filePath) {
			mdChunks := mdChunker.ChunkMarkdown(text, relPath)
			if len(mdChunks) > 0 {
				for _, ch := range mdChunks {
					allItems = append(allItems, gleann.Item{Text: ch.Text, Metadata: ch.Metadata})
				}
				sections := chunking.ParseMarkdownHeadings(text)
				if len(sections) > 0 {
					wordCount := len(strings.Fields(text))
					pResult := markdownToPluginResult(sections, relPath, wordCount, text)
					pluginDocs = append(pluginDocs, &PluginDoc{Result: pResult, SourcePath: relPath})
				}
				continue
			}
		}

		// Code or sentence chunking.
		var rawChunks []chunking.Chunk
		if chunking.IsCodeFile(filePath) {
			rawChunks = codeSplitter.ChunkWithMetadata(text, metadata)
		} else {
			rawChunks = splitter.ChunkWithMetadata(text, metadata)
		}
		for _, rc := range rawChunks {
			allItems = append(allItems, gleann.Item{Text: rc.Text, Metadata: rc.Metadata})
		}
	}

	return allItems, pluginDocs, nil
}

// incrementalBuildIndex attempts to incrementally update the index for changed files.
// It removes old passages for changed/deleted sources and adds new chunks.
// Returns plugin docs and true on success, or nil and false if a full rebuild is needed.
func incrementalBuildIndex(name, docsDir string, changedFiles []string, config gleann.Config, embedder gleann.EmbeddingComputer, tracker *vault.Tracker, mode IndexMode, noPlugins bool) ([]*PluginDoc, bool) {
	// Classify changes: existing files need re-chunking, missing files are deletions.
	var existingFiles []string
	var removeSources []string

	for _, f := range changedFiles {
		relPath, _ := filepath.Rel(docsDir, f)
		if _, err := os.Stat(f); err != nil {
			// File was deleted.
			removeSources = append(removeSources, relPath)
		} else {
			// File exists (new or modified) — remove old passages, then re-add.
			existingFiles = append(existingFiles, f)
			removeSources = append(removeSources, relPath)
		}
	}

	// Read and chunk only the changed files.
	mmProcessor := initMultimodalProcessor(config.OllamaHost, config.MultimodalModel)
	items, pluginDocs, err := readDocumentsForFiles(docsDir, existingFiles,
		config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker, mmProcessor, mode, noPlugins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "incremental: error reading changed files: %v\n", err)
		return nil, false
	}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "incremental: error creating builder: %v\n", err)
		return nil, false
	}

	ctx := context.Background()
	if err := builder.UpdateIndex(ctx, name, items, removeSources); err != nil {
		// UpdateIndex may fail if backend doesn't support removal (e.g., FAISS).
		fmt.Fprintf(os.Stderr, "incremental update failed (%v), falling back to full rebuild\n", err)
		return nil, false
	}

	return pluginDocs, true
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

	mode, noPlugins := parseIndexMode(args)

	// Initial build.
	pluginDocs := buildIndex(name, docsDir, config, cachedEmbedder, tracker, mode, noPlugins)

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

			fmt.Printf("🔄 %d file(s) changed, updating index %q...\n", len(files), name)
			start := time.Now()
			pDocs, ok := incrementalBuildIndex(name, docsDir, files, config, cachedEmbedder, tracker, mode, noPlugins)
			if !ok {
				// Fall back to full rebuild.
				pDocs = buildIndex(name, docsDir, config, cachedEmbedder, tracker, mode, noPlugins)
			} else {
				fmt.Printf("⚡ Incremental update complete in %s\n", time.Since(start).Round(time.Millisecond))
			}

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

// initMultimodalProcessor creates a multimodal processor only when explicitly
// requested via --multimodal-model flag or GLEANN_MULTIMODAL_MODEL env var.
// Auto-detect is disabled by default to avoid unintentional HTTP calls for
// every media file in large projects (e.g., test wav files from SciPy).
// Set model to "none" or "off" to explicitly disable even when auto-detect finds one.
func initMultimodalProcessor(ollamaHost, flagModel string) *multimodal.Processor {
	model := flagModel
	if model == "" {
		model = os.Getenv("GLEANN_MULTIMODAL_MODEL")
	}
	// Explicitly disabled — no auto-detect fallback
	if strings.EqualFold(model, "none") || strings.EqualFold(model, "off") || strings.EqualFold(model, "disable") {
		return nil
	}
	// No auto-detect: only proceed if model is explicitly set
	if model == "" {
		return nil
	}
	p := multimodal.NewProcessor(ollamaHost, model)
	caps := multimodal.DetectCapabilities(ollamaHost, model)
	features := []string{}
	if caps.Vision {
		features = append(features, "vision")
	}
	if caps.Audio {
		features = append(features, "audio")
	}
	if len(features) > 0 {
		fmt.Printf("🎨 Multimodal indexing: %s (%s)\n", model, strings.Join(features, "+"))
	}
	return p
}

// cmdSync performs an on-demand incremental synchronization of an index.
// It checks which files have been modified, added, or deleted in the workspace,
// updating both the vector index and the AST code graph.
//
// Usage:
//   gleann index sync <name> [--docs <dir>] [--files <file1,file2>] [--graph]
func cmdSync(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Fprintln(os.Stderr, "usage: gleann index sync <name> [--docs <dir>] [--files <file1,file2>] [--graph] [--mode code|docs|all] [--no-plugins]")
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			return
		}
		os.Exit(1)
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		fmt.Fprintf(os.Stderr, "error: index name %q looks like a flag\nusage: gleann index sync <name> [--docs <dir>]\n", name)
		os.Exit(1)
	}

	mode, noPlugins := parseIndexMode(args)
	if mode == IndexModeCode {
		fmt.Printf("⚡ Sync mode: CODE (fast code & AST graph, skipping office doc plugins)\n")
	} else if mode == IndexModeDocs {
		fmt.Printf("📄 Sync mode: DOCUMENTS (office docs only, skipping raw code)\n")
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	meta, err := gleann.GetIndexMeta(config.IndexDir, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: index %q not found in %s: %v\n", name, config.IndexDir, err)
		os.Exit(1)
	}

	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		docsDir = meta.SourceDir
	}
	if docsDir == "" {
		fmt.Fprintf(os.Stderr, "error: --docs flag required (index %q does not have a recorded source_dir)\n", name)
		os.Exit(1)
	}

	absDocsDir, err := filepath.Abs(docsDir)
	if err != nil {
		absDocsDir = docsDir
	}

	// Determine if graph indexing is requested or exists.
	buildGraph := hasFlag(args, "--graph")
	if !buildGraph {
		graphDir := filepath.Join(config.IndexDir, name+"_graph")
		if fi, err := os.Stat(graphDir); err == nil && fi.IsDir() {
			buildGraph = true
		}
	}

	start := time.Now()
	fmt.Printf("🔄 Synchronizing index %q with %s...\n", name, absDocsDir)

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
	cachedEmbedder := embedding.NewCachedComputer(embedder, embedding.CacheOptions{})

	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not initialize vault tracker: %v\n", err)
	} else {
		defer tracker.Close()
	}

	filesFlag := getFlag(args, "--files")
	var changedFiles []string
	var deletedFiles []string

	if filesFlag != "" {
		for _, f := range strings.Split(filesFlag, ",") {
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
		var pluginManager *gleann.PluginManager
		if !noPlugins && mode != IndexModeCode {
			pluginManager, _ = gleann.NewPluginManager()
			if pluginManager != nil {
				defer pluginManager.Close()
				pluginManager.ResolveMultimodalPluginEnv()
			}
		}
		nativeExtractor := gleann.NewNativeExtractor()
		mmModel := getFlag(args, "--multimodal-model")
		if mmModel == "" {
			mmModel = config.MultimodalModel
		}
		mmProcessor := initMultimodalProcessor(config.OllamaHost, mmModel)

		eligibleEntries, walkErr := collectEligibleFiles(absDocsDir, pluginManager, nativeExtractor, mmProcessor, mode)
		if walkErr != nil {
			fmt.Fprintf(os.Stderr, "error scanning workspace: %v\n", walkErr)
			os.Exit(1)
		}

		eligiblePaths := make([]string, len(eligibleEntries))
		for i, e := range eligibleEntries {
			eligiblePaths[i] = e.path
		}

		if tracker != nil {
			ctx := context.Background()
			cFiles, dFiles, dErr := tracker.DetectChangedFiles(ctx, absDocsDir, eligiblePaths)
			if dErr != nil {
				fmt.Fprintf(os.Stderr, "warning: change detection failed (%v), checking all files\n", dErr)
				changedFiles = eligiblePaths
			} else {
				changedFiles = cFiles
				deletedFiles = dFiles
			}
		} else {
			changedFiles = eligiblePaths
		}
	}

	if len(deletedFiles) > 0 && mode != IndexModeAll {
		var realDeleted []string
		for _, df := range deletedFiles {
			if _, err := os.Stat(df); err == nil {
				// File still exists on disk; check if it was only excluded by the active mode filter.
				ext := strings.ToLower(filepath.Ext(df))
				if mode == IndexModeDocs && (isCodeExtension(ext) || !isOfficeDocExtension(ext)) {
					continue // Preserve code files when syncing docs
				}
				if mode == IndexModeCode && (isOfficeDocExtension(ext) || !isCodeExtension(ext)) {
					continue // Preserve office documents when syncing code
				}
			}
			realDeleted = append(realDeleted, df)
		}
		deletedFiles = realDeleted
	}

	allChanged := append(changedFiles, deletedFiles...)
	if len(allChanged) == 0 {
		fmt.Printf("⚡ Index %q is already up to date. No changes detected in %s.\n", name, time.Since(start).Round(time.Millisecond))
		return
	}

	fmt.Printf("📦 Detected %d changed/new file(s) and %d deleted file(s)\n", len(changedFiles), len(deletedFiles))

	if tracker != nil && len(deletedFiles) > 0 {
		ctx := context.Background()
		for _, df := range deletedFiles {
			_ = tracker.RemovePath(ctx, df)
		}
	}

	pDocs, ok := incrementalBuildIndex(name, absDocsDir, allChanged, config, cachedEmbedder, tracker, mode, noPlugins)
	if !ok {
		fmt.Println("⚠️  Incremental vector update not supported or failed, running full rebuild...")
		pDocs = buildIndex(name, absDocsDir, config, cachedEmbedder, tracker, mode, noPlugins)
	} else {
		fmt.Printf("⚡ Vector index updated in %s\n", time.Since(start).Round(time.Millisecond))
	}

	if buildGraph {
		buildGraphIndex(name, absDocsDir, config.IndexDir, pDocs, allChanged)
	}

	_ = gleann.UpdateIndexMeta(config.IndexDir, name, func(m *gleann.IndexMeta) {
		m.SourceDir = absDocsDir
	})

	fmt.Printf("✅ Sync complete for %q in %s (%d files processed)\n", name, time.Since(start).Round(time.Millisecond), len(allChanged))
}

