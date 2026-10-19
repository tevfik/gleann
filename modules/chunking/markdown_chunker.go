package chunking

import (
	"path/filepath"
	"strings"
)

// MarkdownSection represents a heading-delimited section from structured JSON.
type MarkdownSection struct {
	ID       string   `json:"id"`
	Heading  string   `json:"heading"`
	Level    int      `json:"level"`
	Content  string   `json:"content"`
	Summary  string   `json:"summary"`
	ParentID string   `json:"parent_id,omitempty"`
	Order    int      `json:"order"`
	Children []string `json:"children,omitempty"`
}

// DocumentMeta holds top-level document metadata from the plugin.
type DocumentMeta struct {
	Title     string `json:"title"`
	Format    string `json:"format"`
	PageCount *int   `json:"page_count,omitempty"`
	WordCount int    `json:"word_count"`
	Summary   string `json:"summary"`
}

// StructuredDocument is the full response from the plugin /convert endpoint.
type StructuredDocument struct {
	Document DocumentMeta      `json:"document"`
	Sections []MarkdownSection `json:"sections"`
	Markdown string            `json:"markdown"`
}

// MarkdownChunk is a chunk with hierarchical context preserved.
type MarkdownChunk struct {
	// Text is the chunk text WITH the context header prepended.
	Text string
	// RawText is the chunk text WITHOUT context header.
	RawText string
	// SectionID is the parent section ID (e.g. "s0.1").
	SectionID string
	// SectionPath is the full heading breadcrumb, e.g. ["Introduction", "Background"].
	SectionPath []string
	// HeadingLevel of the parent section (1-6).
	HeadingLevel int
	// DocTitle from the document metadata.
	DocTitle string
	// Metadata carries all indexing metadata for this chunk.
	Metadata map[string]any
}

// MarkdownChunker splits structured documents into context-aware chunks
// that preserve heading hierarchy as a breadcrumb context header.
type MarkdownChunker struct {
	ChunkSize    int
	ChunkOverlap int
	splitter     *SentenceSplitter
}

// NewMarkdownChunker creates a new markdown-aware chunker.
func NewMarkdownChunker(chunkSize, chunkOverlap int) *MarkdownChunker {
	splitter := NewSentenceSplitter(chunkSize, chunkOverlap)
	// Use the splitter's normalised sizes so prose and tables share one budget
	// (a zero/negative chunkSize would otherwise split tables one row per chunk
	// while prose used the 512-word default).
	return &MarkdownChunker{
		ChunkSize:    splitter.ChunkSize,
		ChunkOverlap: splitter.ChunkOverlap,
		splitter:     splitter,
	}
}

// ChunkDocument splits a structured document into context-aware chunks.
func (mc *MarkdownChunker) ChunkDocument(doc *StructuredDocument) []MarkdownChunk {
	if doc == nil || len(doc.Sections) == 0 {
		return nil
	}

	// Build section lookup for path resolution.
	sectionMap := make(map[string]*MarkdownSection, len(doc.Sections))
	for i := range doc.Sections {
		sectionMap[doc.Sections[i].ID] = &doc.Sections[i]
	}

	var chunks []MarkdownChunk

	for _, section := range doc.Sections {
		if strings.TrimSpace(section.Content) == "" {
			continue
		}

		// Build heading path: ["Introduction", "Background", "Implementation"]
		path := mc.buildSectionPath(section.ID, sectionMap)
		contextHeader := buildContextHeader(path)

		// Split section content into sub-chunks using table-aware splitter.
		textPieces := mc.chunkSectionContent(section.Content)

		for i, piece := range textPieces {
			text := piece.text
			// Prepend context header to each chunk for richer embedding.
			enrichedText := text
			if contextHeader != "" {
				enrichedText = contextHeader + "\n\n" + text
			}

			metadata := map[string]any{
				"doc_title":     doc.Document.Title,
				"doc_format":    doc.Document.Format,
				"section_id":    section.ID,
				"section_title": section.Heading,
				"section_path":  strings.Join(path, " > "),
				"heading_level": section.Level,
				"chunk_index":   i,
				"total_chunks":  len(textPieces),
				"kind":          "docs",
				"is_test":       false,
				"is_vendor":     false,
			}
			if section.Summary != "" {
				metadata["section_summary"] = section.Summary
			}
			if piece.isTable {
				metadata["is_table"] = true
				for k, v := range piece.tableMeta {
					metadata[k] = v
				}
			}

			chunks = append(chunks, MarkdownChunk{
				Text:         enrichedText,
				RawText:      text,
				SectionID:    section.ID,
				SectionPath:  path,
				HeadingLevel: section.Level,
				DocTitle:     doc.Document.Title,
				Metadata:     metadata,
			})
		}
	}

	return chunks
}

