package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

const graphToolsIndex = "gq"

func gqSym(fqn, kind, file string, line int64, isTest bool) graphstore.Symbol {
	return graphstore.Symbol{
		FQN: fqn, Kind: kind, File: file, Name: fqn[strings.LastIndex(fqn, ".")+1:],
		StartLine: line, EndLine: line + 3, StartCol: 1, ByteOffset: line * 30, IsTest: isTest,
	}
}

// newGraphQueryServer builds a format-2 index with a few passages, writes a
// hand-made code graph and document hierarchy into its SQLite database and
// returns a server that loads it. The graph has:
//
//	pkg.Hub        called by 30 production callers and 12 test callers,
//	               calls 30 callees; its callers live in core and vendored files
//	pkg.Leaf       called by pkg.Hub only (one-hop chain Leaf <- Hub <- callers)
//	pkg.Lonely     no edges at all
//
// and one document docs/guide.md (folder docs) with nested headings.
func newGraphQueryServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	embedder := &testBudgetEmbedder{dim: 4}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"
	builder, err := gleann.NewBuilder(glCfg, embedder)
	if err != nil {
		t.Fatal(err)
	}
	passages := []gleann.Item{
		{Text: "func Hub() {}", Metadata: map[string]any{"source": "hub.go", "kind": "code"}},
		{Text: "guide text", Metadata: map[string]any{"source": "docs/guide.md"}},
	}
	if err := builder.Build(context.Background(), graphToolsIndex, passages); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, graphToolsIndex, graphToolsIndex+".db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := graphstore.New(st)
	ctx := context.Background()

	hubDelta := graphstore.FileDelta{File: "core/hub.go"}
	hubDelta.Symbols = append(hubDelta.Symbols,
		gqSym("pkg.Hub", "function", "core/hub.go", 10, false),
		gqSym("pkg.Leaf", "function", "core/hub.go", 40, false),
		gqSym("pkg.Lonely", "function", "core/hub.go", 60, false))
	hubDelta.Edges = append(hubDelta.Edges, graphstore.Edge{Src: "pkg.Hub", Dst: "pkg.Leaf"})
	for i := 0; i < 30; i++ {
		callee := fmt.Sprintf("pkg.Callee%02d", i)
		hubDelta.Symbols = append(hubDelta.Symbols, gqSym(callee, "function", "core/hub.go", int64(100+i*5), false))
		hubDelta.Edges = append(hubDelta.Edges, graphstore.Edge{Src: "pkg.Hub", Dst: callee})
	}
	deltas := []graphstore.FileDelta{hubDelta}

	callers := graphstore.FileDelta{File: "core/callers.go"}
	for i := 0; i < 30; i++ {
		c := fmt.Sprintf("pkg.Caller%02d", i)
		callers.Symbols = append(callers.Symbols, gqSym(c, "function", "core/callers.go", int64(1+i*5), false))
		callers.Edges = append(callers.Edges, graphstore.Edge{Src: c, Dst: "pkg.Hub"})
	}
	deltas = append(deltas, callers)

	tests := graphstore.FileDelta{File: "core/hub_test.go"}
	for i := 0; i < 12; i++ {
		c := fmt.Sprintf("pkg.TestHub%02d", i)
		tests.Symbols = append(tests.Symbols, gqSym(c, "function", "core/hub_test.go", int64(1+i*5), true))
		tests.Edges = append(tests.Edges, graphstore.Edge{Src: c, Dst: "pkg.Hub"})
	}
	deltas = append(deltas, tests)

	// A transitive caller that lives in a vendored (noise) path.
	vendored := graphstore.FileDelta{
		File:    "/repo/vendor/lib/use.go",
		Symbols: []graphstore.Symbol{gqSym("lib.UseHub", "function", "/repo/vendor/lib/use.go", 3, false)},
		Edges:   []graphstore.Edge{{Src: "lib.UseHub", Dst: "pkg.Caller00"}},
	}
	deltas = append(deltas, vendored)

	for _, d := range deltas {
		if err := g.ApplyFileDelta(ctx, d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	// The graph this fixture stands for was written by this binary, so its
	// health record matches (graph_query_tools_test.go's stale-graph case
	// rewrites the stored version).
	if err := g.RecordGraphHealth(ctx, graphstore.ResolverExact, ""); err != nil {
		t.Fatalf("RecordGraphHealth: %v", err)
	}

	err = g.ApplyDocDelta(ctx, graphstore.DocDelta{
		RPath: "docs/guide.md",
		Nodes: []graphstore.DocNode{
			{ID: "f1", Type: "folder", Name: "docs"},
			{ID: "d1", Type: "document", Parent: "f1", Name: "Guide", RPath: "docs/guide.md", Summary: "how it works"},
			{ID: "h1", Type: "heading", Parent: "d1", Name: "Intro", Level: 1},
			{ID: "h2", Type: "heading", Parent: "h1", Name: "Background", Level: 2},
			{ID: "c1", Type: "chunk", Parent: "d1", Text: "the full guide text", StartChar: 0, EndChar: 19},
		},
		Explains: []graphstore.DocExplains{{ChunkID: "c1", FQN: "pkg.Hub"}},
	})
	if err != nil {
		t.Fatalf("ApplyDocDelta: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	s := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom", EmbeddingModel: "test-model"})
	s.embedder = embedder
	t.Cleanup(s.Close)
	searcher := gleann.NewSearcher(glCfg, embedder)
	if err := searcher.Load(ctx, graphToolsIndex); err != nil {
		t.Fatal(err)
	}
	if searcher.GraphDB() == nil {
		t.Fatal("expected the searcher to expose the SQLite graph")
	}
	s.searcherMu.Lock()
	s.searchers[graphToolsIndex] = searcher
	s.searcherMu.Unlock()
	return s
}

func TestGraphNeighbors_TruncatesAndSplitsTestCallers(t *testing.T) {
	s := newGraphQueryServer(t)
	out := callTool(t, s.handleGraphNeighbors, map[string]interface{}{"index": graphToolsIndex, "node_fqn": "pkg.Hub"})
	for _, want := range []string{
		"Graph Neighbors for pkg.Hub",
		"=== Callers (30 production, 12 test) ===",
		"... and 5 more production callers", // 30 > 25
		"Test Callers (12 total):",
		"... and 2 more test callers", // 12 > 10
		"[test] pkg.TestHub00 (function) [core/hub_test.go:1]",
		"=== Callees (Symbols this node calls - 31 total) ===",
		"... and 6 more callees",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Tip:") {
		t.Error("no hint expected when relationships exist")
	}
}

func TestGraphNeighbors_NoRelationshipsSuggestsSimilarSymbols(t *testing.T) {
	s := newGraphQueryServer(t)
	out := callTool(t, s.handleGraphNeighbors, map[string]interface{}{"index": graphToolsIndex, "node_fqn": "Lonely"})
	if !strings.Contains(out, "No production callers found.") || !strings.Contains(out, "None found.") {
		t.Errorf("expected empty sections:\n%s", out)
	}
	if !strings.Contains(out, "Did you mean one of these symbols?") || !strings.Contains(out, "pkg.Lonely (function)") {
		t.Errorf("expected a suggestion for pkg.Lonely:\n%s", out)
	}
}

func TestGraphTools_ArgumentValidation(t *testing.T) {
	s := newGraphQueryServer(t)
	cases := []struct {
		name string
		h    toolHandler
		args map[string]interface{}
		want string
	}{
		{"neighbors missing fqn", s.handleGraphNeighbors, map[string]interface{}{"index": graphToolsIndex}, "index and node_fqn are required"},
		{"impact missing symbol", s.handleImpact, map[string]interface{}{"index": graphToolsIndex}, "index and symbol are required"},
		{"stats missing index", s.handleGraphStats, map[string]interface{}{}, "index is required"},
		{"symbols missing file", s.handleSymbolsInFile, map[string]interface{}{"index": graphToolsIndex}, "index and file_path are required"},
		{"links missing doc", s.handleDocumentLinks, map[string]interface{}{"index": graphToolsIndex}, "index and doc_path are required"},
		{"toc missing index", s.handleDocumentTOC, map[string]interface{}{}, "index is required"},
		{"full doc missing vpath", s.handleReadFullDocument, map[string]interface{}{"index": graphToolsIndex}, "index and vpath are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := callToolResult(t, tc.h, tc.args)
			if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), tc.want) {
				t.Errorf("want error containing %q, got error=%v %v", tc.want, res.IsError, res.Content)
			}
		})
	}
	// Non-map arguments are rejected by every handler.
	for name, h := range map[string]toolHandler{
		"neighbors": s.handleGraphNeighbors, "impact": s.handleImpact, "stats": s.handleGraphStats,
		"symbols": s.handleSymbolsInFile, "links": s.handleDocumentLinks, "toc": s.handleDocumentTOC,
	} {
		var req = mkCallArgs(nil)
		req.Params.Arguments = "not a map"
		res, err := h(context.Background(), req)
		if err != nil || !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "invalid arguments format") {
			t.Errorf("%s: want invalid-arguments error, got %v / %v", name, res, err)
		}
	}
}

