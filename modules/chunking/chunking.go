// Package chunking provides text splitting utilities for indexing.
package chunking

import (
	"strings"
	"unicode"
)

// Chunk represents a piece of text and its associated metadata.
type Chunk struct {
	Text     string
	Metadata map[string]any
}

// SentenceSplitter splits text into chunks by sentences.
type SentenceSplitter struct {
	ChunkSize    int
	ChunkOverlap int
}

// NewSentenceSplitter creates a new sentence splitter with the given parameters.
func NewSentenceSplitter(chunkSize, chunkOverlap int) *SentenceSplitter {
	if chunkSize <= 0 {
		chunkSize = 512
	}
	if chunkOverlap < 0 {
		chunkOverlap = 50
	}
	return &SentenceSplitter{
		ChunkSize:    chunkSize,
		ChunkOverlap: chunkOverlap,
	}
}

// Chunk splits text into chunks.
func (s *SentenceSplitter) Chunk(text string) []string {
	if text == "" {
		return nil
	}

	// Try paragraph splitting first.
	paragraphs := splitParagraphs(text)
	if len(paragraphs) <= 1 {
		// Fall back to sentence splitting.
		return s.chunkBySentences(text)
	}

	var chunks []string
	var currentChunk strings.Builder
	currentLen := 0

	for _, para := range paragraphs {
		paraLen := countWords(para)
		if currentLen+paraLen > s.ChunkSize && currentLen > 0 {
			chunks = append(chunks, strings.TrimSpace(currentChunk.String()))
			// Overlap: keep some of the previous content.
			overlapText := getOverlapText(currentChunk.String(), s.ChunkOverlap)
			currentChunk.Reset()
			if overlapText != "" {
				currentChunk.WriteString(overlapText)
				currentChunk.WriteString("\n\n")
				currentLen = countWords(overlapText)
			} else {
				currentLen = 0
			}
		}
		if currentLen > 0 {
			currentChunk.WriteString("\n\n")
		}
		currentChunk.WriteString(para)
		currentLen += paraLen
	}

	if currentLen > 0 {
		chunks = append(chunks, strings.TrimSpace(currentChunk.String()))
	}

	return chunks
}

// ChunkWithMetadata splits text and preserves source metadata.
func (s *SentenceSplitter) ChunkWithMetadata(text string, metadata map[string]any) []Chunk {
	chunks := s.Chunk(text)
	items := make([]Chunk, len(chunks))
	for i, chunk := range chunks {
		meta := make(map[string]any)
		for k, v := range metadata {
			meta[k] = v
		}
		meta["chunk_index"] = i
		meta["total_chunks"] = len(chunks)
		items[i] = Chunk{
			Text:     chunk,
			Metadata: meta,
		}
	}
	return items
}

// chunkBySentences splits text into chunks by sentences.
func (s *SentenceSplitter) chunkBySentences(text string) []string {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return nil
	}

	var chunks []string
	var currentChunk strings.Builder
	currentLen := 0

	for _, sent := range sentences {
		sentLen := countWords(sent)
		if currentLen+sentLen > s.ChunkSize && currentLen > 0 {
			chunks = append(chunks, strings.TrimSpace(currentChunk.String()))
			overlapText := getOverlapText(currentChunk.String(), s.ChunkOverlap)
			currentChunk.Reset()
			if overlapText != "" {
				currentChunk.WriteString(overlapText)
				currentChunk.WriteString(" ")
				currentLen = countWords(overlapText)
			} else {
				currentLen = 0
			}
		}
		if currentLen > 0 {
			currentChunk.WriteString(" ")
		}
		currentChunk.WriteString(sent)
		currentLen += sentLen
	}

	if currentLen > 0 {
		chunks = append(chunks, strings.TrimSpace(currentChunk.String()))
	}

	return chunks
}

// CodeChunker splits code files into logical chunks based on structure.
type CodeChunker struct {
	ChunkSize    int
	ChunkOverlap int
}

