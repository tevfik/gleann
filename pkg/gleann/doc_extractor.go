// Package gleann provides doc_extractor.go — the bridge between markitdown CLI
// output and the PluginResult graph-ready format. This enables Layer 0 document
// extraction: Go calls markitdown → gets markdown → builds PluginResult with
// the same node/edge structure as the Python plugin.
package gleann

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// DocExtractor is the unified document extraction interface.
// It tries multiple layers in order:
//   - Layer 1: Plugin server (highest quality, e.g. Docling for PDF)
//   - Layer 0: markitdown CLI (Python CLI, intermediate quality)
//   - Layer -1: Go-native extraction (zero deps, always available)
type DocExtractor struct {
	native     *NativeExtractor
	markitdown *MarkItDownExtractor
	plugins    *PluginManager
}

// NewDocExtractor creates a new extractor with optional layers.
// The NativeExtractor is always created as fallback.
func NewDocExtractor(mid *MarkItDownExtractor, pm *PluginManager) *DocExtractor {
	return &DocExtractor{
		native:     NewNativeExtractor(),
		markitdown: mid,
		plugins:    pm,
	}
}

// Extract tries to extract document content from the given file path.
// Priority: Plugin server → markitdown CLI → Go-native.
// Returns a PluginResult with graph-ready nodes and edges.
func (de *DocExtractor) Extract(filePath string) (*PluginResult, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Layer 1: Plugin server (higher quality, e.g. Docling for PDF).
	if de.plugins != nil {
		if plugin := de.plugins.FindDocumentExtractor(ext); plugin != nil {
			result, err := de.plugins.ProcessStructured(plugin, filePath)
			if err == nil {
				return result, nil
			}
			// Plugin failed — fall through to CLI.
		}
	}

	// Layer 0: markitdown CLI (no Python server needed, but needs markitdown installed).
	if de.markitdown != nil && de.markitdown.CanHandle(ext) {
		result, err := de.ExtractWithCLI(filePath)
		if err == nil {
			return result, nil
		}
		// CLI failed — fall through to native.
	}

	// Layer -1: Go-native (zero external deps, always available).
	if de.native != nil && de.native.CanHandle(ext) {
		return de.ExtractNative(filePath)
	}

	return nil, fmt.Errorf("no extractor available for %s", ext)
}

// ExtractNative converts a file via pure-Go parsers and builds a PluginResult.
func (de *DocExtractor) ExtractNative(filePath string) (*PluginResult, error) {
	if de.native == nil {
		return nil, fmt.Errorf("native extractor not available")
	}

	markdown, err := de.native.Extract(filePath)
	if err != nil {
		return nil, fmt.Errorf("native extract: %w", err)
	}

	return MarkdownToPluginResult(markdown, filePath), nil
}

// ExtractWithCLI converts a file via markitdown CLI and builds a PluginResult
// using Go-native heading parsing. This mirrors what section_parser.py does
// on the Python side, producing identical node/edge structure.
func (de *DocExtractor) ExtractWithCLI(filePath string) (*PluginResult, error) {
	if de.markitdown == nil || !de.markitdown.Available() {
		return nil, fmt.Errorf("markitdown CLI not available")
	}

	markdown, err := de.markitdown.Extract(filePath)
	if err != nil {
		return nil, fmt.Errorf("markitdown extract: %w", err)
	}

	return MarkdownToPluginResult(markdown, filePath), nil
}

