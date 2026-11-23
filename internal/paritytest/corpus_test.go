package paritytest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/quality"
	"github.com/tevfik/gleann/pkg/gleann"
)

// corpusQueries maps each corpus index to a query whose words appear only
// in that index, and the words of which the top passage must contain at
// least one (the code chunker splits files per function, so which chunk
// ranks first is a ranking outcome, not a fixture guarantee).
var corpusQueries = []struct {
	index   string
	query   string
	wantAny []string
}{
	{"orchard", "harvest apples", []string{"apple"}},
	{"harbor", "lighthouse keeper", []string{"lighthouse"}},
	{"observatory", "telescope nebula", []string{"telescope", "nebula"}},
}

func TestCorpusDisjointPassages(t *testing.T) {
	fx := New(t)
	passages := map[string][]string{}
	for _, idx := range fx.Indexes {
		s := fx.Searcher(t, idx.Name)
		all := s.PassageManager().All()
		if len(all) == 0 {
			t.Fatalf("index %q has no passages", idx.Name)
		}
		for _, p := range all {
			passages[idx.Name] = append(passages[idx.Name], p.Text)
		}
	}
	for i, a := range fx.Indexes {
		for _, b := range fx.Indexes[i+1:] {
			set := map[string]bool{}
			for _, text := range passages[a.Name] {
				set[text] = true
			}
			for _, text := range passages[b.Name] {
				if set[text] {
					t.Errorf("indexes %q and %q share passage %q", a.Name, b.Name, text)
				}
			}
		}
	}
}

func TestBuildUsesFakeAndHashModel(t *testing.T) {
	fx := New(t)
	if fx.Fake.Count("/api/embed") == 0 {
		t.Fatal("the build did not reach the fake's /api/embed")
	}
	for _, idx := range fx.Indexes {
		s := fx.Searcher(t, idx.Name)
		if !s.VectorAvailable() {
			t.Errorf("index %q: vector index not available", idx.Name)
		}
		meta := s.Meta()
		if meta.EmbeddingModel != (quality.HashEmbedder{}).ModelName() {
			t.Errorf("index %q: model = %q, want %q", idx.Name, meta.EmbeddingModel, (quality.HashEmbedder{}).ModelName())
		}
		if meta.Dimensions != quality.EmbedDims {
			t.Errorf("index %q: dimensions = %d, want %d", idx.Name, meta.Dimensions, quality.EmbedDims)
		}
	}
}

func TestSearchReturnsRightIndex(t *testing.T) {
	fx := New(t)
	ctx := context.Background()
	for _, tc := range corpusQueries {
		s := fx.Searcher(t, tc.index)
		results, err := s.Search(ctx, tc.query, gleann.WithTopK(3))
		if err != nil {
			t.Fatalf("index %q: search %q: %v", tc.index, tc.query, err)
		}
		if len(results) == 0 {
			t.Fatalf("index %q: no results for %q", tc.index, tc.query)
		}
		top := results[0]
		found := false
		for _, w := range tc.wantAny {
			if strings.Contains(strings.ToLower(top.Text), w) {
				found = true
			}
		}
		if !found {
			t.Errorf("index %q: top passage for %q contains none of %v:\n%s", tc.index, tc.query, tc.wantAny, top.Text)
		}
		src, _ := top.Metadata["source"].(string)
		if src == "" {
			src, _ = top.Metadata["file"].(string)
		}
		known := false
		for _, idx := range fx.Indexes {
			if idx.Name != tc.index {
				continue
			}
			for _, d := range idx.Docs {
				if src == d.Path || strings.HasSuffix(src, "/"+d.Path) {
					known = true
				}
			}
		}
		if !known {
			t.Errorf("index %q: top passage source %q is not one of the index's docs", tc.index, src)
		}
	}
}

