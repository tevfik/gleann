package chunking

import (
	"strings"
	"testing"
)

func TestChunkDocument_Basic(t *testing.T) {
	chunker := NewMarkdownChunker(512, 64)

	doc := &StructuredDocument{
		Document: DocumentMeta{
			Title:  "Test Report",
			Format: "pdf",
		},
		Sections: []MarkdownSection{
			{
				ID:      "doc:test.pdf:s0",
				Heading: "Introduction",
				Level:   1,
				Content: "This is the introduction. It contains important background information.",
			},
			{
				ID:       "doc:test.pdf:s0.0",
				Heading:  "Background",
				Level:    2,
				Content:  "Some background details here.",
				ParentID: "doc:test.pdf:s0",
			},
		},
	}

	chunks := chunker.ChunkDocument(doc)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Check first chunk has context header
	if !strings.Contains(chunks[0].Text, "Introduction") {
		t.Error("chunk text should contain section heading in context header")
	}

	// Check metadata
	for _, ch := range chunks {
		if ch.Metadata["doc_title"] != "Test Report" {
			t.Errorf("expected doc_title=Test Report, got %v", ch.Metadata["doc_title"])
		}
		if ch.Metadata["doc_format"] != "pdf" {
			t.Errorf("expected doc_format=pdf, got %v", ch.Metadata["doc_format"])
		}
	}
}

func TestChunkDocument_EmptySections(t *testing.T) {
	chunker := NewMarkdownChunker(512, 64)

	doc := &StructuredDocument{
		Document: DocumentMeta{Title: "Empty"},
		Sections: []MarkdownSection{
			{ID: "s0", Heading: "Empty Section", Level: 1, Content: ""},
			{ID: "s1", Heading: "Has Content", Level: 1, Content: "Real content here."},
		},
	}

	chunks := chunker.ChunkDocument(doc)

	// Empty section should be skipped
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk (empty section skipped), got %d", len(chunks))
	}

	if chunks[0].SectionID != "s1" {
		t.Errorf("expected section_id=s1, got %s", chunks[0].SectionID)
	}
}

func TestChunkDocument_HierarchyBreadcrumb(t *testing.T) {
	chunker := NewMarkdownChunker(2048, 128)

	doc := &StructuredDocument{
		Document: DocumentMeta{Title: "Manual"},
		Sections: []MarkdownSection{
			{ID: "s0", Heading: "Chapter 1", Level: 1, Content: "Chapter content."},
			{ID: "s0.0", Heading: "Section A", Level: 2, Content: "Section A content.", ParentID: "s0"},
			{ID: "s0.0.0", Heading: "Detail", Level: 3, Content: "Detailed explanation here.", ParentID: "s0.0"},
		},
	}

	chunks := chunker.ChunkDocument(doc)

	// Find the detail chunk
	var detailChunk *MarkdownChunk
	for i, ch := range chunks {
		if ch.SectionID == "s0.0.0" {
			detailChunk = &chunks[i]
			break
		}
	}

	if detailChunk == nil {
		t.Fatal("expected a chunk for the Detail section")
	}

	// Context header should contain breadcrumb: Chapter 1 > Section A > Detail
	if !strings.Contains(detailChunk.Text, "Chapter 1") {
		t.Error("expected breadcrumb to include 'Chapter 1'")
	}
	if !strings.Contains(detailChunk.Text, "Section A") {
		t.Error("expected breadcrumb to include 'Section A'")
	}
	if !strings.Contains(detailChunk.Text, "Detail") {
		t.Error("expected breadcrumb to include 'Detail'")
	}

	// SectionPath should be ["Chapter 1", "Section A", "Detail"]
	if len(detailChunk.SectionPath) != 3 {
		t.Fatalf("expected 3-element path, got %d: %v", len(detailChunk.SectionPath), detailChunk.SectionPath)
	}
}

func TestChunkDocument_NilDoc(t *testing.T) {
	chunker := NewMarkdownChunker(512, 64)
	chunks := chunker.ChunkDocument(nil)
	if chunks != nil {
		t.Errorf("expected nil for nil doc, got %d chunks", len(chunks))
	}
}

