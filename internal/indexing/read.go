package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/walker"
)

// ReadOptions configures ReadDocuments.
type ReadOptions struct {
	ChunkSize      int
	ChunkOverlap   int
	SignaturesOnly bool
	// Tracker, when non-nil, receives file records for every processed file.
	Tracker *vault.Tracker
	// Multimodal enables vision/audio extraction for media files and PDFs.
	Multimodal        *multimodal.Processor
	Mode              Mode
	NoPlugins         bool
	IncludeSubmodules bool
	// Stdout / Stderr receive informational / warning lines (nil = discard).
	Stdout io.Writer
	Stderr io.Writer
}

// readOptionsFor derives ReadOptions from cfg chunking settings and opts.
func readOptionsFor(cfg gleann.Config, opts *Options, tracker *vault.Tracker, mm *multimodal.Processor) ReadOptions {
	return ReadOptions{
		ChunkSize:         cfg.ChunkConfig.ChunkSize,
		ChunkOverlap:      cfg.ChunkConfig.ChunkOverlap,
		SignaturesOnly:    cfg.ChunkConfig.SignaturesOnly,
		Tracker:           tracker,
		Multimodal:        mm,
		Mode:              opts.mode(),
		NoPlugins:         opts.NoPlugins,
		IncludeSubmodules: opts.IncludeSubmodules,
	}
}

func makeFileRecord(path string, info os.FileInfo, data []byte) *vault.FileRecord {
	if info == nil {
		return nil
	}
	var hash string
	if len(data) > 0 {
		h := sha256.Sum256(data)
		hash = hex.EncodeToString(h[:])
	} else {
		h, err := vault.ComputeHash(path)
		if err == nil {
			hash = h
		}
	}
	return &vault.FileRecord{
		Hash:         hash,
		Path:         path,
		LastModified: info.ModTime().Unix(),
		Size:         info.Size(),
	}
}

// openPluginManager loads extraction plugins unless disabled for the mode.
// The returned close func is always non-nil.
func openPluginManager(noPlugins bool, mode Mode) (*gleann.PluginManager, func()) {
	if noPlugins || mode == ModeCode {
		return nil, func() {}
	}
	pm, _ := gleann.NewPluginManager()
	if pm == nil {
		return nil, func() {}
	}
	pm.ResolveMultimodalPluginEnv()
	return pm, func() { pm.Close() }
}

func workerCount() int {
	n := runtime.NumCPU()
	if n > 16 {
		n = 16
	}
	if n < 1 {
		n = 1
	}
	return n
}

// ReadDocuments walks dir and reads + chunks every eligible file in
// parallel. It returns the chunks, graph-ready plugin documents, and an
// error if the walk fails or ctx is cancelled.
func ReadDocuments(ctx context.Context, dir string, ro ReadOptions) ([]gleann.Item, []*PluginDoc, error) {
	return readDocuments(ctx, dir, ro, newPrinter(ro.Stdout, ro.Stderr))
}

