// Package chunking — provenance.go
// Byte-level provenance: every chunk carries exact source location metadata
// so search results trace back to the precise file, byte offset, and line number
// they originated from. This enables debuggable, explainable RAG pipelines.
package chunking

import (
	"strings"
	"time"
)

// ProvenanceMetadata holds byte-level lineage information for a chunk.
// Embed this in chunk metadata so every search result carries its exact origin.
type ProvenanceMetadata struct {
	// Source is the relative file path within the indexed directory.
	Source string `json:"source"`
	// StartByte is the inclusive byte offset in the source file.
	StartByte int `json:"start_byte"`
	// EndByte is the exclusive byte offset in the source file.
	EndByte int `json:"end_byte"`
	// StartLine is the 1-based line number of the first line in the chunk.
	StartLine int `json:"start_line"`
	// EndLine is the 1-based line number of the last line in the chunk.
	EndLine int `json:"end_line"`
	// ChunkIndex is the ordinal position within the source.
	ChunkIndex int `json:"chunk_index"`
	// TotalChunks is how many chunks were produced from this source.
	TotalChunks int `json:"total_chunks"`
	// ContentHash is the SHA-256 hex digest of the chunk text.
	ContentHash string `json:"content_hash"`
	// IndexedAt is when this chunk was indexed.
	IndexedAt time.Time `json:"indexed_at"`
	// EmbeddingModel is which model produced the vector for this chunk.
	EmbeddingModel string `json:"embedding_model,omitempty"`
}

// ComputeByteOffsets calculates byte-level provenance for each chunk given
// the full source text and the chunk texts. It searches for each chunk in
// the full text sequentially to find byte offsets.
func ComputeByteOffsets(fullText string, chunks []string) []ProvenanceMetadata {
	provs := make([]ProvenanceMetadata, len(chunks))
	searchFrom := 0

	for i, chunk := range chunks {
		start := strings.Index(fullText[searchFrom:], chunk)
		if start == -1 {
			// Chunk not found verbatim (overlap edits may cause this);
			// fall back to approximate sequential position.
			start = searchFrom
		} else {
			start += searchFrom
		}
		end := start + len(chunk)

		startLine := 1 + strings.Count(fullText[:start], "\n")
		endLine := 1 + strings.Count(fullText[:end], "\n")

		provs[i] = ProvenanceMetadata{
			StartByte:   start,
			EndByte:     end,
			StartLine:   startLine,
			EndLine:     endLine,
			ChunkIndex:  i,
			TotalChunks: len(chunks),
		}
		// Advance search cursor so we don't re-match the same region.
		if end > searchFrom {
			searchFrom = end
		}
	}
	return provs
}

// EnrichChunkMetadata merges provenance info into a chunk's metadata map.
func EnrichChunkMetadata(meta map[string]any, prov ProvenanceMetadata) map[string]any {
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["start_byte"] = prov.StartByte
	meta["end_byte"] = prov.EndByte
	meta["start_line"] = prov.StartLine
	meta["end_line"] = prov.EndLine
	meta["chunk_index"] = prov.ChunkIndex
	meta["total_chunks"] = prov.TotalChunks
	if prov.ContentHash != "" {
		meta["content_hash"] = prov.ContentHash
	}
	if !prov.IndexedAt.IsZero() {
		meta["indexed_at"] = prov.IndexedAt.Format(time.RFC3339)
	}
	if prov.EmbeddingModel != "" {
		meta["embedding_model"] = prov.EmbeddingModel
	}
	if prov.Source != "" {
		meta["source"] = prov.Source
	}
	return meta
}

// ChunkWithProvenance splits text and attaches byte-level provenance metadata.
func (s *SentenceSplitter) ChunkWithProvenance(fullText string, baseMeta map[string]any) []Chunk {
	rawChunks := s.Chunk(fullText)
	provs := ComputeByteOffsets(fullText, rawChunks)

	items := make([]Chunk, len(rawChunks))
	for i, text := range rawChunks {
		meta := make(map[string]any)
		for k, v := range baseMeta {
			meta[k] = v
		}
		meta = EnrichChunkMetadata(meta, provs[i])

		items[i] = Chunk{
			Text:     text,
			Metadata: meta,
		}
	}
	return items
}

// ChunkWithProvenance for CodeChunker — attaches byte-level provenance.
func (c *CodeChunker) ChunkWithProvenance(fullText string, baseMeta map[string]any) []Chunk {
	rawChunks := c.Chunk(fullText)
	provs := ComputeByteOffsets(fullText, rawChunks)

	items := make([]Chunk, len(rawChunks))
	for i, text := range rawChunks {
		meta := make(map[string]any)
		for k, v := range baseMeta {
			meta[k] = v
		}
		meta = EnrichChunkMetadata(meta, provs[i])

		items[i] = Chunk{
			Text:     text,
			Metadata: meta,
		}
	}
	return items
}