func TestGraphTools_UnknownIndexIsToolError(t *testing.T) {
	s := newGraphQueryServer(t)
	for name, h := range map[string]toolHandler{
		"neighbors": s.handleGraphNeighbors, "impact": s.handleImpact, "stats": s.handleGraphStats,
		"symbols": s.handleSymbolsInFile, "links": s.handleDocumentLinks, "toc": s.handleDocumentTOC,
	} {
		res := callToolResult(t, h, map[string]interface{}{
			"index": "does-not-exist", "node_fqn": "x", "symbol": "x", "file_path": "x", "doc_path": "x",
		})
		if !res.IsError {
			t.Errorf("%s: unknown index must be a tool error, got %v", name, res.Content)
		}
	}
}

func TestImpact_OrdersNoiseLastAndTruncates(t *testing.T) {
	s := newGraphQueryServer(t)
	out := callTool(t, s.handleImpact, map[string]interface{}{"index": graphToolsIndex, "symbol": "pkg.Hub", "max_depth": 3.0})
	for _, want := range []string{
		"Impact Analysis for pkg.Hub (depth: 3)",
		"=== Direct Callers (30 total) ===",
		"... and 10 more direct callers",
		"=== Transitive Callers (1 total) ===",
		"- lib.UseHub",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "(2 total: 1 core, 1 submodules/tests)") {
		t.Errorf("affected files must split core vs noise:\n%s", out)
	}
	if strings.Index(out, "- core/callers.go") > strings.Index(out, "- /repo/vendor/lib/use.go") {
		t.Errorf("core files must precede vendored files:\n%s", out)
	}
}

