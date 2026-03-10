package main

import (
	"testing"

	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}

	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestStrVal(t *testing.T) {
	m := map[string]any{
		"name":  "hello",
		"count": 42,
	}

	if got := strVal(m, "name"); got != "hello" {
		t.Errorf("strVal(name) = %q, want %q", got, "hello")
	}
	if got := strVal(m, "count"); got != "" {
		t.Errorf("strVal(count) = %q, want empty (not a string)", got)
	}
	if got := strVal(m, "missing"); got != "" {
		t.Errorf("strVal(missing) = %q, want empty", got)
	}
}

func TestPluginResultToDoc(t *testing.T) {
	result := &gleann.PluginResult{
		Nodes: []gleann.PluginNode{
			{
				Type: "Document",
				Data: map[string]any{
					"title":      "Test Doc",
					"format":     "pdf",
					"summary":    "A test document",
					"word_count": float64(100),
					"page_count": float64(5),
				},
			},
			{
				Type: "Section",
				Data: map[string]any{
					"id":      "sec1",
					"heading": "Introduction",
					"level":   float64(1),
					"content": "Hello world",
					"summary": "Intro summary",
				},
			},
			{
				Type: "Section",
				Data: map[string]any{
					"id":      "sec2",
					"heading": "Details",
					"level":   float64(2),
					"content": "Some details",
					"summary": "Detail summary",
				},
			},
		},
		Edges: []gleann.PluginEdge{
			{
				Type: "HAS_SUBSECTION",
				From: "sec1",
				To:   "sec2",
			},
		},
	}

	doc := pluginResultToDoc(result)

	if doc.Document.Title != "Test Doc" {
		t.Errorf("title = %q, want %q", doc.Document.Title, "Test Doc")
	}
	if doc.Document.Format != "pdf" {
		t.Errorf("format = %q, want %q", doc.Document.Format, "pdf")
	}
	if doc.Document.WordCount != 100 {
		t.Errorf("word_count = %d, want 100", doc.Document.WordCount)
	}
	if doc.Document.PageCount == nil || *doc.Document.PageCount != 5 {
		t.Errorf("page_count = %v, want 5", doc.Document.PageCount)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("sections count = %d, want 2", len(doc.Sections))
	}
	// sec2 should have ParentID = "sec1" from the HAS_SUBSECTION edge.
	if doc.Sections[1].ParentID != "sec1" {
		t.Errorf("sec2 ParentID = %q, want %q", doc.Sections[1].ParentID, "sec1")
	}
}

func TestMarkdownToPluginResult(t *testing.T) {
	sections := []chunking.MarkdownSection{
		{ID: "s1", Heading: "Title", Level: 1, Content: "Top content"},
		{ID: "s2", Heading: "Sub", Level: 2, Content: "Sub content", ParentID: "s1"},
	}

	result := markdownToPluginResult(sections, "README.md", 50)

	// Should have 1 Document + 2 Section nodes.
	if len(result.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(result.Nodes))
	}

	docNode := result.Nodes[0]
	if docNode.Type != "Document" {
		t.Errorf("first node type = %q, want Document", docNode.Type)
	}
	if docNode.Data["title"] != "Title" {
		t.Errorf("title = %v, want Title", docNode.Data["title"])
	}

	// Top-level section → HAS_SECTION edge
	// Child section → HAS_SUBSECTION edge
	hasSection := 0
	hasSubsection := 0
	for _, e := range result.Edges {
		switch e.Type {
		case "HAS_SECTION":
			hasSection++
		case "HAS_SUBSECTION":
			hasSubsection++
		}
	}
	if hasSection != 1 {
		t.Errorf("HAS_SECTION edges = %d, want 1", hasSection)
	}
	if hasSubsection != 1 {
		t.Errorf("HAS_SUBSECTION edges = %d, want 1", hasSubsection)
	}
}

func TestMarkdownToPluginResult_NoH1(t *testing.T) {
	sections := []chunking.MarkdownSection{
		{ID: "s1", Heading: "Overview", Level: 2, Content: "content"},
	}

	result := markdownToPluginResult(sections, "test.md", 10)

	// When there's no H1, use the first heading as title.
	docNode := result.Nodes[0]
	if docNode.Data["title"] != "Overview" {
		t.Errorf("title = %v, want Overview (fallback from first heading)", docNode.Data["title"])
	}
}

func TestGetFlag(t *testing.T) {
	args := []string{"build", "--docs", "/tmp", "--model", "bge-m3", "--top-k", "5"}

	if got := getFlag(args, "--docs"); got != "/tmp" {
		t.Errorf("getFlag(--docs) = %q, want /tmp", got)
	}
	if got := getFlag(args, "--model"); got != "bge-m3" {
		t.Errorf("getFlag(--model) = %q, want bge-m3", got)
	}
	if got := getFlag(args, "--missing"); got != "" {
		t.Errorf("getFlag(--missing) = %q, want empty", got)
	}
	// Edge: flag at end with no value
	args2 := []string{"--docs"}
	if got := getFlag(args2, "--docs"); got != "" {
		t.Errorf("getFlag(--docs at end) = %q, want empty", got)
	}
}

func TestHasFlag(t *testing.T) {
	args := []string{"build", "--graph", "--docs", "/tmp"}

	if !hasFlag(args, "--graph") {
		t.Error("hasFlag(--graph) should be true")
	}
	if hasFlag(args, "--missing") {
		t.Error("hasFlag(--missing) should be false")
	}
}

func TestGetConfig_DefaultValues(t *testing.T) {
	args := []string{}
	config := getConfig(args)

	if config.EmbeddingModel == "" {
		t.Error("default EmbeddingModel should not be empty")
	}
	if config.IndexDir == "" {
		t.Error("default IndexDir should not be empty")
	}
}

func TestGetConfig_CustomFlags(t *testing.T) {
	args := []string{"--model", "custom-model", "--provider", "openai", "--top-k", "20", "--host", "http://custom:11434"}

	config := getConfig(args)

	if config.EmbeddingModel != "custom-model" {
		t.Errorf("EmbeddingModel = %q, want custom-model", config.EmbeddingModel)
	}
	if config.EmbeddingProvider != "openai" {
		t.Errorf("EmbeddingProvider = %q, want openai", config.EmbeddingProvider)
	}
	if config.SearchConfig.TopK != 20 {
		t.Errorf("TopK = %d, want 20", config.SearchConfig.TopK)
	}
	if config.OllamaHost != "http://custom:11434" {
		t.Errorf("OllamaHost = %q, want http://custom:11434", config.OllamaHost)
	}
}

func TestApplySavedConfig_CLIOverride(t *testing.T) {
	// When CLI flag is provided, saved config should not override it.
	config := gleann.DefaultConfig()
	config.EmbeddingModel = "cli-model"
	applySavedConfig(&config, []string{"--model", "cli-model"})
	if config.EmbeddingModel != "cli-model" {
		t.Errorf("EmbeddingModel = %q, want cli-model (CLI should take precedence)", config.EmbeddingModel)
	}
}

func TestIsCodeExtension(t *testing.T) {
	codeExts := []string{".go", ".py", ".js", ".ts", ".java", ".rs", ".c", ".cpp"}
	for _, ext := range codeExts {
		if !isCodeExtension(ext) {
			t.Errorf("isCodeExtension(%q) = false, want true", ext)
		}
	}

	nonCode := []string{".pdf", ".png", ".exe", ".zip", ""}
	for _, ext := range nonCode {
		if isCodeExtension(ext) {
			t.Errorf("isCodeExtension(%q) = true, want false", ext)
		}
	}
}
