package chunking

import (
	"strings"
	"testing"
	"time"
)

func TestComputeByteOffsets_BasicCase(t *testing.T) {
	fullText := "Hello world. This is a test. Another sentence here."
	chunks := []string{"Hello world.", "This is a test.", "Another sentence here."}

	provs := ComputeByteOffsets(fullText, chunks)

	if len(provs) != 3 {
		t.Fatalf("expected 3 provenance entries, got %d", len(provs))
	}

	// First chunk should start at byte 0.
	if provs[0].StartByte != 0 {
		t.Errorf("chunk 0 start byte: expected 0, got %d", provs[0].StartByte)
	}
	if provs[0].EndByte != 12 { // len("Hello world.") = 12
		t.Errorf("chunk 0 end byte: expected 12, got %d", provs[0].EndByte)
	}

	// All chunks should have correct total.
	for i, p := range provs {
		if p.TotalChunks != 3 {
			t.Errorf("chunk %d: expected TotalChunks=3, got %d", i, p.TotalChunks)
		}
		if p.ChunkIndex != i {
			t.Errorf("chunk %d: expected ChunkIndex=%d, got %d", i, i, p.ChunkIndex)
		}
	}
}

func TestComputeByteOffsets_MultiLine(t *testing.T) {
	fullText := "line one\nline two\nline three\nline four"
	chunks := []string{"line one\nline two", "line three\nline four"}

	provs := ComputeByteOffsets(fullText, chunks)

	if provs[0].StartLine != 1 {
		t.Errorf("chunk 0 start line: expected 1, got %d", provs[0].StartLine)
	}
	if provs[0].EndLine != 2 {
		t.Errorf("chunk 0 end line: expected 2, got %d", provs[0].EndLine)
	}
	if provs[1].StartLine != 3 {
		t.Errorf("chunk 1 start line: expected 3, got %d", provs[1].StartLine)
	}
	if provs[1].EndLine != 4 {
		t.Errorf("chunk 1 end line: expected 4, got %d", provs[1].EndLine)
	}
}

func TestComputeByteOffsets_SingleChunk(t *testing.T) {
	text := "just one chunk"
	provs := ComputeByteOffsets(text, []string{text})

	if len(provs) != 1 {
		t.Fatalf("expected 1, got %d", len(provs))
	}
	if provs[0].StartByte != 0 || provs[0].EndByte != len(text) {
		t.Errorf("expected [0, %d], got [%d, %d]", len(text), provs[0].StartByte, provs[0].EndByte)
	}
	if provs[0].TotalChunks != 1 {
		t.Errorf("expected TotalChunks=1, got %d", provs[0].TotalChunks)
	}
}

func TestComputeByteOffsets_EmptyChunks(t *testing.T) {
	provs := ComputeByteOffsets("some text", nil)
	if len(provs) != 0 {
		t.Errorf("nil chunks should return empty, got %d", len(provs))
	}

	provs = ComputeByteOffsets("some text", []string{})
	if len(provs) != 0 {
		t.Errorf("empty chunks should return empty, got %d", len(provs))
	}
}

func TestComputeByteOffsets_ChunkNotFound(t *testing.T) {
	// When chunk text doesn't appear verbatim (e.g. overlap editing),
	// it should fall back gracefully.
	fullText := "Hello world"
	chunks := []string{"NOT IN TEXT"}

	provs := ComputeByteOffsets(fullText, chunks)
	if len(provs) != 1 {
		t.Fatalf("expected 1, got %d", len(provs))
	}
	// Should not panic; uses fallback position.
}

func TestEnrichChunkMetadata_NilMeta(t *testing.T) {
	prov := ProvenanceMetadata{
		Source:    "test.go",
		StartByte: 10,
		EndByte:   50,
		StartLine: 2,
		EndLine:   4,
	}

	meta := EnrichChunkMetadata(nil, prov)
	if meta == nil {
		t.Fatal("should create new map when nil")
	}
	if meta["source"] != "test.go" {
		t.Errorf("expected source=test.go, got %v", meta["source"])
	}
	if meta["start_byte"] != 10 {
		t.Errorf("expected start_byte=10, got %v", meta["start_byte"])
	}
	if meta["end_byte"] != 50 {
		t.Errorf("expected end_byte=50, got %v", meta["end_byte"])
	}
	if meta["start_line"] != 2 {
		t.Errorf("expected start_line=2, got %v", meta["start_line"])
	}
}

