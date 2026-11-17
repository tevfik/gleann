package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// RFC 004 A2: communities, risk analysis, repo map and navigate on the
// format-2 SQLite graph, in every build.

var lineRe = regexp.MustCompile(`(\[[^\]:]+):\d+\]`)

type sqliteMCP struct {
	t   *testing.T
	env *analysisgolden.Env
	s   *Server
}

func newSQLiteMCP(t *testing.T) *sqliteMCP {
	env := analysisgolden.BuildIndexFormat2(t)
	s := &Server{gPool: newGraphPool(env.IndexDir)}
	t.Cleanup(s.closeGraphPool)
	return &sqliteMCP{t: t, env: env, s: s}
}

// all returns the full text of every analysis tool, keyed by golden name.
func (m *sqliteMCP) all() map[string]string {
	idx := analysisgolden.IndexName
	out := map[string]string{}
	run := func(name string, h toolHandler, args map[string]interface{}) {
		args["index"] = idx
		out[name] = m.env.Normalize(callTool(m.t, h, args))
	}
	run("mcp_communities.txt", m.s.handleCommunities, map[string]interface{}{})
	run("mcp_risk_analysis.txt", m.s.handleRiskAnalysis, map[string]interface{}{"top": 1000.0})
	run("mcp_risk_analysis_top5.txt", m.s.handleRiskAnalysis, map[string]interface{}{"top": 5.0})
	run("mcp_risk_analysis_by_file.txt", m.s.handleRiskAnalysis, map[string]interface{}{"top": 1000.0, "by_file": true})
	run("mcp_repo_map.txt", m.s.handleRepoMap, map[string]interface{}{"top_k": 1000.0, "max_tokens": 1000000.0})
	run("mcp_repo_map_default.txt", m.s.handleRepoMap, map[string]interface{}{})
	for _, q := range []struct {
		name, symbol string
		depth        float64
	}{
		{"mcp_navigate_validate.txt", "validate", 1},
		{"mcp_navigate_new.txt", "New", 2},
		{"mcp_navigate_main.txt", "main", 3},
		{"mcp_navigate_nomatch.txt", "NoSuchSymbolAnywhere", 1},
	} {
		run(q.name, m.s.handleNavigateSymbol, map[string]interface{}{"symbol": q.symbol, "depth": q.depth})
	}
	return out
}

func TestMCPGraphToolsSQLiteDeterministic(t *testing.T) {
	m := newSQLiteMCP(t)
	first := m.all()
	for name, v := range first {
		if strings.TrimSpace(v) == "" {
			t.Errorf("%s: empty", name)
		}
	}
	for i := 0; i < 4; i++ {
		again := m.all()
		for name, want := range first {
			if again[name] != want {
				t.Fatalf("run %d: %s differs from the first run", i+2, name)
			}
		}
	}
}

