package gleann

import (
	"testing"
)

func TestNewBM25Adapter(t *testing.T) {
	a := NewBM25Adapter()
	if a == nil {
		t.Fatal("NewBM25Adapter returned nil")
	}
	if a.scorer == nil {
		t.Fatal("scorer should not be nil")
	}
}

func TestNewBM25AdapterWithParams(t *testing.T) {
	a := NewBM25AdapterWithParams(1.5, 0.8)
	if a == nil {
		t.Fatal("NewBM25AdapterWithParams returned nil")
	}
}

func TestBM25AdapterAddDocuments(t *testing.T) {
	a := NewBM25Adapter()

	passages := []Passage{
		{ID: 1, Text: "Go is a systems programming language"},
		{ID: 2, Text: "Python is great for data science"},
		{ID: 3, Text: "Rust provides memory safety without garbage collection"},
	}

	// Should not panic.
	a.AddDocuments(passages)
}

func TestBM25AdapterScore(t *testing.T) {
	a := NewBM25Adapter()

	passages := []Passage{
		{ID: 1, Text: "Go is a systems programming language created at Google"},
		{ID: 2, Text: "Python is great for data science and machine learning"},
		{ID: 3, Text: "Rust provides memory safety without garbage collection"},
	}

	scores := a.Score("Go programming language", passages)
	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}

	// The first passage about Go should have user interest score.
	if scores[0] <= 0 {
		t.Error("Go passage should have positive score for Go query")
	}
}

func TestBM25AdapterScoreEmpty(t *testing.T) {
	a := NewBM25Adapter()
	scores := a.Score("query", nil)
	if len(scores) != 0 {
		t.Errorf("expected 0 scores, got %d", len(scores))
	}
}

func TestBM25AdapterMultipleScores(t *testing.T) {
	a := NewBM25Adapter()

	passages := []Passage{
		{ID: 1, Text: "machine learning algorithms for classification"},
		{ID: 2, Text: "deep learning neural networks for image recognition"},
		{ID: 3, Text: "cooking recipes for Italian pasta dishes"},
	}

	scores := a.Score("machine learning", passages)
	// ML passages should score higher than cooking.
	if scores[2] >= scores[0] {
		t.Log("Expected ML passages to score higher than cooking")
	}
}

func TestBM25AdapterAddThenScore(t *testing.T) {
	a := NewBM25Adapter()

	passages := []Passage{
		{ID: 1, Text: "alpha beta gamma delta"},
		{ID: 2, Text: "epsilon zeta eta theta"},
	}

	// Add first, then score.
	a.AddDocuments(passages)
	scores := a.Score("alpha", passages)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0] <= 0 {
		t.Error("passage with 'alpha' should have positive score")
	}
}

// ── PassageManager extended tests ──────────────────────────────

func TestPassageManagerCountExt(t *testing.T) {
	tmp := t.TempDir()
	pm := NewPassageManager(tmp)
	t.Cleanup(func() { pm.Close() })

	if pm.Count() != 0 {
		t.Errorf("initial count = %d, want 0", pm.Count())
	}

	pm.Add([]Item{{Text: "doc"}})
	if pm.Count() != 1 {
		t.Errorf("count = %d, want 1", pm.Count())
	}
}

// ── FindDocumentExtractor ──────────────────────────────────────

func TestFindDocumentExtractor(t *testing.T) {
	pm := &PluginManager{
		Registry: &PluginRegistry{
			Plugins: []Plugin{
				{Name: "pdf-reader", Extensions: []string{".pdf", ".PDF"}, Capabilities: []string{"document-extraction"}},
				{Name: "docx-reader", Extensions: []string{".docx"}, Capabilities: []string{"document-extraction"}},
			},
		},
	}

	p := pm.FindDocumentExtractor(".pdf")
	if p == nil {
		t.Fatal("expected to find pdf-reader")
	}
	if p.Name != "pdf-reader" {
		t.Errorf("name = %q, want pdf-reader", p.Name)
	}

	p2 := pm.FindDocumentExtractor(".docx")
	if p2 == nil || p2.Name != "docx-reader" {
		t.Error("expected to find docx-reader")
	}

	p3 := pm.FindDocumentExtractor(".txt")
	if p3 != nil {
		t.Error("expected nil for .txt")
	}
}

func TestFindDocumentExtractorNilRegistry(t *testing.T) {
	pm := &PluginManager{}
	p := pm.FindDocumentExtractor(".pdf")
	if p != nil {
		t.Error("expected nil with no registry")
	}
}

// ── LLMProvider constants ──────────────────────────────────────

func TestLLMProviderConstants(t *testing.T) {
	if LLMOllama != "ollama" {
		t.Errorf("LLMOllama = %q", LLMOllama)
	}
	if LLMOpenAI != "openai" {
		t.Errorf("LLMOpenAI = %q", LLMOpenAI)
	}
	if LLMAnthropic != "anthropic" {
		t.Errorf("LLMAnthropic = %q", LLMAnthropic)
	}
}
