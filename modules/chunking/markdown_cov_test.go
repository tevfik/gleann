package chunking

import (
	"strings"
	"testing"
	"time"
)

func TestBuildContextHeader_DepthCap(t *testing.T) {
	if got := buildContextHeader(nil); got != "" {
		t.Errorf("empty path = %q, want empty", got)
	}
	got := buildContextHeader([]string{"A", "B"})
	if got != "# A > ## B" {
		t.Errorf("header = %q, want %q", got, "# A > ## B")
	}
	// Depth beyond 6 stays at six hashes.
	deep := buildContextHeader([]string{"1", "2", "3", "4", "5", "6", "7", "8"})
	if !strings.HasSuffix(deep, "###### 7 > ###### 8") {
		t.Errorf("deep header = %q, want levels capped at 6", deep)
	}
}

func TestParseMarkdownHeadings_RejectsInvalidHeadings(t *testing.T) {
	md := strings.Join([]string{
		"#nospace is not a heading",
		"####### seven hashes is not a heading",
		"#",
		"#   ",
		"# Real heading",
		"body text",
		"## Child",
		"more",
	}, "\n")
	sections := ParseMarkdownHeadings(md)
	var titles []string
	for _, s := range sections {
		titles = append(titles, s.Heading)
	}
	if len(titles) != 2 || titles[0] != "Real heading" || titles[1] != "Child" {
		t.Errorf("headings = %v, want only [Real heading Child]", titles)
	}
}

func TestInferTitle(t *testing.T) {
	tests := []struct {
		name     string
		sections []MarkdownSection
		want     string
	}{
		{"prefers level 1", []MarkdownSection{{Heading: "Sub", Level: 2}, {Heading: "Top", Level: 1}}, "Top"},
		{"falls back to first section", []MarkdownSection{{Heading: "Sub", Level: 2}, {Heading: "Other", Level: 3}}, "Sub"},
		{"no sections", nil, "Untitled"},
	}
	for _, tc := range tests {
		if got := inferTitle(tc.sections); got != tc.want {
			t.Errorf("%s: inferTitle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEnrichChunkMetadata_OmitsEmptyOptionals(t *testing.T) {
	full := EnrichChunkMetadata(nil, ProvenanceMetadata{
		ContentHash:    "abc",
		IndexedAt:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		EmbeddingModel: "bge",
		Source:         "doc.md",
		StartLine:      3,
	})
	want := map[string]any{
		"content_hash":    "abc",
		"indexed_at":      "2026-01-02T03:04:05Z",
		"embedding_model": "bge",
		"source":          "doc.md",
		"start_line":      3,
	}
	for k, v := range want {
		if full[k] != v {
			t.Errorf("meta[%q] = %v, want %v", k, full[k], v)
		}
	}

	// Zero-valued optional fields are omitted and existing keys preserved.
	base := map[string]any{"keep": true}
	bare := EnrichChunkMetadata(base, ProvenanceMetadata{})
	for _, k := range []string{"content_hash", "indexed_at", "embedding_model", "source"} {
		if _, ok := bare[k]; ok {
			t.Errorf("meta has %q for an empty provenance", k)
		}
	}
	if bare["keep"] != true {
		t.Error("existing metadata was dropped")
	}
}
