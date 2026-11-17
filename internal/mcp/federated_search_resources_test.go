package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

// newFederatedServer builds three real hnsw indexes in one directory:
//
//	alpha   tags work       passages about "kiwi"   (a.go, two chunks)
//	beta    tags docs       passages about "mango"  (b.md)
//	hidden  tags work       mcp_exposed=false       passages about "secret"
//
// alpha and beta are preloaded into the server's searcher cache; hidden is
// not, so any attempt to read it would have to go through acquireSearcher's
// access check.
func newFederatedServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	embedder := &testBudgetEmbedder{dim: 4}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"
	ctx := context.Background()

	type spec struct {
		name     string
		tags     []string
		hidden   bool
		passages []gleann.Item
	}
	specs := []spec{
		{"alpha", []string{"work"}, false, []gleann.Item{
			{Text: "kiwi parser handles tokens first part", Metadata: map[string]any{"source": "a.go", "kind": "code"}},
			{Text: "kiwi parser handles tokens second part", Metadata: map[string]any{"source": "a.go", "kind": "code"}},
		}},
		{"beta", []string{"docs"}, false, []gleann.Item{
			{Text: "mango guide explains deployment", Metadata: map[string]any{"source": "b.md", "kind": "doc"}},
		}},
		{"hidden", []string{"work"}, true, []gleann.Item{
			{Text: "secret credentials rotation notes", Metadata: map[string]any{"source": "s.md"}},
		}},
	}
	s := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom", EmbeddingModel: "test-model"})
	s.embedder = embedder
	t.Cleanup(s.Close)
	for _, sp := range specs {
		b, err := gleann.NewBuilder(glCfg, embedder)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Build(ctx, sp.name, sp.passages); err != nil {
			t.Fatal(err)
		}
		if err := gleann.UpdateIndexMeta(dir, sp.name, func(m *gleann.IndexMeta) {
			m.Tags = sp.tags
			if sp.hidden {
				off := false
				m.MCPExposed = &off
			}
		}); err != nil {
			t.Fatal(err)
		}
		if sp.hidden {
			continue
		}
		sr := gleann.NewSearcher(glCfg, embedder)
		if err := sr.Load(ctx, sp.name); err != nil {
			t.Fatal(err)
		}
		s.searcherMu.Lock()
		s.searchers[sp.name] = sr
		s.searcherLRU = append(s.searcherLRU, sp.name)
		s.searcherMu.Unlock()
	}
	return s
}

func searchText(t *testing.T, s *Server, args map[string]interface{}) string {
	t.Helper()
	return callTool(t, s.handleSearch, args)
}

func TestSearch_FederatedAcrossAllExposedIndexes(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	out := searchText(t, s, map[string]interface{}{"index": "@all", "query": "kiwi mango", "top_k": 10.0})
	if !strings.Contains(out, "kiwi parser") || !strings.Contains(out, "mango guide") {
		t.Errorf("@all must merge both exposed indexes:\n%s", out)
	}
	if strings.Contains(out, "secret credentials") {
		t.Errorf("a private index must never leak into federated results:\n%s", out)
	}
}

func TestSearch_FederatedTagSelectsOnlyMatchingIndexes(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	out := searchText(t, s, map[string]interface{}{"index": "@docs", "query": "kiwi mango", "top_k": 10.0})
	if !strings.Contains(out, "mango guide") || strings.Contains(out, "kiwi parser") {
		t.Errorf("@docs must only search beta:\n%s", out)
	}
	// @work matches alpha and the private index: only alpha is searched.
	out = searchText(t, s, map[string]interface{}{"index": "@work", "query": "kiwi secret", "top_k": 10.0})
	if !strings.Contains(out, "kiwi parser") || strings.Contains(out, "secret credentials") {
		t.Errorf("@work must search alpha but skip the private index:\n%s", out)
	}
}

func TestSearch_FederatedTopKCapsMergedResults(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	out := searchText(t, s, map[string]interface{}{"index": "@all", "query": "kiwi mango", "top_k": 1.0})
	hits := strings.Count(out, "kiwi parser") + strings.Count(out, "mango guide")
	if hits != 1 {
		t.Errorf("top_k=1 must keep one merged result, got %d:\n%s", hits, out)
	}
}

func TestSearch_FederatedNoMatchMessages(t *testing.T) {
	t.Run("unknown tag", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "")
		s := newFederatedServer(t)
		out := searchText(t, s, map[string]interface{}{"index": "@nonesuch", "query": "x"})
		if !strings.HasPrefix(out, "No indexes found.") {
			t.Errorf("unexpected message: %s", out)
		}
	})
	t.Run("server tag filter excludes the tag", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "docs")
		s := newFederatedServer(t)
		out := searchText(t, s, map[string]interface{}{"index": "@work", "query": "x"})
		if out != `No accessible indexes found with tag "work".` {
			t.Errorf("unexpected message: %s", out)
		}
	})
	t.Run("server tag filter narrows @all", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "docs")
		s := newFederatedServer(t)
		out := searchText(t, s, map[string]interface{}{"index": "@all", "query": "kiwi mango", "top_k": 10.0})
		if !strings.Contains(out, "mango guide") || strings.Contains(out, "kiwi parser") {
			t.Errorf("GLEANN_TAGS=docs must restrict @all to beta:\n%s", out)
		}
	})
}

func TestSearch_AutoResolvesToFederatedWhenSeveralIndexesExposed(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	t.Setenv("GLEANN_INDEX", "")
	s := newFederatedServer(t)
	// No index given and two exposed indexes: the search is federated.
	out := searchText(t, s, map[string]interface{}{"query": "kiwi mango", "top_k": 10.0})
	if !strings.Contains(out, "kiwi parser") || !strings.Contains(out, "mango guide") {
		t.Errorf("expected results from both indexes:\n%s", out)
	}
}