// ChunkMarkdown parses raw markdown by headings when structured JSON is
// unavailable (e.g. plain .md files on disk, not from plugin). This is the
// fallback path.
func (mc *MarkdownChunker) ChunkMarkdown(markdown string, source string) []MarkdownChunk {
	sections := ParseMarkdownHeadings(markdown)
	if len(sections) == 0 {
		// No headings — fall back to table-aware splitter.
		textPieces := mc.chunkSectionContent(markdown)
		chunks := make([]MarkdownChunk, 0, len(textPieces))
		for i, piece := range textPieces {
			meta := map[string]any{
				"source":       source,
				"chunk_index":  i,
				"total_chunks": len(textPieces),
				"kind":         "docs",
				"ext":          filepath.Ext(source),
				"is_test":      false,
				"is_vendor":    false,
			}
			if piece.isTable {
				meta["is_table"] = true
				for k, v := range piece.tableMeta {
					meta[k] = v
				}
			}
			chunks = append(chunks, MarkdownChunk{
				Text:     piece.text,
				RawText:  piece.text,
				Metadata: meta,
			})
		}
		return chunks
	}

	// Wrap parsed sections into a StructuredDocument and reuse ChunkDocument.
	doc := &StructuredDocument{
		Document: DocumentMeta{Title: inferTitle(sections), Format: "md"},
		Sections: sections,
		Markdown: markdown,
	}
	chunks := mc.ChunkDocument(doc)
	// Inject source and ext into all metadata.
	ext := filepath.Ext(source)
	for i := range chunks {
		chunks[i].Metadata["source"] = source
		chunks[i].Metadata["ext"] = ext
	}
	return chunks
}

// buildSectionPath walks up the parent chain to build the full heading breadcrumb.
func (mc *MarkdownChunker) buildSectionPath(
	sectionID string, lookup map[string]*MarkdownSection,
) []string {
	var path []string
	current := sectionID

	for current != "" {
		sec, ok := lookup[current]
		if !ok {
			break
		}
		path = append([]string{sec.Heading}, path...)
		current = sec.ParentID
	}

	return path
}

// buildContextHeader formats a section path as a breadcrumb header.
// Example: "# Introduction > ## Background > ### Implementation"
func buildContextHeader(path []string) string {
	if len(path) == 0 {
		return ""
	}

	parts := make([]string, len(path))
	for i, heading := range path {
		level := i + 1
		if level > 6 {
			level = 6
		}
		parts[i] = strings.Repeat("#", level) + " " + heading
	}
	return strings.Join(parts, " > ")
}

// IsMarkdownFile checks if a filename is a markdown file.
func IsMarkdownFile(filename string) bool {
	ext := strings.ToLower(filename)
	if idx := strings.LastIndex(ext, "."); idx >= 0 {
		ext = ext[idx:]
	}
	switch ext {
	case ".md", ".markdown", ".mdx", ".mdown", ".mkd", ".mkdn":
		return true
	}
	return false
}