func TestChunkDocument_NoSections(t *testing.T) {
	chunker := NewMarkdownChunker(512, 64)
	doc := &StructuredDocument{
		Document: DocumentMeta{Title: "No Sections"},
		Sections: nil,
	}
	chunks := chunker.ChunkDocument(doc)
	if chunks != nil {
		t.Errorf("expected nil for empty sections, got %d chunks", len(chunks))
	}
}

func TestChunkDocument_MetadataFields(t *testing.T) {
	chunker := NewMarkdownChunker(2048, 128)

	doc := &StructuredDocument{
		Document: DocumentMeta{Title: "Report", Format: "docx"},
		Sections: []MarkdownSection{
			{
				ID:      "s0",
				Heading: "Summary",
				Level:   1,
				Content: "Executive summary goes here.",
				Summary: "Brief overview.",
			},
		},
	}

	chunks := chunker.ChunkDocument(doc)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}

	ch := chunks[0]
	if ch.Metadata["section_id"] != "s0" {
		t.Errorf("expected section_id=s0, got %v", ch.Metadata["section_id"])
	}
	if ch.Metadata["section_title"] != "Summary" {
		t.Errorf("expected section_title=Summary, got %v", ch.Metadata["section_title"])
	}
	if ch.Metadata["heading_level"] != 1 {
		t.Errorf("expected heading_level=1, got %v", ch.Metadata["heading_level"])
	}
	if ch.Metadata["section_summary"] != "Brief overview." {
		t.Errorf("expected section_summary, got %v", ch.Metadata["section_summary"])
	}
}

func TestChunkDocument_LargeSection(t *testing.T) {
	chunker := NewMarkdownChunker(100, 20)

	// Create content larger than chunk size to verify splitting.
	longContent := strings.Repeat("This is a test sentence. ", 50)

	doc := &StructuredDocument{
		Document: DocumentMeta{Title: "Big"},
		Sections: []MarkdownSection{
			{ID: "s0", Heading: "Big Section", Level: 1, Content: longContent},
		},
	}

	chunks := chunker.ChunkDocument(doc)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for large section, got %d", len(chunks))
	}

	// All chunks should belong to the same section.
	for _, ch := range chunks {
		if ch.SectionID != "s0" {
			t.Errorf("expected section_id=s0, got %s", ch.SectionID)
		}
	}

	// Check chunk_index and total_chunks metadata.
	for i, ch := range chunks {
		if ch.Metadata["chunk_index"] != i {
			t.Errorf("chunk %d: expected chunk_index=%d, got %v", i, i, ch.Metadata["chunk_index"])
		}
		if ch.Metadata["total_chunks"] != len(chunks) {
			t.Errorf("chunk %d: expected total_chunks=%d, got %v", i, len(chunks), ch.Metadata["total_chunks"])
		}
	}
}

func TestChunkMarkdown_Fallback(t *testing.T) {
	chunker := NewMarkdownChunker(2048, 128)

	md := "# Chapter 1\n\nSome text here.\n\n## Section A\n\nDetails about section A."
	chunks := chunker.ChunkMarkdown(md, "notes.md")

	if len(chunks) == 0 {
		t.Fatal("expected chunks from markdown fallback")
	}

	// Check source metadata
	for _, ch := range chunks {
		if ch.Metadata["source"] != "notes.md" {
			t.Errorf("expected source=notes.md, got %v", ch.Metadata["source"])
		}
	}
}

func TestChunkMarkdown_NoHeadings(t *testing.T) {
	chunker := NewMarkdownChunker(2048, 128)

	md := "Plain text without any headings.\n\nJust paragraphs."
	chunks := chunker.ChunkMarkdown(md, "plain.txt")

	if len(chunks) == 0 {
		t.Fatal("expected chunks even without headings")
	}
}