func TestImpact_NoCallersSuggestsSymbols(t *testing.T) {
	s := newGraphQueryServer(t)
	out := callTool(t, s.handleImpact, map[string]interface{}{"index": graphToolsIndex, "symbol": "Lonely"})
	if !strings.Contains(out, "Total blast radius: 0 affected symbols across 0 files") {
		t.Errorf("unexpected totals:\n%s", out)
	}
	if !strings.Contains(out, "Tip:") || !strings.Contains(out, "pkg.Lonely (function)") {
		t.Errorf("expected a tip naming pkg.Lonely:\n%s", out)
	}
}

func TestGraphStatsAndSymbolsInFile(t *testing.T) {
	s := newGraphQueryServer(t)
	stats := callTool(t, s.handleGraphStats, map[string]interface{}{"index": graphToolsIndex})
	for _, want := range []string{"Graph Statistics — gq", "Symbols:         76", "Call edges:      74"} {
		if !strings.Contains(stats, want) {
			t.Errorf("stats missing %q:\n%s", want, stats)
		}
	}

	// Graph health (T3): how the graph was built, and the rebuild hint when
	// the stored graph extractor is older than this binary.
	for _, want := range []string{"Graph extractor:", "Resolver:", "Tree-sitter:", "Built at:"} {
		if !strings.Contains(stats, want) {
			t.Errorf("stats miss graph health %q:\n%s", want, stats)
		}
	}
	if strings.Contains(stats, "gleann graph build") {
		t.Errorf("a graph built by this binary must not be reported stale:\n%s", stats)
	}
	if !strings.Contains(stats, "CALLS/") && !strings.Contains(stats, "Edges by confidence") {
		t.Errorf("stats must break edges down by confidence:\n%s", stats)
	}

	syms := callTool(t, s.handleSymbolsInFile, map[string]interface{}{"index": graphToolsIndex, "file_path": "core/hub.go"})
	if !strings.Contains(syms, "Symbols in core/hub.go (33 found)") || !strings.Contains(syms, "[function] pkg.Lonely") {
		t.Errorf("unexpected symbol listing:\n%s", syms)
	}

	none := callTool(t, s.handleSymbolsInFile, map[string]interface{}{"index": graphToolsIndex, "file_path": "nope.go"})
	if !strings.Contains(none, `No symbols found in file "nope.go" (index: gq)`) {
		t.Errorf("unexpected empty answer: %s", none)
	}
}