// NewCodeChunker creates a new code chunker.
func NewCodeChunker(chunkSize, chunkOverlap int) *CodeChunker {
	if chunkSize <= 0 {
		chunkSize = 512
	}
	return &CodeChunker{
		ChunkSize:    chunkSize,
		ChunkOverlap: chunkOverlap,
	}
}

// Chunk splits code into logical chunks (by function/class boundaries).
func (c *CodeChunker) Chunk(code string) []string {
	lines := strings.Split(code, "\n")
	if len(lines) == 0 {
		return nil
	}

	var chunks []string
	var currentChunk strings.Builder
	currentLines := 0

	for _, line := range lines {
		// Detect function/class boundaries.
		isBoundary := isCodeBoundary(line)

		if isBoundary && currentLines >= c.ChunkSize/10 {
			// Start new chunk at boundary.
			if currentLines > 0 {
				chunks = append(chunks, strings.TrimRight(currentChunk.String(), "\n"))
			}
			currentChunk.Reset()
			currentLines = 0
		}

		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
		currentLines++

		// Also split if chunk is too large.
		if currentLines >= c.ChunkSize/5 {
			chunks = append(chunks, strings.TrimRight(currentChunk.String(), "\n"))
			currentChunk.Reset()
			currentLines = 0
		}
	}

	if currentLines > 0 {
		chunks = append(chunks, strings.TrimRight(currentChunk.String(), "\n"))
	}

	return chunks
}

// ChunkWithMetadata splits code and preserves metadata.
func (c *CodeChunker) ChunkWithMetadata(code string, metadata map[string]any) []Chunk {
	chunks := c.Chunk(code)
	items := make([]Chunk, len(chunks))
	for i, chunk := range chunks {
		meta := make(map[string]any)
		for k, v := range metadata {
			meta[k] = v
		}
		meta["chunk_index"] = i
		meta["total_chunks"] = len(chunks)
		items[i] = Chunk{
			Text:     chunk,
			Metadata: meta,
		}
	}
	return items
}

// isCodeBoundary detects if a line is a function/class/method boundary.
func isCodeBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	// Common code boundaries.
	prefixes := []string{
		"func ", "def ", "class ", "type ", "pub fn ",
		"public ", "private ", "protected ",
		"function ", "const ", "var ", "let ",
		"impl ", "trait ", "interface ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// IsCodeFile checks if a filename looks like a code file.
func IsCodeFile(filename string) bool {
	codeExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true,
		".java": true, ".c": true, ".cpp": true, ".h": true,
		".rs": true, ".rb": true, ".php": true, ".swift": true,
		".kt": true, ".scala": true, ".cs": true, ".r": true,
		".lua": true, ".sh": true, ".bash": true, ".zsh": true,
		".sql": true, ".html": true, ".css": true, ".scss": true,
		".yaml": true, ".yml": true, ".toml": true, ".json": true,
		".xml": true, ".rst": true, ".tex": true,
	}

	for ext := range codeExts {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return true
		}
	}
	return false
}

// --- Helper functions ---

func splitParagraphs(text string) []string {
	parts := strings.Split(text, "\n\n")
	var paragraphs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	return paragraphs
}

func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		current.WriteRune(runes[i])

		// Detect sentence end: period/question/exclamation followed by space or end.
		if runes[i] == '.' || runes[i] == '?' || runes[i] == '!' {
			if i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
				s := strings.TrimSpace(current.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				current.Reset()
			}
		}
	}

	remaining := strings.TrimSpace(current.String())
	if remaining != "" {
		sentences = append(sentences, remaining)
	}

	return sentences
}

func countWords(text string) int {
	return len(strings.Fields(text))
}

func getOverlapText(text string, overlapWords int) string {
	words := strings.Fields(text)
	if len(words) <= overlapWords {
		return text
	}
	return strings.Join(words[len(words)-overlapWords:], " ")
}