// ParseMarkdownHeadings extracts heading structure from raw markdown text.
// This is the Go-side parser for when structured JSON is not available
// (e.g. indexing a local .md file directly or parsing markitdown output).
//
// Handles edge cases:
//   - Skips headings inside fenced code blocks (``` or ~~~)
//   - Skips YAML front matter (--- delimited at file start)
//   - Skips headings inside HTML comments (<!-- ... -->)
//   - Strips heading anchor tags ({#id}) and trailing hashes
//   - Supports setext-style headings (Title\n==== or Title\n----)
func ParseMarkdownHeadings(markdown string) []MarkdownSection {
	lines := strings.Split(markdown, "\n")

	type heading struct {
		lineIdx int
		level   int
		title   string
	}

	var headings []heading
	inFence := false
	inHTMLComment := false
	inFrontMatter := false
	frontMatterChecked := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// --- YAML front matter (must be at very start of file) ---
		if !frontMatterChecked {
			frontMatterChecked = true
			if trimmed == "---" {
				inFrontMatter = true
				continue
			}
		}
		if inFrontMatter {
			if trimmed == "---" || trimmed == "..." {
				inFrontMatter = false
			}
			continue
		}

		// --- Fenced code blocks (``` or ~~~) ---
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		// --- HTML comments (single-line and multi-line) ---
		if strings.Contains(trimmed, "<!--") {
			if !strings.Contains(trimmed, "-->") {
				inHTMLComment = true
			}
			continue
		}
		if inHTMLComment {
			if strings.Contains(trimmed, "-->") {
				inHTMLComment = false
			}
			continue
		}

		// --- Setext-style headings: Title\n==== or Title\n---- ---
		if i > 0 && len(trimmed) >= 2 {
			if allSameChar(trimmed, '=') {
				prevTitle := strings.TrimSpace(lines[i-1])
				if prevTitle != "" && !strings.HasPrefix(prevTitle, "#") {
					headings = append(headings, heading{lineIdx: i - 1, level: 1, title: cleanHeading(prevTitle)})
				}
				continue
			}
			if allSameChar(trimmed, '-') {
				// Setext H2 requires a non-blank preceding line.
				// A standalone "---" with blank line before is a horizontal rule, not a heading.
				prevTitle := strings.TrimSpace(lines[i-1])
				if prevTitle != "" && prevTitle != "---" && !strings.HasPrefix(prevTitle, "#") {
					headings = append(headings, heading{lineIdx: i - 1, level: 2, title: cleanHeading(prevTitle)})
				}
				continue
			}
		}

		// --- ATX-style headings: # Title ---
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Count leading '#' characters.
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
		// Must be followed by a space or end of string (commonmark spec).
		rest := trimmed[hashes:]
		if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		title := cleanHeading(strings.TrimSpace(rest))
		if title == "" {
			continue
		}
		headings = append(headings, heading{lineIdx: i, level: hashes, title: title})
	}

	if len(headings) == 0 {
		return nil
	}

	sections := make([]MarkdownSection, 0, len(headings))
	parentStack := make([]struct {
		id    string
		level int
	}, 0)
	childCounters := map[string]int{} // parentID → count ("" for root)

	for idx, h := range headings {
		contentStart := h.lineIdx + 1
		// For setext headings, content starts after the underline.
		if contentStart < len(lines) {
			nextLine := strings.TrimSpace(lines[contentStart])
			if allSameChar(nextLine, '=') || allSameChar(nextLine, '-') {
				contentStart++
			}
		}
		contentEnd := len(lines)
		if idx+1 < len(headings) {
			contentEnd = headings[idx+1].lineIdx
		}
		if contentStart > contentEnd {
			contentStart = contentEnd
		}
		var content string
		if contentStart <= contentEnd && contentStart <= len(lines) {
			content = strings.TrimSpace(strings.Join(lines[contentStart:contentEnd], "\n"))
		}

		// Find parent by walking the stack.
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
			sectionID = parentID + "." + itoa(order)
		} else {
			sectionID = "s" + itoa(order)
		}

		sec := MarkdownSection{
			ID:       sectionID,
			Heading:  h.title,
			Level:    h.level,
			Content:  content,
			ParentID: parentID,
			Order:    order,
			Summary:  extractSummary(content),
		}
		sections = append(sections, sec)
		parentStack = append(parentStack, struct {
			id    string
			level int
		}{sectionID, h.level})

		// Update parent's children list.
		if parentID != "" {
			for i := range sections {
				if sections[i].ID == parentID {
					sections[i].Children = append(sections[i].Children, sectionID)
					break
				}
			}
		}
	}

	return sections
}

// cleanHeading strips trailing hashes, anchor tags {#id}, and extra whitespace.
func cleanHeading(s string) string {
	// Strip trailing hashes: "## Title ##" → "Title"
	s = strings.TrimRight(s, "# ")
	// Strip anchor tags: "Title {#my-id}" → "Title"
	if idx := strings.LastIndex(s, " {#"); idx >= 0 && strings.HasSuffix(s, "}") {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// allSameChar returns true if the string has length ≥ 2 and every rune is ch.
func allSameChar(s string, ch rune) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if r != ch {
			return false
		}
	}
	return true
}

// inferTitle picks the first H1 heading, or the first heading.
func inferTitle(sections []MarkdownSection) string {
	for _, s := range sections {
		if s.Level == 1 {
			return s.Heading
		}
	}
	if len(sections) > 0 {
		return sections[0].Heading
	}
	return "Untitled"
}

// extractSummary takes the first non-empty, non-heading paragraph.
func extractSummary(text string) string {
	const maxChars = 200
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" || strings.HasPrefix(para, "#") {
			continue
		}
		if len(para) > maxChars {
			cut := para[:maxChars]
			if idx := strings.LastIndex(cut, " "); idx > 0 {
				cut = cut[:idx]
			}
			return cut + "..."
		}
		return para
	}
	return ""
}