// MarkdownToPluginResult converts raw markdown text into a PluginResult
// with the same graph-ready node/edge structure as the Python section_parser.
//
// Node types:
//   - Document: {_type: "Document", path, title, format, word_count}
//   - Section:  {_type: "Section", id, heading, level, content, summary, order}
//
// Edge types:
//   - HAS_SECTION:    Document → top-level Section
//   - HAS_SUBSECTION: Section → child Section
func MarkdownToPluginResult(markdown, sourcePath string) *PluginResult {
	sections := parseHeadings(markdown)
	docID := "doc:" + sourcePath

	// Infer title from first H1 heading, or filename.
	title := filepath.Base(sourcePath)
	for _, s := range sections {
		if s.level == 1 {
			title = s.heading
			break
		}
	}

	// Word count.
	wordCount := len(strings.Fields(markdown))

	// Summary and Hash
	summaryStr := ExtractSummary(markdown)
	hashBytes := sha256.Sum256([]byte(markdown))
	hashStr := hex.EncodeToString(hashBytes[:])

	// Infer format from extension.
	ext := strings.TrimPrefix(filepath.Ext(sourcePath), ".")
	if ext == "" {
		ext = "md"
	}

	result := &PluginResult{}

	// Document node.
	result.Nodes = append(result.Nodes, PluginNode{
		Type: "Document",
		Data: map[string]any{
			"_type":      "Document",
			"rpath":      sourcePath,
			"vpath":      sourcePath, // Optional: Can be overridden by the indexer
			"title":      title,
			"format":     ext,
			"hash":       hashStr,
			"summary":    summaryStr,
			"word_count": wordCount,
		},
	})

	// Section nodes + edges.
	for _, sec := range sections {
		sectionID := docID + ":" + sec.id

		result.Nodes = append(result.Nodes, PluginNode{
			Type: "Section",
			Data: map[string]any{
				"_type":   "Section",
				"id":      sectionID,
				"heading": sec.heading,
				"level":   sec.level,
				"content": sec.content,
				"summary": firstParagraph(sec.content, 200),
				"order":   sec.order,
			},
		})

		// Edge: parent → this section.
		if sec.parentID == "" {
			// Top-level → Document has it.
			result.Edges = append(result.Edges, PluginEdge{
				Type: "HAS_SECTION",
				From: docID,
				To:   sectionID,
			})
		} else {
			// Subsection → parent section has it.
			result.Edges = append(result.Edges, PluginEdge{
				Type: "HAS_SUBSECTION",
				From: docID + ":" + sec.parentID,
				To:   sectionID,
			})
		}
	}

	return result
}

// --- internal heading parser (mirrors ParseMarkdownHeadings but builds local IDs) ---

type parsedSection struct {
	id       string
	heading  string
	level    int
	content  string
	parentID string
	order    int
}

// parseHeadings extracts sections from markdown, assigning IDs like "s0", "s0.0", "s0.0.1".
func parseHeadings(markdown string) []parsedSection {
	lines := strings.Split(markdown, "\n")

	type hdr struct {
		lineIdx int
		level   int
		title   string
	}

	var hdrs []hdr
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		hashes := 0
		for _, ch := range trimmed {
			if ch == '#' {
				hashes++
			} else {
				break
			}
		}
		if hashes < 1 || hashes > 6 {
			continue
		}
		rest := strings.TrimSpace(trimmed[hashes:])
		if rest == "" {
			continue
		}
		hdrs = append(hdrs, hdr{lineIdx: i, level: hashes, title: rest})
	}

	if len(hdrs) == 0 {
		return nil
	}

	sections := make([]parsedSection, 0, len(hdrs))
	parentStack := make([]struct {
		id    string
		level int
	}, 0)
	childCounters := map[string]int{}

	for idx, h := range hdrs {
		contentStart := h.lineIdx + 1
		contentEnd := len(lines)
		if idx+1 < len(hdrs) {
			contentEnd = hdrs[idx+1].lineIdx
		}
		content := strings.TrimSpace(strings.Join(lines[contentStart:contentEnd], "\n"))

		// Find parent.
		for len(parentStack) > 0 && parentStack[len(parentStack)-1].level >= h.level {
			parentStack = parentStack[:len(parentStack)-1]
		}
		parentID := ""
		if len(parentStack) > 0 {
			parentID = parentStack[len(parentStack)-1].id
		}

		order := childCounters[parentID]
		childCounters[parentID] = order + 1

		var sectionID string
		if parentID != "" {
			sectionID = parentID + "." + smallItoa(order)
		} else {
			sectionID = "s" + smallItoa(order)
		}

		sections = append(sections, parsedSection{
			id:       sectionID,
			heading:  h.title,
			level:    h.level,
			content:  content,
			parentID: parentID,
			order:    order,
		})

		parentStack = append(parentStack, struct {
			id    string
			level int
		}{sectionID, h.level})
	}

	return sections
}

// firstParagraph returns the first non-empty paragraph, truncated to maxLen.
func firstParagraph(text string, maxLen int) string {
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" || strings.HasPrefix(para, "#") {
			continue
		}
		if len(para) > maxLen {
			cut := para[:maxLen]
			if idx := strings.LastIndex(cut, " "); idx > 0 {
				cut = cut[:idx]
			}
			return cut + "..."
		}
		return para
	}
	return ""
}

// smallItoa converts int to string without strconv.
func smallItoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