// TestHTTPAndDirectSearchersAgree is the reproducibility guarantee of the
// fixtures: an index built through the fake and searched over HTTP gives
// bit-identical results to the same index searched with the in-process
// HashEmbedder, because the fake serves quality.EmbedText.
func TestHTTPAndDirectSearchersAgree(t *testing.T) {
	fx := New(t)
	ctx := context.Background()
	for _, idx := range fx.Indexes {
		http := fx.Searcher(t, idx.Name)
		direct := fx.DirectSearcher(t, idx.Name)
		for _, q := range []string{"harvest apples", "lighthouse keeper", "telescope nebula", "PruneBranches"} {
			a, err := http.Search(ctx, q, gleann.WithTopK(5))
			if err != nil {
				t.Fatalf("index %q: http search %q: %v", idx.Name, q, err)
			}
			b, err := direct.Search(ctx, q, gleann.WithTopK(5))
			if err != nil {
				t.Fatalf("index %q: direct search %q: %v", idx.Name, q, err)
			}
			if len(a) != len(b) {
				t.Fatalf("index %q query %q: %d http results vs %d direct", idx.Name, q, len(a), len(b))
			}
			for i := range a {
				if a[i].ID != b[i].ID || a[i].Score != b[i].Score || a[i].Text != b[i].Text {
					t.Errorf("index %q query %q rank %d: http {%d %f} vs direct {%d %f}",
						idx.Name, q, i, a[i].ID, a[i].Score, b[i].ID, b[i].Score)
				}
			}
		}
	}
}

func TestRerankThroughSearch(t *testing.T) {
	fx := New(t)
	ctx := context.Background()
	s := fx.Searcher(t, "orchard")

	before := fx.Fake.Count("/v1/rerank")
	results, err := s.Search(ctx, "harvest apples", gleann.WithTopK(3),
		gleann.WithCustomReranker(fx.Reranker()), gleann.WithReranker(true))
	if err != nil {
		t.Fatalf("search with rerank: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if fx.Fake.Count("/v1/rerank") != before+1 {
		t.Fatalf("rerank count = %d, want %d", fx.Fake.Count("/v1/rerank"), before+1)
	}
	var req struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}
	if err := json.Unmarshal([]byte(fx.Fake.LastBody("/v1/rerank")), &req); err != nil {
		t.Fatalf("rerank request body: %v", err)
	}
	if req.Query != "harvest apples" {
		t.Errorf("rerank query = %q", req.Query)
	}
	if len(req.Documents) == 0 {
		t.Error("the rerank stub received no candidates")
	}
	if !results[0].Reranked {
		t.Error("top result not marked reranked")
	}
}

func TestAskStreamThroughFake(t *testing.T) {
	fx := New(t)
	ctx := context.Background()
	s := fx.Searcher(t, "orchard")

	ollama := gleann.NewChat(s, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "test-chat", BaseURL: fx.Fake.URL})
	var sb strings.Builder
	results, err := ollama.AskStream(ctx, "harvest apples", func(tok string) { sb.WriteString(tok) })
	if err != nil {
		t.Fatalf("ollama AskStream: %v", err)
	}
	if len(results) == 0 {
		t.Error("AskStream retrieved nothing")
	}
	if sb.String() != "The answer is forty-two." {
		t.Errorf("ollama stream = %q", sb.String())
	}
	if fx.Fake.Count("/api/chat") != 1 {
		t.Errorf("/api/chat count = %d, want 1", fx.Fake.Count("/api/chat"))
	}

	openai := gleann.NewChat(s, gleann.ChatConfig{Provider: gleann.LLMOpenAI, Model: "test-chat", BaseURL: fx.Fake.URL, APIKey: "sk"})
	sb.Reset()
	if _, err := openai.AskStream(ctx, "harvest apples", func(tok string) { sb.WriteString(tok) }); err != nil {
		t.Fatalf("openai AskStream: %v", err)
	}
	if sb.String() != "The answer is forty-two." {
		t.Errorf("openai stream = %q", sb.String())
	}
	if fx.Fake.Count("/v1/chat/completions") != 1 {
		t.Errorf("/v1/chat/completions count = %d, want 1", fx.Fake.Count("/v1/chat/completions"))
	}
	if auth := fx.Fake.LastHeader("/v1/chat/completions").Get("Authorization"); auth != "Bearer sk" {
		t.Errorf("Authorization = %q, want Bearer sk", auth)
	}
}