func TestDocumentTOC_ListOutlineAndJSON(t *testing.T) {
	s := newGraphQueryServer(t)

	list := callTool(t, s.handleDocumentTOC, map[string]interface{}{"index": graphToolsIndex})
	if !strings.Contains(list, "Indexed Documents in \"gq\" (1 total)") ||
		!strings.Contains(list, "**docs/guide.md** (2 headings) [folder: docs]") ||
		!strings.Contains(list, "*Summary:* how it works") {
		t.Errorf("unexpected document list:\n%s", list)
	}
	listJSON := callTool(t, s.handleDocumentTOC, map[string]interface{}{"index": graphToolsIndex, "format": "json"})
	if !strings.Contains(listJSON, `"docs/guide.md"`) {
		t.Errorf("json list lacks the document:\n%s", listJSON)
	}

	outline := callTool(t, s.handleDocumentTOC, map[string]interface{}{"index": graphToolsIndex, "vpath": "docs/guide.md"})
	for _, want := range []string{
		"# Document: Guide", "- **Path:** `docs/guide.md`", "- **Folder:** `docs`",
		"- **Summary:** how it works", "- **Total Headings:** 2",
		"- [H1] Intro\n  - [H2] Background\n",
	} {
		if !strings.Contains(outline, want) {
			t.Errorf("outline missing %q:\n%s", want, outline)
		}
	}
	tocJSON := callTool(t, s.handleDocumentTOC, map[string]interface{}{"index": graphToolsIndex, "vpath": "docs/guide.md", "format": "json"})
	if !strings.Contains(tocJSON, `"Background"`) {
		t.Errorf("json TOC lacks the nested heading:\n%s", tocJSON)
	}

	res := callToolResult(t, s.handleDocumentTOC, map[string]interface{}{"index": graphToolsIndex, "vpath": "docs/missing.md"})
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "not found in graph index") {
		t.Errorf("missing document must be a tool error, got %v", res.Content)
	}
}

func TestFormatHeadingsOutline_Nesting(t *testing.T) {
	var sb strings.Builder
	formatHeadingsOutline(&sb, []gleann.DocumentHeadingItem{
		{Level: 1, Title: "A", Children: []gleann.DocumentHeadingItem{{Level: 2, Title: "A1", Children: []gleann.DocumentHeadingItem{{Level: 3, Title: "A1x"}}}}},
		{Level: 1, Title: "B"},
	}, 0)
	want := "- [H1] A\n  - [H2] A1\n    - [H3] A1x\n- [H1] B\n"
	if sb.String() != want {
		t.Errorf("outline = %q, want %q", sb.String(), want)
	}
}

