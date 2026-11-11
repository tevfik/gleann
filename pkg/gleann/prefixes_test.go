package gleann

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// recordingEmbedder remembers every text it embeds.
type recordingEmbedder struct {
	testEmbedder
	model string
	mu    sync.Mutex
	texts []string
}

func (e *recordingEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.texts = append(e.texts, texts...)
	e.mu.Unlock()
	return e.testEmbedder.Compute(ctx, texts)
}

func (e *recordingEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, err := e.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func (e *recordingEmbedder) ModelName() string { return e.model }

func TestEmbeddingPrefixes(t *testing.T) {
	for model, want := range map[string]string{
		"/home/u/.gleann/models/multilingual-e5-small-q8_0.gguf": "passage: |query: ",
		"intfloat/e5-base-v2": "passage: |query: ",
		"bge-m3":              "|",
		"nomic-embed-text":    "|",
	} {
		p, q := EmbeddingPrefixes(Config{}, model)
		if p+"|"+q != want {
			t.Errorf("%s: %q %q", model, p, q)
		}
	}
	if p, q := EmbeddingPrefixes(Config{BuildPromptTemplate: "doc: ", QueryPromptTemplate: "ask: "}, "e5-small"); p != "doc: " || q != "ask: " {
		t.Errorf("configured templates: %q %q", p, q)
	}
}

// A new index embeds its passages with the model's prefix, records both prefixes, and its searches and updates
// use the recorded ones; an index without recorded prefixes keeps embedding without any.
func TestPrefixesRecordedAndUsed(t *testing.T) {
	dir := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)
	emb := &recordingEmbedder{testEmbedder: testEmbedder{dim: 8}, model: "multilingual-e5-small"}
	builder.embedder = emb
	ctx := context.Background()
	items := []Item{{Text: "alpha", Metadata: map[string]any{"source": "a.md"}}, {Text: "beta", Metadata: map[string]any{"source": "b.md"}}}
	if err := builder.Build(ctx, "e5idx", items); err != nil {
		t.Fatal(err)
	}
	for _, txt := range emb.texts {
		if !strings.HasPrefix(txt, "passage: ") {
			t.Errorf("build embedded %q", txt)
		}
	}
	meta, err := GetIndexMeta(dir, "e5idx")
	if err != nil || meta.PassagePrefix != "passage: " || meta.QueryPrefix != "query: " {
		t.Fatalf("meta %+v %v", meta, err)
	}

	emb.texts = nil
	if err := builder.UpdateIndex(ctx, "e5idx", []Item{{Text: "gamma", Metadata: map[string]any{"source": "c.md"}}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, txt := range emb.texts {
		if !strings.HasPrefix(txt, "passage: ") {
			t.Errorf("update embedded %q", txt)
		}
	}

	initTestHNSW()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", SearchConfig: SearchConfig{HybridAlpha: 0.7}}, emb)
	if err := s.Load(ctx, "e5idx"); err != nil {
		t.Fatal(err)
	}
	emb.texts = nil
	if _, err := s.Search(ctx, "where is alpha"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, txt := range emb.texts {
		found = found || txt == "query: where is alpha"
		if strings.HasPrefix(txt, "where is") {
			t.Errorf("query embedded without prefix: %q", txt)
		}
	}
	if !found {
		t.Errorf("query not embedded with its prefix: %q", emb.texts)
	}
	s.Close()

	// An index from before the prefixes: no prefix on updates.
	_ = UpdateIndexMeta(dir, "e5idx", func(m *IndexMeta) { m.PassagePrefix, m.QueryPrefix = "", "" })
	emb.texts = nil
	if err := builder.UpdateIndex(ctx, "e5idx", []Item{{Text: "delta", Metadata: map[string]any{"source": "d.md"}}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, txt := range emb.texts {
		if strings.HasPrefix(txt, "passage: ") {
			t.Errorf("old index updated with a prefix: %q", txt)
		}
	}
}

// prefixRecEmbedder records the texts it embeds and returns unit vectors.
type prefixRecEmbedder struct{ texts []string }

func (e *prefixRecEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	e.texts = append(e.texts, texts...)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func (e *prefixRecEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, err := e.Compute(ctx, []string{text})
	return v[0], err
}
func (e *prefixRecEmbedder) Dimensions() int   { return 2 }
func (e *prefixRecEmbedder) ModelName() string { return "multilingual-e5-small" }

func TestExplainAndAttributeUsePrefixes(t *testing.T) {
	rec := &prefixRecEmbedder{}
	s := &LeannSearcher{embedder: rec, meta: IndexMeta{PassagePrefix: "passage: ", QueryPrefix: "query: "}}
	r := SearchResult{ID: 1, Text: "line one\nline two\nline three\nline four"}
	if _, err := s.ExplainResult(context.Background(), "find it", r, ExplainOptions{}); err != nil {
		t.Fatal(err)
	}
	if rec.texts[0] != "query: find it" {
		t.Fatalf("explain query text = %q", rec.texts[0])
	}
	for _, x := range rec.texts[1:] {
		if !strings.HasPrefix(x, "passage: ") {
			t.Fatalf("explain passage text without prefix: %q", x)
		}
	}
	pp, qp := s.EmbeddingPrefixes()
	rec.texts = nil
	if _, err := AttributeWithPrefixes(context.Background(), rec, qp, pp, "It works.", []SearchResult{r}, 0); err != nil {
		t.Fatal(err)
	}
	if rec.texts[0] != "query: It works." || !strings.HasPrefix(rec.texts[1], "passage: ") {
		t.Fatalf("attribute texts = %q", rec.texts)
	}
	// Attribute keeps embedding without prefixes.
	rec.texts = nil
	if _, err := Attribute(context.Background(), rec, "It works.", []SearchResult{r}, 0); err != nil {
		t.Fatal(err)
	}
	if rec.texts[0] != "It works." {
		t.Fatalf("plain attribute text = %q", rec.texts[0])
	}
}
