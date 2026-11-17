package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2Graph is an analysisGraph with canned data and injectable failures.
type h2Graph struct {
	call, full                                 *community.Graph
	callErr, fullErr, commErr, countErr, inErr error
	declErr                                    error
	count                                      int
	in                                         []graphstore.InDegree
	decls                                      []graphstore.Symbol
	db                                         gleann.GraphDB
	inCalls                                    [][]string
	closed                                     bool
}

func (g *h2Graph) CallGraph() (*community.Graph, error) {
	if g.call == nil && g.callErr == nil {
		return community.NewGraph(), nil
	}
	return g.call, g.callErr
}
func (g *h2Graph) FullGraph() (*community.Graph, error) {
	if g.full == nil && g.fullErr == nil {
		return community.NewGraph(), nil
	}
	return g.full, g.fullErr
}
func (g *h2Graph) Communities(int, int) (*community.Result, error) {
	return &community.Result{}, g.commErr
}
func (g *h2Graph) SymbolCount() (int, error) { return g.count, g.countErr }
func (g *h2Graph) InDegreeTop(n int, excludeKinds ...string) ([]graphstore.InDegree, error) {
	g.inCalls = append(g.inCalls, excludeKinds)
	if g.inErr != nil {
		return nil, g.inErr
	}
	if n < len(g.in) {
		return g.in[:n], nil
	}
	return g.in, nil
}
func (g *h2Graph) Declarations(limit int, excludeKinds ...string) ([]graphstore.Symbol, error) {
	return g.decls, g.declErr
}
func (g *h2Graph) DB() gleann.GraphDB { return g.db }
func (g *h2Graph) Close()             { g.closed = true }

// h2GraphDB fakes the GraphDB calls navigate makes; other methods panic.
type h2GraphDB struct {
	gleann.GraphDB
	search    []gleann.Callee
	searchErr error
	callers   map[string][]gleann.Callee
	callees   map[string][]gleann.Callee
}

func (d *h2GraphDB) SymbolSearch(string) ([]gleann.Callee, error) { return d.search, d.searchErr }
func (d *h2GraphDB) Callers(fqn string) ([]gleann.Callee, error)  { return d.callers[fqn], nil }
func (d *h2GraphDB) Callees(fqn string) ([]gleann.Callee, error) {
	if c, ok := d.callees[fqn]; ok {
		return c, nil
	}
	return nil, errors.New("no such symbol")
}

func h2GraphServer(t *testing.T, g analysisGraph) *Server {
	t.Helper()
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom"})
	t.Cleanup(srv.Close)
	srv.gPool.dbs["fake"] = g
	return srv
}

func TestH2GraphTools_BackendErrorsAreToolErrors(t *testing.T) {
	boom := errors.New("disk I/O error")
	cases := []struct {
		name string
		g    *h2Graph
		tool func(*Server) toolHandler
		args map[string]any
		want string
	}{
		{"communities", &h2Graph{commErr: boom}, func(s *Server) toolHandler { return s.handleCommunities }, nil, "Community detection failed: disk I/O error"},
		{"risk count", &h2Graph{countErr: boom}, func(s *Server) toolHandler { return s.handleRiskAnalysis }, nil, "Query error: disk I/O error"},
		{"risk large by file", &h2Graph{count: 2500, inErr: boom}, func(s *Server) toolHandler { return s.handleRiskAnalysis }, map[string]any{"by_file": true}, "Query error: disk I/O error"},
		{"risk large", &h2Graph{count: 2500, inErr: boom}, func(s *Server) toolHandler { return s.handleRiskAnalysis }, nil, "Query error: disk I/O error"},
		{"risk small", &h2Graph{count: 3, callErr: boom}, func(s *Server) toolHandler { return s.handleRiskAnalysis }, nil, `Error loading graph "fake": disk I/O error`},
		{"navigate search", &h2Graph{db: &h2GraphDB{searchErr: boom}}, func(s *Server) toolHandler { return s.handleNavigateSymbol }, map[string]any{"symbol": "x"}, "Symbol search failed: disk I/O error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := h2GraphServer(t, tc.g)
			args := map[string]any{"index": "fake"}
			for k, v := range tc.args {
				args[k] = v
			}
			res := h2Call(t, tc.tool(srv), args)
			if !res.IsError || !strings.Contains(extractText(res), tc.want) {
				t.Fatalf("got error=%v %q, want %q", res.IsError, extractText(res), tc.want)
			}
		})
	}
}

