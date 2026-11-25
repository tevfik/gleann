package mcp

// T7 one_per_file parity: gleann_search, gleann_search_multi and gleann_search_ids
// must all honour an optional boolean one_per_file parameter. The fixture is a
// real index whose chunks come from one source with several matching chunks and
// several single-chunk sources, so de-duplicating by file has an observable
// effect. Everything lives in t.TempDir(); no ~/.gleann, ~/.config or network.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

const opfIndex = "opfinstidx"

// newDupSourceMCPServer builds an isolated MCP server whose index has one
// source ("shared/dup.go") carrying three matching chunks plus seven unique
// single-chunk sources. It returns the server so a test can invoke the real
// registered tool handlers.
func newDupSourceMCPServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	memory.ResetRemoteForTesting()
	dir := t.TempDir()
	embedder := &testBudgetEmbedder{dim: 4}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"

	builder, err := gleann.NewBuilder(glCfg, embedder)
	if err != nil {
		t.Fatalf("new builder: %v", err)
	}
	items := make([]gleann.Item, 0, 10)
	for i := 0; i < 3; i++ {
		items = append(items, gleann.Item{
			Text:     "alpha zeta keyword matching content variant " + string(rune('a'+i)),
			Metadata: map[string]any{"source": "shared/dup.go", "kind": "code"},
		})
	}
	for i := 0; i < 7; i++ {
		items = append(items, gleann.Item{
			Text:     "alpha zeta keyword matching content other " + string(rune('a'+i)),
			Metadata: map[string]any{"source": "uniq/other" + string(rune('0'+i)) + ".go", "kind": "code"},
		})
	}
	if err := builder.Build(context.Background(), opfIndex, items); err != nil {
		t.Fatalf("build index: %v", err)
	}

	s := NewServer(Config{
		IndexDir:          dir,
		EmbeddingProvider: "custom",
		EmbeddingModel:    "test-model",
		LLMProvider:       "ollama",
		OllamaHost:        "http://127.0.0.1:0",
		ToolsProfile:      "full",
	})
	s.embedder = embedder
	t.Cleanup(s.Close)

	searcher := gleann.NewSearcher(glCfg, embedder)
	if err := searcher.Load(context.Background(), opfIndex); err != nil {
		t.Fatalf("load searcher: %v", err)
	}
	s.searcherMu.Lock()
	s.searchers[opfIndex] = searcher
	s.searcherLRU = append(s.searcherLRU, opfIndex)
	s.searcherMu.Unlock()

	// Isolated block memory so the tools never reach the user's store.
	mstore, err := memory.OpenStore(t.TempDir() + "/memory.db")
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	s.blockMem.close()
	s.blockMem = &blockMemPool{mgr: memory.NewManager(mstore)}
	return s
}

func opfCall(t *testing.T, s *Server, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	st := s.mcpServer.GetTool(name)
	if st == nil {
		t.Fatalf("tool %q is not registered", name)
	}
	var req mcpsdk.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: handler error: %v", name, err)
	}
	if res == nil {
		t.Fatalf("%s: nil result", name)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, extractText(res))
	}
	return res
}

// countDupSources counts how many rendered results carry source "shared/dup.go"
// in the plain-text search output (each result prints a "Source:" line).
func countDupSources(text string) int {
	return strings.Count(text, "Source: shared/dup.go")
}

func opfArgs(extra map[string]any) map[string]any {
	a := map[string]any{"index": opfIndex, "query": "alpha zeta keyword", "top_k": 20.0}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestMCP_OnePerFile_gleann_search(t *testing.T) {
	s := newDupSourceMCPServer(t)

	def := opfCall(t, s, "gleann_search", opfArgs(nil))
	if n := countDupSources(extractText(def)); n < 2 {
		t.Fatalf("default search shows dup.go %d time(s), want >=2 (fixture wrong?)", n)
	}

	withOpf := opfCall(t, s, "gleann_search", opfArgs(map[string]any{"one_per_file": true}))
	txt := extractText(withOpf)
	if n := countDupSources(txt); n != 1 {
		t.Fatalf("one_per_file search shows dup.go %d time(s), want exactly 1:\n%s", n, txt)
	}
	// No source may repeat once one_per_file is on.
	for _, src := range []string{"uniq/other0.go", "uniq/other1.go"} {
		if c := strings.Count(txt, "Source: "+src); c > 1 {
			t.Errorf("one_per_file shows %s %d times, want <=1", src, c)
		}
	}
}

func TestMCP_OnePerFile_gleann_search_ids(t *testing.T) {
	s := newDupSourceMCPServer(t)

	decode := func(res *mcpsdk.CallToolResult) []compactResult {
		var out []compactResult
		if err := json.Unmarshal([]byte(extractText(res)), &out); err != nil {
			t.Fatalf("decode search_ids body: %v", err)
		}
		return out
	}
	dupCount := func(rs []compactResult) int {
		n := 0
		for _, r := range rs {
			if r.Source == "shared/dup.go" {
				n++
			}
		}
		return n
	}

	def := decode(opfCall(t, s, "gleann_search_ids", opfArgs(nil)))
	if n := dupCount(def); n < 2 {
		t.Fatalf("default search_ids shows dup.go %d time(s), want >=2", n)
	}
	one := decode(opfCall(t, s, "gleann_search_ids", opfArgs(map[string]any{"one_per_file": true})))
	if n := dupCount(one); n != 1 {
		t.Fatalf("one_per_file search_ids shows dup.go %d time(s), want exactly 1", n)
	}
	if len(one) >= len(def) {
		t.Errorf("one_per_file returned %d ids, want fewer than default %d", len(one), len(def))
	}
}

func TestMCP_OnePerFile_gleann_search_multi(t *testing.T) {
	s := newDupSourceMCPServer(t)
	multiArgs := func(extra map[string]any) map[string]any {
		a := map[string]any{"indexes": opfIndex, "query": "alpha zeta keyword", "top_k": 20.0}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}

	def := opfCall(t, s, "gleann_search_multi", multiArgs(nil))
	if n := strings.Count(extractText(def), ", file: shared/dup.go"); n < 2 {
		t.Fatalf("default search_multi shows dup.go %d time(s), want >=2:\n%s", n, extractText(def))
	}
	withOpf := opfCall(t, s, "gleann_search_multi", multiArgs(map[string]any{"one_per_file": true}))
	if n := strings.Count(extractText(withOpf), ", file: shared/dup.go"); n != 1 {
		t.Fatalf("one_per_file search_multi shows dup.go %d time(s), want exactly 1:\n%s", n, extractText(withOpf))
	}
}
