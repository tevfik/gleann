// Package multimodal provides model-native multimodal processing for gleann.
package multimodal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IndexableItem represents a multimodal file converted to text for vector indexing.
type IndexableItem struct {
	Source      string // Original file path.
	Text        string // LLM-generated description text.
	MediaType   MediaType
	Description string // Same as Text, kept for clarity.
}

// ProcessDirectory scans a directory for multimodal files (images, audio, video)
// and generates text descriptions for each using the configured Ollama model.
// The returned items can be passed directly to LeannBuilder.Build().
//
// skipExts optionally lists extensions to skip (e.g., ".svg" which tree-sitter handles).
// progressFn is called after each file with (current, total, path).
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

	var items []IndexableItem
	for i, path := range files {
		if progressFn != nil {
			progressFn(i+1, len(files), path)
		}

		result := p.ProcessFile(path)
		if result.Error != nil {
			// Log warning but continue with other files.
			fmt.Fprintf(os.Stderr, "warning: multimodal processing failed for %s: %v\n", path, result.Error)
			continue
		}
		if result.Description == "" {
			continue
		}

		// Build a rich text representation for embedding.
		relPath, _ := filepath.Rel(dir, path)
		if relPath == "" {
			relPath = filepath.Base(path)
		}

		text := fmt.Sprintf("[%s: %s]\n\n%s",
			mediaTypeName(result.MediaType), relPath, result.Description)

		items = append(items, IndexableItem{
			Source:      path,
			Text:        text,
			MediaType:   result.MediaType,
			Description: result.Description,
		})
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