func TestH2GraphTools_OpenErrors(t *testing.T) {
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom"})
	defer srv.Close()
	for _, h := range []toolHandler{srv.handleCommunities, srv.handleRiskAnalysis, srv.handleRepoMap} {
		res := h2Call(t, h, map[string]any{"index": "../escape"})
		if !res.IsError || !strings.Contains(extractText(res), `Error opening graph "../escape"`) {
			t.Errorf("invalid name: %q", extractText(res))
		}
	}
	res := h2Call(t, srv.handleNavigateSymbol, map[string]any{"index": "missing", "symbol": "x"})
	if !res.IsError || !strings.Contains(extractText(res), `Error opening graph "missing"`) {
		t.Errorf("navigate on a missing index: %q", extractText(res))
	}
	if len(srv.gPool.dbs) != 0 {
		t.Error("a failed open was cached")
	}
}

func TestH2RiskAnalysis_EmptyGraphs(t *testing.T) {
	srv := h2GraphServer(t, &h2Graph{count: 0})
	if out := extractText(h2Call(t, srv.handleRiskAnalysis, map[string]any{"index": "fake"})); !strings.Contains(out, "No nodes in graph") {
		t.Errorf("zero symbols: %q", out)
	}
	// Symbols counted but the call graph loads empty.
	srv = h2GraphServer(t, &h2Graph{count: 4, call: community.NewGraph()})
	if out := extractText(h2Call(t, srv.handleRiskAnalysis, map[string]any{"index": "fake"})); !strings.Contains(out, "No nodes in graph") {
		t.Errorf("empty call graph: %q", out)
	}
}

func TestH2RiskAnalysis_LargeGraphFastPath(t *testing.T) {
	g := &h2Graph{count: 2500, in: []graphstore.InDegree{
		{FQN: "p.A", Name: "p.A", Kind: "function", File: "a.go", InDegree: 60},
		{FQN: "p.B", Name: "p.B", Kind: "method", File: "b.go", InDegree: 25},
		{FQN: "p.C", Name: "p.C", Kind: "function", File: "c.go", InDegree: 7},
		{FQN: "p.D", Name: "p.D", Kind: "function", File: "d.go", InDegree: 1},
	}}
	srv := h2GraphServer(t, g)
	out := extractText(h2Call(t, srv.handleRiskAnalysis, map[string]any{"index": "fake", "top": 4.0}))
	for _, want := range []string{"Symbol Risk Analysis — fake (top 4)", "CRITICAL", "HIGH", "MEDIUM", "LOW", "0.6000"} {
		if !strings.Contains(out, want) {
			t.Errorf("symbol view lacks %q:\n%s", want, out)
		}
	}
	if len(g.inCalls) != 1 || len(g.inCalls[0]) != 1 || g.inCalls[0][0] != "macro" {
		t.Errorf("symbol view must exclude macros: %v", g.inCalls)
	}
	out = extractText(h2Call(t, srv.handleRiskAnalysis, map[string]any{"index": "fake", "top": 2.0, "by_file": true}))
	if !strings.Contains(out, "File Risk Analysis — fake (top 2)") || !strings.Contains(out, "a.go") || strings.Contains(out, "c.go") {
		t.Errorf("file view:\n%s", out)
	}
}

func TestH2RepoMap_Fallbacks(t *testing.T) {
	t.Run("in-degree when the full graph fails", func(t *testing.T) {
		g := &h2Graph{fullErr: errors.New("x"), in: []graphstore.InDegree{
			{FQN: "p.Serve", Name: "Serve", Kind: "function", File: "srv.go", InDegree: 9},
			{FQN: "v.Lib", Name: "Lib", Kind: "function", File: "/repo/vendor/v/lib.go", InDegree: 20},
		}}
		srv := h2GraphServer(t, g)
		out := extractText(h2Call(t, srv.handleRepoMap, map[string]any{"index": "fake", "top_k": 5.0}))
		if !strings.Contains(out, "### srv.go") || !strings.Contains(out, "`Serve`") || !strings.Contains(out, "`Lib`") {
			t.Fatalf("repo map:\n%s", out)
		}
		// With one slot, the project's own symbol wins over the busier
		// vendored one.
		out = extractText(h2Call(t, srv.handleRepoMap, map[string]any{"index": "fake", "top_k": 1.0}))
		if !strings.Contains(out, "`Serve`") || strings.Contains(out, "`Lib`") {
			t.Errorf("noise symbol took the only slot:\n%s", out)
		}
	})
	t.Run("declarations when nothing is called", func(t *testing.T) {
		var decls []graphstore.Symbol
		for i := 0; i < 4; i++ {
			decls = append(decls, graphstore.Symbol{FQN: fmt.Sprintf("p.F%d", i), Name: fmt.Sprintf("F%d", i), Kind: "function", File: "f.go"})
		}
		decls = append(decls, graphstore.Symbol{FQN: "v.G", Name: "G", Kind: "function", File: "/repo/vendor/g.go"})
		srv := h2GraphServer(t, &h2Graph{decls: decls})
		out := extractText(h2Call(t, srv.handleRepoMap, map[string]any{"index": "fake", "top_k": 2.0}))
		if !strings.Contains(out, "`F0`") || !strings.Contains(out, "`F1`") || strings.Contains(out, "`F2`") {
			t.Errorf("top_k=2 over declarations:\n%s", out)
		}
		out = extractText(h2Call(t, srv.handleRepoMap, map[string]any{"index": "fake", "top_k": 10.0}))
		if !strings.Contains(out, "`G`") {
			t.Errorf("noise declarations must fill the remaining slots:\n%s", out)
		}
	})
	t.Run("nothing at all", func(t *testing.T) {
		srv := h2GraphServer(t, &h2Graph{declErr: errors.New("x")})
		out := extractText(h2Call(t, srv.handleRepoMap, map[string]any{"index": "fake"}))
		if !strings.Contains(out, "No nodes in graph") {
			t.Errorf("got %q", out)
		}
	})
}