// The Go part of the navigate answers equals the A0 Kùzu golden (a Go symbol
// with one caller and no callees; an unknown symbol).
func TestMCPNavigateSQLiteMatchesKuzuGolden(t *testing.T) {
	m := newSQLiteMCP(t)
	got := m.all()
	for _, name := range []string{"mcp_navigate_validate.txt", "mcp_navigate_nomatch.txt"} {
		want, err := os.ReadFile(filepath.Join(analysisgolden.GoldenDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		// Known difference: the Kùzu Go extractor anchors a declaration at its
		// doc comment (service.go:20), format 2 at the func keyword (:21).
		g := lineRe.ReplaceAllString(canonicalNavigate(got[name]), "$1]")
		w := lineRe.ReplaceAllString(string(want), "$1]")
		if g != w {
			t.Errorf("%s:\n got %q\nwant %q", name, g, w)
		}
	}
	if !strings.Contains(got["mcp_navigate_validate.txt"], "[service/service.go:21]") {
		t.Errorf("caller line must be the func line (21):\n%s", got["mcp_navigate_validate.txt"])
	}
	// Communities: the counts differ (the SQLite graph has no phantom nodes);
	// the god nodes made of Go files are the same.
	for _, want := range []string{"service/service.go", "store/store.go"} {
		if !strings.Contains(got["mcp_communities.txt"], `"id":"`+want+`"`) {
			t.Errorf("communities lack the god node %s", want)
		}
	}
}

// Tool errors: unknown index and an index without a graph.
func TestMCPGraphToolsSQLiteErrors(t *testing.T) {
	m := newSQLiteMCP(t)
	for _, h := range []toolHandler{m.s.handleCommunities, m.s.handleRiskAnalysis, m.s.handleRepoMap, m.s.handleNavigateSymbol} {
		res := callToolResult(t, h, map[string]interface{}{"index": "nope", "symbol": "x"})
		if !res.IsError {
			t.Errorf("unknown index must be a tool error")
		}
	}
}

// evictGraph releases the handle (index remove / migrate on Windows) and the
// next call reopens the graph.
func TestMCPGraphPoolEviction(t *testing.T) {
	m := newSQLiteMCP(t)
	idx := analysisgolden.IndexName
	if _, err := m.s.gPool.get(idx); err != nil {
		t.Fatal(err)
	}
	if len(m.s.gPool.dbs) != 1 {
		t.Fatalf("pool holds %d handles, want 1", len(m.s.gPool.dbs))
	}
	m.s.evictGraph(idx)
	if len(m.s.gPool.dbs) != 0 {
		t.Fatal("evictGraph left the handle in the pool")
	}
	if _, err := m.s.gPool.get(idx); err != nil {
		t.Fatalf("reopen after evict: %v", err)
	}
	m.s.closeGraphPool()
	if len(m.s.gPool.dbs) != 0 {
		t.Fatal("closeGraphPool left handles")
	}
}

// A graph above 2000 symbols takes the aggregate path (no PageRank).
func TestMCPRiskFastPathSQLite(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(dir, "big")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idx, "big.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(idx, "big.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	sym := func(i int, kind string) graphstore.Symbol {
		return graphstore.Symbol{FQN: fmt.Sprintf("p.F%04d", i), Name: fmt.Sprintf("F%04d", i), Kind: kind, File: "big.go",
			StartLine: int64(i + 1), EndLine: int64(i + 2), StartCol: 1, ByteOffset: int64(i*10 + 1)}
	}
	var syms []graphstore.Symbol
	var edges []graphstore.Edge
	for i := 0; i < 2100; i++ {
		kind := "function"
		if i == 1 {
			kind = "macro"
		}
		syms = append(syms, sym(i, kind))
		if i > 5 {
			edges = append(edges, graphstore.Edge{Src: fmt.Sprintf("p.F%04d", i), Dst: "p.F0000"})
			if i%2 == 0 {
				edges = append(edges, graphstore.Edge{Src: fmt.Sprintf("p.F%04d", i), Dst: "p.F0001"}) // macro: filtered
			}
		}
		if i > 1000 {
			edges = append(edges, graphstore.Edge{Src: fmt.Sprintf("p.F%04d", i), Dst: "p.F0002"})
		}
	}
	if err := graphstore.New(st).ApplyFileDelta(context.Background(), graphstore.FileDelta{File: "big.go", Symbols: syms, Edges: edges}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	s := &Server{gPool: newGraphPool(dir)}
	t.Cleanup(s.closeGraphPool)
	text := callTool(t, s.handleRiskAnalysis, map[string]interface{}{"index": "big", "top": 3.0})
	if !strings.Contains(text, "F0000") || !strings.Contains(text, "F0002") || strings.Contains(text, "F0001") {
		t.Fatalf("fast path rows wrong (macro must be filtered, F0000 and F0002 present):\n%s", text)
	}
	if strings.Index(text, "F0000") > strings.Index(text, "F0002") {
		t.Fatalf("F0000 (2094 callers) must rank before F0002 (1099):\n%s", text)
	}
	if !strings.Contains(text, "CRITICAL") {
		t.Fatalf("hub with 50+ callers must be CRITICAL:\n%s", text)
	}
	byFile := callTool(t, s.handleRiskAnalysis, map[string]interface{}{"index": "big", "top": 3.0, "by_file": true})
	if !strings.Contains(byFile, "big.go") || !strings.Contains(byFile, "File Risk Analysis") {
		t.Fatalf("by_file fast path:\n%s", byFile)
	}
}