func readDocuments(ctx context.Context, dir string, ro ReadOptions, p *printer) ([]gleann.Item, []*PluginDoc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	chunkSize, chunkOverlap, signaturesOnly := ro.ChunkSize, ro.ChunkOverlap, ro.SignaturesOnly
	tracker, mmProcessor := ro.Tracker, ro.Multimodal

	// Load plugins once and manage their lifecycles only when plugins are allowed
	pluginManager, closePlugins := openPluginManager(ro.NoPlugins, ro.Mode)
	defer closePlugins()

	// Native extractor: pure-Go fallback for PDF, DOCX, XLSX, PPTX, CSV, HTML.
	nativeExtractor := gleann.NewNativeExtractor()

	// Phase 1: collect eligible file paths (serial walk is fast — just syscalls).
	files, walkErr := CollectEligibleFiles(ctx, dir, CollectOptions{
		Mode:              ro.Mode,
		IncludeSubmodules: ro.IncludeSubmodules,
		PluginManager:     pluginManager,
		NativeExtractor:   nativeExtractor,
		Multimodal:        mmProcessor,
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	if len(files) == 0 {
		return nil, nil, nil
	}

	// Phase 2: parallel read + chunk.
	nWorkers := workerCount()

	type result struct {
		items  []gleann.Item
		record *vault.FileRecord
	}

	jobCh := make(chan FileEntry, len(files))
	resCh := make(chan result, len(files))

	// Collected plugin results for deferred graph indexing.
	var pluginDocs []*PluginDoc
	var pluginDocsMu sync.Mutex
	addPluginDoc := func(pd *PluginDoc) {
		pluginDocsMu.Lock()
		pluginDocs = append(pluginDocs, pd)
		pluginDocsMu.Unlock()
	}

	for i := 0; i < nWorkers; i++ {
		go func() {
			// Each worker gets its own splitter instances (they are not thread-safe).
			splitter := chunking.NewSentenceSplitter(chunkSize, chunkOverlap)
			astCfg := chunking.DefaultASTChunkerConfig()
			astCfg.MaxChunkSize = chunkSize
			astCfg.ChunkOverlap = chunkOverlap
			astCfg.SignaturesOnly = signaturesOnly
			codeSplitter := chunking.NewASTChunker(astCfg)
			mdChunker := chunking.NewMarkdownChunker(chunkSize, chunkOverlap)

			for fe := range jobCh {
				// Cancelled: drain remaining jobs without doing work.
				if ctx.Err() != nil {
					resCh <- result{}
					continue
				}
				ext := strings.ToLower(filepath.Ext(fe.Path))
				var data []byte
				var err error

				// If a plugin handles this extension, use structured extraction.
				pluginSucceeded := false
				if pluginManager != nil && !((ext == ".pdf" || multimodal.IsMultimodal(fe.Path)) && mmProcessor != nil) {
					if plugin := pluginManager.FindDocumentExtractor(ext); plugin != nil {
						pResult, perr := pluginManager.ProcessStructured(plugin, fe.Path)
						if perr != nil {
							p.Warnf("Warning: plugin %s failed to extract %s: %v (fallback to native)\n", plugin.Name, filepath.Base(fe.Path), perr)
							// Fall through to native extractor
						} else {
							pluginSucceeded = true

							relPath, _ := filepath.Rel(dir, fe.Path)

							// Convert plugin result → StructuredDocument → context-aware chunks.
							doc := PluginResultToDoc(pResult)
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
							addPluginDoc(&PluginDoc{Result: pResult, SourcePath: relPath})

							resCh <- result{items: items, record: makeFileRecord(fe.Path, fe.Info, data)}
							continue
						}
					}
				}

				// Multimodal PDF Vision RAG extraction
				if !pluginSucceeded && ext == ".pdf" && mmProcessor != nil {
					pdfCfg := multimodal.DefaultPDFConfig()
					analysis, perr := mmProcessor.AnalyzePDF(fe.Path, pdfCfg)
					if perr == nil && analysis != nil {
						relPath, _ := filepath.Rel(dir, fe.Path)
						items, fullMd := visionPDFItems(analysis, relPath, mdChunker)

						pResult := gleann.MarkdownToPluginResult(fullMd, relPath)
						if pResult != nil && (len(pResult.Nodes) > 0 || pResult.Markdown != "") {
							addPluginDoc(&PluginDoc{Result: pResult, SourcePath: relPath})
						}

						resCh <- result{items: items, record: makeFileRecord(fe.Path, fe.Info, data)}
						continue
					}
					// Fallback to native if AnalyzePDF fails
					p.Warnf("Warning: vision extraction failed for %s, falling back to native text: %v\n", filepath.Base(fe.Path), perr)
				}

				// Try native extractor for binary document formats (PDF, DOCX, etc.).
				// (Only if plugin didn't succeed)
				if !pluginSucceeded && nativeExtractor.CanHandle(ext) {
					md, nerr := nativeExtractor.Extract(fe.Path)
					if nerr != nil {
						p.Warnf("Warning: native extraction failed for %s: %v\n", filepath.Base(fe.Path), nerr)
						resCh <- result{}
						continue
					}
					if md = strings.TrimSpace(md); md == "" {
						resCh <- result{}
						continue
					}

					relPath, _ := filepath.Rel(dir, fe.Path)
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
									"kind":         "docs",
									"is_test":      false,
									"is_vendor":    false,
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
						addPluginDoc(&PluginDoc{Result: pResult, SourcePath: relPath})
					}

					resCh <- result{items: items, record: makeFileRecord(fe.Path, fe.Info, data)}
					continue
				}

				// Try multimodal processing for media files (images, audio, video).
				if mmProcessor != nil && mmProcessor.CanProcess(fe.Path) {
					mr := mmProcessor.ProcessFile(fe.Path)
					if mr.Error != nil {
						p.Warnf("Warning: multimodal processing failed for %s: %v\n", filepath.Base(fe.Path), mr.Error)
						resCh <- result{record: makeFileRecord(fe.Path, fe.Info, data)}
						continue
					}
					if desc := strings.TrimSpace(mr.Description); desc != "" {
						relPath, _ := filepath.Rel(dir, fe.Path)
						items := []gleann.Item{{
							Text: desc,
							Metadata: map[string]any{
								"source":     relPath,
								"extractor":  "multimodal",
								"media_type": mediaTypeName(mr.MediaType),
							},
						}}
						p.Printf("🎨 Multimodal: %s → %d chars\n", filepath.Base(fe.Path), len(desc))
						resCh <- result{items: items, record: makeFileRecord(fe.Path, fe.Info, data)}
					} else {
						resCh <- result{record: makeFileRecord(fe.Path, fe.Info, data)}
					}
					continue
				}

				if data == nil {
					data, err = os.ReadFile(fe.Path)
					if err != nil {
						resCh <- result{record: makeFileRecord(fe.Path, fe.Info, nil)} // skip unreadable
						continue
					}
				}

				// Skip binary content (null bytes).
				if looksBinary(data) {
					resCh <- result{record: makeFileRecord(fe.Path, fe.Info, data)}
					continue
				}

				text := string(data)
				if len(strings.TrimSpace(text)) == 0 {
					resCh <- result{record: makeFileRecord(fe.Path, fe.Info, data)}
					continue
				}

				relPath, rerr := filepath.Rel(dir, fe.Path)
				if rerr != nil || relPath == "" {
					relPath = filepath.Base(fe.Path)
				}
				relPath = filepath.ToSlash(relPath)
				metadata := map[string]any{
					"source":    relPath,
					"file":      relPath,
					"file_path": relPath,
				}

				if tracker != nil {
					h := sha256.Sum256(data)
					metadata["hash"] = hex.EncodeToString(h[:])
				}

				// Markdown files get heading-aware chunking + graph structure.
				if chunking.IsMarkdownFile(fe.Path) {
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
							addPluginDoc(&PluginDoc{
								Result:     MarkdownSectionsToPluginResult(sections, relPath, wordCount, text),
								SourcePath: relPath,
							})
						}

						resCh <- result{items: items, record: makeFileRecord(fe.Path, fe.Info, data)}
						continue
					}
					// No headings found — fall through to code/sentence chunking.
				}

				isCode := chunking.IsCodeFile(fe.Path)
				if !isCode {
					s := walker.SniffBytes(fe.Path, data)
					if s.Category == walker.CategoryCode || s.Category == walker.CategoryConfig {
						isCode = true
					}
				}

				var rawChunks []chunking.Chunk
				if isCode {
					rawChunks = codeSplitter.ChunkWithMetadata(text, metadata)
				} else {
					metadata["kind"] = "docs"
					metadata["is_test"] = false
					metadata["is_vendor"] = false
					rawChunks = splitter.ChunkWithMetadata(text, metadata)
				}

				var chunks []gleann.Item
				for _, rc := range rawChunks {
					chunks = append(chunks, gleann.Item{
						Text:     rc.Text,
						Metadata: rc.Metadata,
					})
				}
				resCh <- result{items: chunks, record: makeFileRecord(fe.Path, fe.Info, data)}
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
	var records []vault.FileRecord
	for range files {
		r := <-resCh
		allItems = append(allItems, r.items...)
		if r.record != nil && r.record.Hash != "" {
			records = append(records, *r.record)
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	if tracker != nil && len(records) > 0 {
		_ = tracker.BatchUpsertRecords(ctx, records)
	}

	return allItems, pluginDocs, nil
}

// looksBinary reports whether the first 512 bytes contain a NUL byte.
func looksBinary(data []byte) bool {
	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	return bytes.ContainsRune(check, 0)
}

func mediaTypeName(t multimodal.MediaType) string {
	switch t {
	case multimodal.MediaTypeAudio:
		return "audio"
	case multimodal.MediaTypeVideo:
		return "video"
	}
	return "image"
}

// visionPDFItems converts a vision PDF analysis into per-page chunks and the
// concatenated markdown used for the document graph.
func visionPDFItems(analysis *multimodal.PDFAnalysis, relPath string, mdChunker *chunking.MarkdownChunker) ([]gleann.Item, string) {
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
	return items, fullMd.String()
}

// readDocumentsForFiles reads and chunks only the specified files. It is
// used for incremental updates (watch / sync) where only changed files need
// processing — much faster than re-reading the entire directory. It also
// returns tracker records for the processed files.
func readDocumentsForFiles(ctx context.Context, dir string, filePaths []string, ro ReadOptions, p *printer) ([]gleann.Item, []*PluginDoc, []vault.FileRecord, error) {
	if len(filePaths) == 0 {
		return nil, nil, nil, nil
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	mode, mmProcessor := ro.Mode, ro.Multimodal

	pluginManager, closePlugins := openPluginManager(ro.NoPlugins, mode)
	defer closePlugins()
	nativeExtractor := gleann.NewNativeExtractor()
	ignoreMatcher := walker.NewMatcher(dir, ro.IncludeSubmodules, nil)

	splitter := chunking.NewSentenceSplitter(ro.ChunkSize, ro.ChunkOverlap)
	astCfg := chunking.DefaultASTChunkerConfig()
	astCfg.MaxChunkSize = ro.ChunkSize
	astCfg.ChunkOverlap = ro.ChunkOverlap
	astCfg.SignaturesOnly = ro.SignaturesOnly
	codeSplitter := chunking.NewASTChunker(astCfg)
	mdChunker := chunking.NewMarkdownChunker(ro.ChunkSize, ro.ChunkOverlap)

	var allItems []gleann.Item
	var pluginDocs []*PluginDoc
	var records []vault.FileRecord
	addRecord := func(path string, info os.FileInfo, data []byte) {
		if rec := makeFileRecord(path, info, data); rec != nil && rec.Hash != "" {
			records = append(records, *rec)
		}
	}

	for _, filePath := range filePaths {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		info, err := os.Stat(filePath)
		if err != nil || info.IsDir() {
			continue
		}

		absFilePath := filePath
		if afp, err := filepath.Abs(filePath); err == nil {
			absFilePath = afp
		}
		relPath, rerr := filepath.Rel(dir, absFilePath)
		if rerr != nil || relPath == "" {
			relPath = filepath.Base(filePath)
		}
		relPath = filepath.ToSlash(relPath)
		if ignoreMatcher.ShouldIgnore(relPath, false) {
			continue
		}

		ext := strings.ToLower(filepath.Ext(filePath))

		// Apply mode filtering
		switch mode {
		case ModeCode:
			if IsOfficeDocExtension(ext) || binaryExts[ext] {
				addRecord(filePath, info, nil)
				continue
			}
			if !IsCodeExtension(ext) && !IsDocumentationExtension(ext) {
				addRecord(filePath, info, nil)
				continue
			}
		case ModeDocs:
			if !IsOfficeDocExtension(ext) && !IsDocumentationExtension(ext) {
				addRecord(filePath, info, nil)
				continue
			}
		}

		// Plugin extraction.
		if pluginManager != nil && !(ext == ".pdf" && mmProcessor != nil) {
			if plugin := pluginManager.FindDocumentExtractor(ext); plugin != nil {
				pResult, perr := pluginManager.ProcessStructured(plugin, filePath)
				if perr == nil {
					doc := PluginResultToDoc(pResult)
					mdChunks := mdChunker.ChunkDocument(doc)
					if len(mdChunks) == 0 && pResult.Markdown != "" {
						mdChunks = mdChunker.ChunkMarkdown(pResult.Markdown, relPath)
					}
					for _, ch := range mdChunks {
						ch.Metadata["source"] = relPath
						allItems = append(allItems, gleann.Item{Text: ch.Text, Metadata: ch.Metadata})
					}
					pluginDocs = append(pluginDocs, &PluginDoc{Result: pResult, SourcePath: relPath})
					addRecord(filePath, info, nil)
					continue
				}
			}
		}

		// Multimodal PDF Vision RAG extraction
		if ext == ".pdf" && mmProcessor != nil {
			pdfCfg := multimodal.DefaultPDFConfig()
			analysis, perr := mmProcessor.AnalyzePDF(filePath, pdfCfg)
			if perr == nil && analysis != nil {
				items, fullMd := visionPDFItems(analysis, relPath, mdChunker)
				allItems = append(allItems, items...)

				pResult := gleann.MarkdownToPluginResult(fullMd, relPath)
				if pResult != nil && (len(pResult.Nodes) > 0 || pResult.Markdown != "") {
					pluginDocs = append(pluginDocs, &PluginDoc{Result: pResult, SourcePath: relPath})
				}
				addRecord(filePath, info, nil)
				continue
			}
			p.Warnf("Warning: incremental vision extraction failed for %s, falling back to native text: %v\n", filepath.Base(filePath), perr)
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
			addRecord(filePath, info, nil)
			continue
		}

		// Multimodal processing (images, audio, video).
		if mmProcessor != nil && mmProcessor.CanProcess(filePath) {
			mr := mmProcessor.ProcessFile(filePath)
			if mr.Error == nil {
				if desc := strings.TrimSpace(mr.Description); desc != "" {
					allItems = append(allItems, gleann.Item{
						Text:     desc,
						Metadata: map[string]any{"source": relPath, "extractor": "multimodal", "media_type": mediaTypeName(mr.MediaType)},
					})
				}
			}
			addRecord(filePath, info, nil)
			continue
		}

		// Text file: read, detect binary, chunk.
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		if looksBinary(data) {
			addRecord(filePath, info, data)
			continue
		}

		text := string(data)
		if len(strings.TrimSpace(text)) == 0 {
			addRecord(filePath, info, data)
			continue
		}

		metadata := map[string]any{
			"source":    relPath,
			"file":      relPath,
			"file_path": relPath,
		}

		rec := makeFileRecord(filePath, info, data)
		if rec != nil && rec.Hash != "" {
			metadata["hash"] = rec.Hash
			records = append(records, *rec)
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
					pluginDocs = append(pluginDocs, &PluginDoc{
						Result:     MarkdownSectionsToPluginResult(sections, relPath, wordCount, text),
						SourcePath: relPath,
					})
				}
				continue
			}
		}

		// Code or sentence chunking.
		var rawChunks []chunking.Chunk
		if chunking.IsCodeFile(filePath) {
			rawChunks = codeSplitter.ChunkWithMetadata(text, metadata)
		} else {
			metadata["kind"] = "docs"
			metadata["is_test"] = false
			metadata["is_vendor"] = false
			rawChunks = splitter.ChunkWithMetadata(text, metadata)
		}
		for _, rc := range rawChunks {
			allItems = append(allItems, gleann.Item{Text: rc.Text, Metadata: rc.Metadata})
		}
	}

	return allItems, pluginDocs, records, nil
}