func TestSearch_SingleIndexOptions(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"graph context and focus", map[string]interface{}{"graph_context": true, "focus": true}, "kiwi parser"},
		{"kind filter", map[string]interface{}{"kind": "code", "include_tests": false}, "kiwi parser"},
		{"metadata filter", map[string]interface{}{"filters": []interface{}{map[string]interface{}{"field": "source", "operator": "eq", "value": "a.go"}}, "filter_logic": "and"}, "kiwi parser"},
		// A tiny budget degrades results to locators and reports it.
		{"token budget", map[string]interface{}{"max_tokens": 50.0}, "[Token Budget:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{"index": "alpha", "query": "kiwi parser tokens", "top_k": 5.0}
			for k, v := range tc.args {
				args[k] = v
			}
			out := searchText(t, s, args)
			if !strings.Contains(out, tc.want) || !strings.Contains(out, "a.go") {
				t.Errorf("expected %q and the alpha source in the output:\n%s", tc.want, out)
			}
			if strings.Contains(out, "mango guide") {
				t.Errorf("a single-index search must not leak other indexes:\n%s", out)
			}
		})
	}
}

func TestSearch_PrivateIndexIsDenied(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	res := callToolResult(t, s.handleSearch, map[string]interface{}{"index": "hidden", "query": "secret"})
	if !res.IsError || !strings.Contains(textOf(res), "access denied") {
		t.Errorf("want access denied, got %v", res.Content)
	}
}

func textOf(res *mcpsdk.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestDedupStrings(t *testing.T) {
	got := dedupStrings([]string{"a", "b", "a", "c", "b", "a"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("dedupStrings = %v, want [a b c] in first-seen order", got)
	}
	if got := dedupStrings(nil); len(got) != 0 {
		t.Errorf("dedupStrings(nil) = %v", got)
	}
}

func TestFocusResults_KeepsTextWhenIndexCannotBeLoaded(t *testing.T) {
	s := newFederatedServer(t)
	in := []gleann.SearchResult{
		{ID: 1, Text: "original text", Metadata: map[string]any{"_index": "no-such-index"}},
		{ID: 2, Text: "private text", Metadata: map[string]any{"_index": "hidden"}},
	}
	out := s.focusResults(context.Background(), "alpha", "q", in)
	if len(out) != 2 || out[0].Text != "original text" || out[1].Text != "private text" {
		t.Errorf("unexplainable results must keep their text, got %+v", out)
	}
	if &out[0] == &in[0] {
		t.Error("focusResults must return a copy, not alias the input")
	}
}

func TestIndexListResource_RespectsExposureAndTags(t *testing.T) {
	read := func(t *testing.T, s *Server) string {
		t.Helper()
		var req mcpsdk.ReadResourceRequest
		req.Params.URI = "gleann://indexes"
		contents, err := s.handleIndexListResource(context.Background(), req)
		if err != nil || len(contents) != 1 {
			t.Fatalf("handleIndexListResource: %v %v", contents, err)
		}
		tc := contents[0].(mcpsdk.TextResourceContents)
		if tc.URI != "gleann://indexes" || tc.MIMEType != "text/plain" {
			t.Errorf("unexpected resource envelope: %+v", tc)
		}
		return tc.Text
	}
	t.Run("all exposed", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "")
		txt := read(t, newFederatedServer(t))
		if !strings.Contains(txt, "- alpha:") || !strings.Contains(txt, "tags=[work]") || !strings.Contains(txt, "- beta:") {
			t.Errorf("missing exposed indexes:\n%s", txt)
		}
		if strings.Contains(txt, "hidden") {
			t.Errorf("private index listed:\n%s", txt)
		}
	})
	t.Run("tag filter", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "docs")
		txt := read(t, newFederatedServer(t))
		if strings.Contains(txt, "alpha") || !strings.Contains(txt, "beta") {
			t.Errorf("GLEANN_TAGS=docs must list only beta:\n%s", txt)
		}
	})
	t.Run("nothing matches", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "nonesuch")
		txt := read(t, newFederatedServer(t))
		if !strings.Contains(txt, "No exposed indexes found.") {
			t.Errorf("unexpected text:\n%s", txt)
		}
	})
}

func TestReadResource(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	read := func(uri string) ([]mcpsdk.ResourceContents, error) {
		var req mcpsdk.ReadResourceRequest
		req.Params.URI = uri
		return s.handleReadResource(context.Background(), req)
	}

	contents, err := read("gleann://alpha/a.go")
	if err != nil {
		t.Fatalf("read a.go: %v", err)
	}
	txt := contents[0].(mcpsdk.TextResourceContents).Text
	if !strings.Contains(txt, "first part") || !strings.Contains(txt, "second part") {
		t.Errorf("all chunks of the file must be concatenated, got %q", txt)
	}

	for name, tc := range map[string]struct{ uri, want string }{
		"wrong scheme":     {"http://alpha/a.go", "invalid URI scheme"},
		"no file part":     {"gleann://alpha", "invalid URI format"},
		"unknown file":     {"gleann://alpha/zzz.go", "not found in index"},
		"unknown index":    {"gleann://nope/a.go", "failed to load index"},
		"private index":    {"gleann://hidden/s.md", "access denied"},
		"invalid index id": {"gleann://../a.go", "failed to load index"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := read(tc.uri)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("read(%q): want error containing %q, got %v", tc.uri, tc.want, err)
			}
		})
	}
}