func TestH2NavigateSymbol_CallersCalleesAndDepth(t *testing.T) {
	db := &h2GraphDB{
		search: []gleann.Callee{
			{FQN: "p.Root", Kind: "function"},
			{FQN: "p.Lone", Kind: "function"},
		},
		callers: map[string][]gleann.Callee{
			"p.Root": {
				{FQN: "p.Main", Kind: "function", File: "main.go"},
				{FQN: "p.TestRoot", Kind: "function", File: "root_test.go", Line: 3, IsTest: true},
			},
		},
		callees: map[string][]gleann.Callee{
			"p.Root": {{FQN: "p.Mid", Kind: "function", File: "mid.go"}},
			"p.Mid":  {{FQN: "p.Leaf", Kind: "function", File: "leaf.go", Line: 9}},
			"p.Leaf": {{FQN: "p.Deep", Kind: "function"}},
		},
	}
	srv := h2GraphServer(t, &h2Graph{db: db})
	out := extractText(h2Call(t, srv.handleNavigateSymbol, map[string]any{"index": "fake", "symbol": "Root", "depth": 3.0}))
	for _, want := range []string{
		"← p.Main (function) [main.go]",
		"[+ 1 test caller(s)]",
		"→ p.Mid (function) [mid.go]",
		"p.Mid → p.Leaf (function)",
		"3rd-level callees:",
		"p.Leaf → p.Deep (function)",
		"── 2. p.Lone (function)\n  Callers: none\n  Callees: none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("navigate lacks %q:\n%s", want, out)
		}
	}

	// Only test callers: the production list is empty.
	db.callers["p.Root"] = []gleann.Callee{{FQN: "p.TestRoot", Kind: "function", IsTest: true}}
	out = extractText(h2Call(t, srv.handleNavigateSymbol, map[string]any{"index": "fake", "symbol": "Root"}))
	if !strings.Contains(out, "  Callers:\n    [+ 1 test caller(s)]") {
		t.Errorf("test-only callers:\n%s", out)
	}
}

func TestH2GraphPool_CloseReleasesHandles(t *testing.T) {
	g := &h2Graph{}
	srv := h2GraphServer(t, g)
	srv.evictGraph("fake")
	if !g.closed || len(srv.gPool.dbs) != 0 {
		t.Fatalf("evictGraph: closed=%v cached=%d", g.closed, len(srv.gPool.dbs))
	}
	srv.evictGraph("fake") // already gone: no-op
	g2 := &h2Graph{}
	srv.gPool.dbs["other"] = g2
	srv.closeGraphPool()
	if !g2.closed {
		t.Error("closeGraphPool left a handle open")
	}
}

// storeGraph.Declarations on a real SQLite code graph: ordered by FQN,
// limited, without the excluded kinds and without fileless symbols.
func TestH2StoreGraphDeclarations(t *testing.T) {
	srv := newGraphQueryServer(t)
	ag, err := srv.gPool.get(graphToolsIndex)
	if err != nil {
		t.Fatal(err)
	}
	all, err := ag.Declarations(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 70 {
		t.Fatalf("Declarations(0) = %d symbols, want every declared symbol", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].FQN > all[i].FQN {
			t.Fatalf("not ordered by FQN: %s before %s", all[i-1].FQN, all[i].FQN)
		}
	}
	few, err := ag.Declarations(3)
	if err != nil || len(few) != 3 {
		t.Fatalf("Declarations(3) = %d, %v", len(few), err)
	}
	none, err := ag.Declarations(0, "function")
	if err != nil || len(none) != 0 {
		t.Errorf("excluding the only kind left %d symbols (err %v)", len(none), err)
	}
	n, err := ag.SymbolCount()
	if err != nil || n < len(all) {
		t.Errorf("SymbolCount = %d, %v", n, err)
	}
}