// itoa converts a small integer to string without importing strconv.
func itoa(n int) string {
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

// ── Table-Aware Chunker Helpers ──────────────────────────────────────────

type contentPiece struct {
	text      string
	isTable   bool
	tableMeta map[string]any
}

type tableBlock struct {
	header    string
	separator string
	rows      []string
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	hasDash := false
	for _, r := range trimmed {
		if r == '-' {
			hasDash = true
		} else if r != '|' && r != ':' && r != ' ' && r != '\t' {
			return false
		}
	}
	return hasDash
}

// chunkSectionContent splits section content while respecting markdown tables.
// Small tables are kept atomic (never cut in half).
// Large tables that exceed ChunkSize are split across row boundaries,
// with the header and separator rows repeated in every sub-chunk so column semantics are preserved.
func (mc *MarkdownChunker) chunkSectionContent(content string) []contentPiece {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	var pieces []contentPiece
	var textLines []string

	flushText := func() {
		if len(textLines) == 0 {
			return
		}
		txt := strings.TrimSpace(strings.Join(textLines, "\n"))
		textLines = nil
		if txt == "" {
			return
		}
		chunks := mc.splitter.Chunk(txt)
		for _, c := range chunks {
			pieces = append(pieces, contentPiece{
				text:    c,
				isTable: false,
			})
		}
	}

	inFence := false
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Pipe tables inside fenced code blocks are code, not tables: keep the
		// fence intact so it is not torn apart around a pseudo-table.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			textLines = append(textLines, line)
			i++
			continue
		}
		if inFence {
			textLines = append(textLines, line)
			i++
			continue
		}

		// Check if line i is a table header and line i+1 is a separator
		if i+1 < len(lines) && strings.Contains(trimmed, "|") && isTableSeparator(lines[i+1]) {
			flushText()

			tbl := tableBlock{
				header:    line,
				separator: lines[i+1],
			}

			j := i + 2
			for j < len(lines) {
				rowTrimmed := strings.TrimSpace(lines[j])
				if rowTrimmed == "" || !strings.Contains(rowTrimmed, "|") {
					break
				}
				tbl.rows = append(tbl.rows, lines[j])
				j++
			}

			tableChunks := mc.chunkTable(tbl)
			pieces = append(pieces, tableChunks...)
			i = j
			continue
		}

		textLines = append(textLines, line)
		i++
	}

	flushText()
	return pieces
}

func (mc *MarkdownChunker) chunkTable(tbl tableBlock) []contentPiece {
	fullTableLines := append([]string{tbl.header, tbl.separator}, tbl.rows...)
	fullTableText := strings.Join(fullTableLines, "\n")

	// If table fits within ChunkSize, keep it atomic (never split)
	if len(tbl.rows) == 0 || countWords(fullTableText) <= mc.ChunkSize {
		return []contentPiece{
			{
				text:    fullTableText,
				isTable: true,
				tableMeta: map[string]any{
					"table_rows": len(tbl.rows),
				},
			},
		}
	}

	// Large table: split rows while repeating header and separator on each chunk
	headerWords := countWords(tbl.header) + countWords(tbl.separator)
	var subChunks []contentPiece
	var currentRows []string
	currentWords := headerWords

	for _, row := range tbl.rows {
		rowWords := countWords(row)
		if len(currentRows) > 0 && currentWords+rowWords > mc.ChunkSize {
			subTableLines := append([]string{tbl.header, tbl.separator}, currentRows...)
			subChunks = append(subChunks, contentPiece{
				text:    strings.Join(subTableLines, "\n"),
				isTable: true,
				tableMeta: map[string]any{
					"table_rows":     len(currentRows),
					"table_subchunk": len(subChunks),
				},
			})
			currentRows = nil
			currentWords = headerWords
		}
		currentRows = append(currentRows, row)
		currentWords += rowWords
	}

	if len(currentRows) > 0 {
		subTableLines := append([]string{tbl.header, tbl.separator}, currentRows...)
		subChunks = append(subChunks, contentPiece{
			text:    strings.Join(subTableLines, "\n"),
			isTable: true,
			tableMeta: map[string]any{
				"table_rows":     len(currentRows),
				"table_subchunk": len(subChunks),
			},
		})
	}

	for idx := range subChunks {
		subChunks[idx].tableMeta["total_table_subchunks"] = len(subChunks)
	}

	return subChunks
}
