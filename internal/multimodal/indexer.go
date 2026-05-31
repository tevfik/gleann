// Package multimodal provides model-native multimodal processing for gleann.
package multimodal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// IndexableItem represents a multimodal file converted to text for vector indexing.
type IndexableItem struct {
	Source      string // Original file path.
	Text        string // LLM-generated description text.
	MediaType   MediaType
	Description string // Same as Text, kept for clarity.

	// Layer C / Bug #4 #7 #11 enrichments. All optional; nil/empty when
	// the vision plugin is not configured or did not return that field.
	OCRText       string         `json:"ocr_text,omitempty"`
	CLIPEmbedding []float32      `json:"clip_embedding,omitempty"`
	Entities      []VisionEntity `json:"entities,omitempty"`
	Metadata      *FileMetadata  `json:"metadata,omitempty"`
}

// multimodalWorkerCount returns the configured worker pool size for
// ProcessDirectory. Bug #2: defaults to 4, overridable via env so users on
// constrained hardware can dial it back without recompiling.
func multimodalWorkerCount() int {
	if v := os.Getenv("GLEANN_MULTIMODAL_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 64 {
			return n
		}
	}
	return 4
}

// ProcessDirectory scans a directory for multimodal files (images, audio, video)
// and generates text descriptions for each using the configured Ollama model.
// The returned items can be passed directly to LeannBuilder.Build().
//
// skipExts optionally lists extensions to skip (e.g., ".svg" which tree-sitter handles).
// progressFn is called after each file with (current, total, path).
//
// Bug #2 fix: files are processed by a worker pool (default 4 workers,
// override with GLEANN_MULTIMODAL_WORKERS) so a large directory does not
// pay the full Ollama latency sequentially. Ordering of the returned slice
// matches the discovered file ordering for reproducibility.
func (p *Processor) ProcessDirectory(dir string, skipExts []string, progressFn func(int, int, string)) ([]IndexableItem, error) {
	if p.Model == "" {
		return nil, fmt.Errorf("no multimodal model configured")
	}

	skipSet := make(map[string]bool, len(skipExts))
	for _, ext := range skipExts {
		skipSet[strings.ToLower(ext)] = true
	}

	// Collect multimodal files.
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if skipSet[ext] {
			return nil
		}
		if IsMultimodal(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}

	if len(files) == 0 {
		return nil, nil
	}

	workers := multimodalWorkerCount()
	if workers > len(files) {
		workers = len(files)
	}

	// Layer B/C: optional vision plugin URL is resolved once so each
	// worker does not re-probe. Empty means "VLM only, no enrichment".
	visionURL := VisionPluginURL()

	type job struct {
		idx  int
		path string
	}
	type out struct {
		idx  int
		item IndexableItem
		skip bool
	}

	jobs := make(chan job, len(files))
	results := make(chan out, len(files))
	var wg sync.WaitGroup

	// Progress callback is invoked serially to avoid interleaved output;
	// the worker pool publishes completions to a counter, the main
	// goroutine collects them.
	var doneCount int
	var doneMu sync.Mutex

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				result := p.ProcessFile(j.path)
				doneMu.Lock()
				doneCount++
				cur := doneCount
				if progressFn != nil {
					// Invoke under the lock to keep callbacks serial and
					// race-free: tests (and stdout) observe a single
					// monotonically increasing `cur` without overlap.
					progressFn(cur, len(files), j.path)
				}
				doneMu.Unlock()
				if result.Error != nil {
					fmt.Fprintf(os.Stderr, "warning: multimodal processing failed for %s: %v\n", j.path, result.Error)
					results <- out{idx: j.idx, skip: true}
					continue
				}
				if result.Description == "" {
					results <- out{idx: j.idx, skip: true}
					continue
				}
				relPath, _ := filepath.Rel(dir, j.path)
				if relPath == "" {
					relPath = filepath.Base(j.path)
				}
				text := fmt.Sprintf("[%s: %s]\n\n%s",
					mediaTypeName(result.MediaType), relPath, result.Description)

				item := IndexableItem{
					Source:      j.path,
					Text:        text,
					MediaType:   result.MediaType,
					Description: result.Description,
				}
				// Bug #9: cheap stdlib-only metadata enrichment.
				if md, err := ExtractFileMetadata(j.path); err == nil {
					item.Metadata = &md
				}
				// Layer B/C: vision plugin enrichment (CLIP/OCR/entities).
				if visionURL != "" && result.MediaType == MediaTypeImage {
					if vr, err := CallVisionPlugin(visionURL, j.path); err == nil && vr != nil {
						item.OCRText = vr.OCRText
						item.CLIPEmbedding = vr.CLIPEmbedding
						item.Entities = vr.Entities
						// Bug #11: append OCR text to the description so
						// downstream BM25 immediately benefits without a
						// dedicated index. Joint CLIP embedding is
						// surfaced through the dedicated field.
						if vr.OCRText != "" {
							item.Text += "\n\n[OCR]\n" + vr.OCRText
						}
					}
				}
				results <- out{
					idx:  j.idx,
					item: item,
				}
			}
		}()
	}

	for i, path := range files {
		jobs <- job{idx: i, path: path}
	}
	close(jobs)
	wg.Wait()
	close(results)

	// Collect and re-order to match the input file ordering.
	ordered := make([]*IndexableItem, len(files))
	for r := range results {
		if r.skip {
			continue
		}
		item := r.item
		ordered[r.idx] = &item
	}
	items := make([]IndexableItem, 0, len(files))
	for _, it := range ordered {
		if it != nil {
			items = append(items, *it)
		}
	}
	return items, nil
}

func mediaTypeName(mt MediaType) string {
	switch mt {
	case MediaTypeImage:
		return "Image"
	case MediaTypeAudio:
		return "Audio"
	case MediaTypeVideo:
		return "Video"
	default:
		return "File"
	}
}