func TestDocumentLinks_ListsExplainedSymbols(t *testing.T) {
	s := newGraphQueryServer(t)
	out := callTool(t, s.handleDocumentLinks, map[string]interface{}{"index": graphToolsIndex, "doc_path": "docs/guide.md"})
	if !strings.Contains(out, "Symbols explained by document docs/guide.md") || !strings.Contains(out, "- pkg.Hub (function) [File: core/hub.go]") {
		t.Errorf("unexpected links output:\n%s", out)
	}
	res := callToolResult(t, s.handleDocumentLinks, map[string]interface{}{"index": graphToolsIndex, "doc_path": "docs/other.md"})
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "document not found") {
		t.Errorf("an unknown document must be a tool error, got %v", res.Content)
	}
}

func TestReadFullDocument_GraphThenDiskThenNotFound(t *testing.T) {
	s := newGraphQueryServer(t)
	out := callTool(t, s.handleReadFullDocument, map[string]interface{}{"index": graphToolsIndex, "vpath": "docs/guide.md"})
	if !strings.Contains(out, "the full guide text") {
		t.Errorf("document text must come from the graph, got %q", out)
	}
	res := callToolResult(t, s.handleReadFullDocument, map[string]interface{}{"index": graphToolsIndex, "vpath": "docs/missing.md"})
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "not found in index") {
		t.Errorf("want not-found tool error, got %v", res.Content)
	}
	// An unloadable index is reported through indexBusyError, not as a panic.
	res = callToolResult(t, s.handleReadFullDocument, map[string]interface{}{"index": "nope", "vpath": "x.md"})
	if !res.IsError {
		t.Errorf("unknown index must be an error, got %v", res.Content)
	}
}

func TestImpact_NonGoNotice(t *testing.T) {
	env := analysisgolden.BuildIndexFormat2(t)
	s := NewServer(Config{IndexDir: env.IndexDir})
	// Close, not closeGraphPool: impact and neighbors borrow a cached searcher that
	// also holds the index db (Windows cannot delete an open file).
	t.Cleanup(s.Close)

	out := callTool(t, s.handleImpact, map[string]interface{}{
		"index":  analysisgolden.IndexName,
		"symbol": "example.com/fixture/py.clean",
	})
	wantNotice := "note: non-Go code was extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"
	if chunking.TreeSitterAvailable() {
		if strings.Contains(out, "without tree-sitter") {
			t.Errorf("treesitter build must not include notice, got:\n%s", out)
		}
	} else {
		if !strings.Contains(out, wantNotice) {
			t.Errorf("impact on non-Go symbol missing notice in:\n%s", out)
		}
	}

	// Impact on Go symbol must not include notice.
	outGo := callTool(t, s.handleImpact, map[string]interface{}{
		"index":  analysisgolden.IndexName,
		"symbol": "example.com/fixture/service.Service.Load",
	})
	if strings.Contains(outGo, "without tree-sitter") {
		t.Errorf("Go symbol impact must not include notice, got:\n%s", outGo)
	}
}

func TestImpact_ShortNameClean_NoNoticeOnGo(t *testing.T) {
	docsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsDir, "go.mod"), []byte("module example.com/m\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "a.py"), []byte("def clean():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docsDir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "svc", "z.go"), []byte("package svc\nfunc Run() { clean() }\nfunc clean() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexDir := t.TempDir()
	idxPath := filepath.Join(indexDir, "zz2")
	if err := os.MkdirAll(idxPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxPath, "zz2.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var outBuf bytes.Buffer
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:     "zz2",
		DocsDir:  docsDir,
		IndexDir: indexDir,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	})

	s := NewServer(Config{IndexDir: indexDir})
	t.Cleanup(s.Close)

	out := callTool(t, s.handleImpact, map[string]interface{}{
		"index":  "zz2",
		"symbol": "clean",
	})
	if strings.Contains(out, "without tree-sitter") {
		t.Fatalf("BUG: impact of Go symbol svc.clean carrying non-Go notice:\n%s", out)
	}
}