func TestEnrichChunkMetadata_ExistingMeta(t *testing.T) {
	existing := map[string]any{"custom_key": "value"}
	prov := ProvenanceMetadata{
		StartByte:   0,
		EndByte:     100,
		StartLine:   1,
		EndLine:     5,
		ChunkIndex:  2,
		TotalChunks: 10,
		ContentHash: "abc123",
		IndexedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	meta := EnrichChunkMetadata(existing, prov)
	if meta["custom_key"] != "value" {
		t.Error("existing keys should be preserved")
	}
	if meta["chunk_index"] != 2 {
		t.Errorf("expected chunk_index=2, got %v", meta["chunk_index"])
	}
	if meta["content_hash"] != "abc123" {
		t.Errorf("expected content_hash, got %v", meta["content_hash"])
	}
	if _, ok := meta["indexed_at"]; !ok {
		t.Error("indexed_at should be set")
	}
}

func TestEnrichChunkMetadata_OptionalFields(t *testing.T) {
	// Empty optional fields should not be added.
	prov := ProvenanceMetadata{
		StartByte: 0,
		EndByte:   10,
	}
	meta := EnrichChunkMetadata(nil, prov)

	if _, ok := meta["content_hash"]; ok {
		t.Error("empty ContentHash should not be added")
	}
	if _, ok := meta["embedding_model"]; ok {
		t.Error("empty EmbeddingModel should not be added")
	}
	if _, ok := meta["source"]; ok {
		t.Error("empty Source should not be added")
	}
}

func TestSentenceSplitter_ChunkWithProvenance(t *testing.T) {
	splitter := NewSentenceSplitter(50, 10)

	fullText := "First sentence here. Second sentence here. Third sentence for testing."
	baseMeta := map[string]any{"source": "doc.txt"}

	chunks := splitter.ChunkWithProvenance(fullText, baseMeta)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for i, c := range chunks {
		if c.Text == "" {
			t.Errorf("chunk %d has empty text", i)
		}
		if c.Metadata == nil {
			t.Errorf("chunk %d has nil metadata", i)
			continue
		}
		if c.Metadata["source"] != "doc.txt" {
			t.Errorf("chunk %d: source not preserved", i)
		}
		if _, ok := c.Metadata["start_byte"]; !ok {
			t.Errorf("chunk %d: missing start_byte", i)
		}
		if _, ok := c.Metadata["start_line"]; !ok {
			t.Errorf("chunk %d: missing start_line", i)
		}
	}
}

func TestCodeChunker_ChunkWithProvenance(t *testing.T) {
	chunker := NewCodeChunker(100, 20)

	code := "func main() {\n\tfmt.Println(\"hello\")\n}\n\nfunc helper() {\n\treturn\n}"
	baseMeta := map[string]any{"source": "main.go", "language": "go"}

	chunks := chunker.ChunkWithProvenance(code, baseMeta)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for i, c := range chunks {
		if c.Metadata["source"] != "main.go" {
			t.Errorf("chunk %d: source not preserved", i)
		}
		if c.Metadata["language"] != "go" {
			t.Errorf("chunk %d: language not preserved", i)
		}
	}
}

func TestComputeByteOffsets_DuplicateChunks(t *testing.T) {
	// When the same text appears multiple times, each chunk should
	// match sequentially (not re-match the first occurrence).
	fullText := "hello hello hello"
	chunks := []string{"hello", "hello", "hello"}

	provs := ComputeByteOffsets(fullText, chunks)
	if len(provs) != 3 {
		t.Fatalf("expected 3, got %d", len(provs))
	}

	// Each chunk should start at a different position.
	starts := make(map[int]bool)
	for _, p := range provs {
		if starts[p.StartByte] {
			t.Errorf("duplicate StartByte %d — chunks should match sequentially", p.StartByte)
		}
		starts[p.StartByte] = true
	}
}

func TestComputeByteOffsets_UnicodeText(t *testing.T) {
	fullText := "Merhaba dünya. Güzel gün."
	chunks := []string{"Merhaba dünya.", "Güzel gün."}

	provs := ComputeByteOffsets(fullText, chunks)
	if len(provs) != 2 {
		t.Fatalf("expected 2, got %d", len(provs))
	}

	// Verify the byte offsets actually extract the right text.
	extracted := fullText[provs[0].StartByte:provs[0].EndByte]
	if extracted != chunks[0] {
		t.Errorf("extracted %q, expected %q", extracted, chunks[0])
	}
}

func TestComputeByteOffsets_VerifyExtraction(t *testing.T) {
	fullText := "The quick brown fox jumps over the lazy dog."
	splitter := NewSentenceSplitter(20, 5)
	rawChunks := splitter.Chunk(fullText)

	provs := ComputeByteOffsets(fullText, rawChunks)

	for i, p := range provs {
		if p.StartByte >= 0 && p.EndByte <= len(fullText) && p.StartByte < p.EndByte {
			extracted := fullText[p.StartByte:p.EndByte]
			if !strings.Contains(extracted, rawChunks[i][:minInt(10, len(rawChunks[i]))]) {
				t.Errorf("chunk %d: extraction doesn't match original", i)
			}
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