func TestParseMarkdownHeadings(t *testing.T) {
	md := "# Title\n\nIntro.\n\n## Sub 1\n\nSub 1 text.\n\n## Sub 2\n\nSub 2 text.\n\n### Deep\n\nDeep text."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(sections))
	}

	if sections[0].Heading != "Title" || sections[0].Level != 1 {
		t.Errorf("first section: expected Title/1, got %s/%d", sections[0].Heading, sections[0].Level)
	}

	// Check parent relationships
	if sections[1].ParentID != sections[0].ID {
		t.Errorf("Sub 1 should be child of Title, parentID=%s", sections[1].ParentID)
	}

	if sections[3].ParentID != sections[2].ID {
		t.Errorf("Deep should be child of Sub 2, parentID=%s", sections[3].ParentID)
	}
}

func TestBuildContextHeader(t *testing.T) {
	tests := []struct {
		path []string
		want string
	}{
		{nil, ""},
		{[]string{"Title"}, "# Title"},
		{[]string{"A", "B"}, "# A > ## B"},
		{[]string{"A", "B", "C"}, "# A > ## B > ### C"},
	}

	for _, tt := range tests {
		got := buildContextHeader(tt.path)
		if got != tt.want {
			t.Errorf("buildContextHeader(%v) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// --- Robust parser tests ---

func TestParseMarkdownHeadings_SkipsFencedCodeBlocks(t *testing.T) {
	md := "# Real Title\n\nSome text.\n\n```python\n# this is a comment not a heading\ndef foo():\n    pass\n```\n\n## Real Section\n\nMore text."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections (skipping fenced code), got %d", len(sections))
	}
	if sections[0].Heading != "Real Title" {
		t.Errorf("expected 'Real Title', got %q", sections[0].Heading)
	}
	if sections[1].Heading != "Real Section" {
		t.Errorf("expected 'Real Section', got %q", sections[1].Heading)
	}
}

func TestParseMarkdownHeadings_SkipsTildeFence(t *testing.T) {
	md := "# Title\n\n~~~bash\n# comment\necho hello\n~~~\n\n## Next\n\nText."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
}

func TestParseMarkdownHeadings_SkipsYAMLFrontMatter(t *testing.T) {
	md := "---\ntitle: My Doc\nauthor: Test\n---\n\n# Introduction\n\nHello world."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if sections[0].Heading != "Introduction" {
		t.Errorf("expected 'Introduction', got %q", sections[0].Heading)
	}
}

func TestParseMarkdownHeadings_SkipsHTMLComments(t *testing.T) {
	md := "# Title\n\n<!-- # Hidden Heading -->\n\nSome text.\n\n## Visible\n\nMore text."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections (comment skipped), got %d", len(sections))
	}
	if sections[1].Heading != "Visible" {
		t.Errorf("expected 'Visible', got %q", sections[1].Heading)
	}
}

func TestParseMarkdownHeadings_SkipsMultilineHTMLComments(t *testing.T) {
	md := "# Title\n\n<!--\n# Not a heading\nSome comment text\n-->\n\n## Real\n\nContent."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
}

func TestParseMarkdownHeadings_SetextH1(t *testing.T) {
	md := "Title Here\n==========\n\nIntro paragraph.\n\nSubtitle\n--------\n\nSub text."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections (setext), got %d", len(sections))
	}
	if sections[0].Heading != "Title Here" || sections[0].Level != 1 {
		t.Errorf("expected setext H1 'Title Here', got %q level %d", sections[0].Heading, sections[0].Level)
	}
	if sections[1].Heading != "Subtitle" || sections[1].Level != 2 {
		t.Errorf("expected setext H2 'Subtitle', got %q level %d", sections[1].Heading, sections[1].Level)
	}
	if sections[1].ParentID != sections[0].ID {
		t.Errorf("setext H2 should be child of H1")
	}
}

func TestParseMarkdownHeadings_StripsAnchorTags(t *testing.T) {
	md := "# Introduction {#intro}\n\nText.\n\n## Details {#details}\n\nMore."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Heading != "Introduction" {
		t.Errorf("expected anchor stripped 'Introduction', got %q", sections[0].Heading)
	}
	if sections[1].Heading != "Details" {
		t.Errorf("expected anchor stripped 'Details', got %q", sections[1].Heading)
	}
}

func TestParseMarkdownHeadings_StripsTrailingHashes(t *testing.T) {
	md := "# Title ##\n\nText.\n\n## Section ###\n\nMore."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Heading != "Title" {
		t.Errorf("expected trailing hashes stripped 'Title', got %q", sections[0].Heading)
	}
	if sections[1].Heading != "Section" {
		t.Errorf("expected trailing hashes stripped 'Section', got %q", sections[1].Heading)
	}
}

func TestParseMarkdownHeadings_RequiresSpaceAfterHash(t *testing.T) {
	md := "# Real Title\n\n#notaheading\n\nSome text."
	sections := ParseMarkdownHeadings(md)

	if len(sections) != 1 {
		t.Fatalf("expected 1 section (#notaheading should be skipped), got %d", len(sections))
	}
}

func TestParseMarkdownHeadings_EmptyDocument(t *testing.T) {
	sections := ParseMarkdownHeadings("")
	if sections != nil {
		t.Errorf("expected nil for empty doc, got %d sections", len(sections))
	}
}

func TestParseMarkdownHeadings_NoHeadings(t *testing.T) {
	sections := ParseMarkdownHeadings("Just some plain text.\n\nNo headings here.")
	if sections != nil {
		t.Errorf("expected nil for no headings, got %d sections", len(sections))
	}
}

func TestParseMarkdownHeadings_ComplexDocument(t *testing.T) {
	md := `---
title: Complex Doc
---

# Main Title

Introduction text.

` + "```go" + `
# not a heading
func main() {}
` + "```" + `

## Section 1

Content.

### Subsection 1.1

Details.

<!-- # hidden heading -->

## Section 2

More content.

### Subsection 2.1 {#s2-1}

Even more.`

	sections := ParseMarkdownHeadings(md)

	// Should get: Main Title, Section 1, Subsection 1.1, Section 2, Subsection 2.1
	if len(sections) != 5 {
		t.Fatalf("expected 5 sections from complex doc, got %d", len(sections))
	}

	expected := []struct {
		heading string
		level   int
	}{
		{"Main Title", 1},
		{"Section 1", 2},
		{"Subsection 1.1", 3},
		{"Section 2", 2},
		{"Subsection 2.1", 3},
	}
	for i, exp := range expected {
		if sections[i].Heading != exp.heading {
			t.Errorf("section %d: expected heading %q, got %q", i, exp.heading, sections[i].Heading)
		}
		if sections[i].Level != exp.level {
			t.Errorf("section %d: expected level %d, got %d", i, exp.level, sections[i].Level)
		}
	}

	// Hierarchy: Section 1 and Section 2 are children of Main Title
	if sections[1].ParentID != sections[0].ID {
		t.Errorf("Section 1 should be child of Main Title")
	}
	if sections[3].ParentID != sections[0].ID {
		t.Errorf("Section 2 should be child of Main Title")
	}
	// Subsection 1.1 → Section 1, Subsection 2.1 → Section 2
	if sections[2].ParentID != sections[1].ID {
		t.Errorf("Subsection 1.1 should be child of Section 1")
	}
	if sections[4].ParentID != sections[3].ID {
		t.Errorf("Subsection 2.1 should be child of Section 2")
	}
	// Anchor tag should be stripped
	if sections[4].Heading != "Subsection 2.1" {
		t.Errorf("anchor tag not stripped: %q", sections[4].Heading)
	}
}

func TestIsMarkdownFile(t *testing.T) {
	tests := []struct {
		file string
		want bool
	}{
		{"README.md", true},
		{"notes.markdown", true},
		{"doc.mdx", true},
		{"guide.mdown", true},
		{"main.go", false},
		{"data.json", false},
		{"style.css", false},
		{"report.txt", false},
		{"path/to/README.md", true},
	}
	for _, tt := range tests {
		got := IsMarkdownFile(tt.file)
		if got != tt.want {
			t.Errorf("IsMarkdownFile(%q) = %v, want %v", tt.file, got, tt.want)
		}
	}
}