func TestNeighbors_NonGoNotice(t *testing.T) {
	env := analysisgolden.BuildIndexFormat2(t)
	s := NewServer(Config{IndexDir: env.IndexDir})
	t.Cleanup(s.Close)

	out := callTool(t, s.handleGraphNeighbors, map[string]interface{}{
		"index":    analysisgolden.IndexName,
		"node_fqn": "example.com/fixture/py.clean",
	})
	wantNotice := "note: non-Go code was extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"
	if chunking.TreeSitterAvailable() {
		if strings.Contains(out, "without tree-sitter") {
			t.Errorf("treesitter build must not include notice, got:\n%s", out)
		}
	} else {
		if !strings.Contains(out, wantNotice) {
			t.Errorf("neighbors on non-Go symbol missing notice in:\n%s", out)
		}
	}

	// Go symbol must not include notice.
	outGo := callTool(t, s.handleGraphNeighbors, map[string]interface{}{
		"index":    analysisgolden.IndexName,
		"node_fqn": "example.com/fixture/service.Service.Load",
	})
	if strings.Contains(outGo, "without tree-sitter") {
		t.Errorf("Go symbol neighbors must not include notice, got:\n%s", outGo)
	}
}

// TestNeighbors_CalleeOnlyGoShortName_NoNotice pins the direction-aware notice
// of gleann_graph_neighbors: it answers BOTH callers and callees, so the
// notice must be resolved over both sides. A Go symbol with outgoing but no
// incoming CALLS edges and a non-Go namesake must not carry the notice
// (v3 review Finding 1, residual E on MCP neighbors).
func TestNeighbors_CalleeOnlyGoShortName_NoNotice(t *testing.T) {
	docsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsDir, "go.mod"), []byte("module example.com/m\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "a.py"), []byte("def clean():\n    pass\n\ndef pyonly():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docsDir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "svc", "z.go"), []byte("package svc\nfunc clean() { helper() }\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexDir := t.TempDir()
	idxPath := filepath.Join(indexDir, "zz3")
	if err := os.MkdirAll(idxPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxPath, "zz3.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var outBuf bytes.Buffer
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:     "zz3",
		DocsDir:  docsDir,
		IndexDir: indexDir,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	})
	if _, err := os.Stat(filepath.Join(idxPath, "zz3.db")); err != nil {
		t.Fatalf("graph index not built: %v\n%s", err, outBuf.String())
	}

	s := NewServer(Config{IndexDir: indexDir})
	t.Cleanup(s.Close)

	out := callTool(t, s.handleGraphNeighbors, map[string]interface{}{
		"index":    "zz3",
		"node_fqn": "clean",
	})
	if !strings.Contains(out, "example.com/m/svc.helper") {
		t.Fatalf("neighbors of clean must list the Go callee svc.helper:\n%s", out)
	}
	if strings.Contains(out, "without tree-sitter") {
		t.Fatalf("BUG: neighbors of the callee-only Go symbol carry a non-Go notice:\n%s", out)
	}

	// A non-Go symbol without a Go namesake still deserves the notice in a
	// lite build: the direction-aware resolution must not silence the real
	// blind spot.
	outPy := callTool(t, s.handleGraphNeighbors, map[string]interface{}{
		"index":    "zz3",
		"node_fqn": "example.com/m.pyonly",
	})
	if chunking.TreeSitterAvailable() {
		if strings.Contains(outPy, "without tree-sitter") {
			t.Fatalf("treesitter build must not include the notice, got:\n%s", outPy)
		}
	} else if !strings.Contains(outPy, indexing.NonGoNoticeMessage) {
		t.Fatalf("neighbors of the non-Go symbol must carry the notice in a lite build:\n%s", outPy)
	}
}
