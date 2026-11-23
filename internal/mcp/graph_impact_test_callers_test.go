package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Roadmap 3.6: graphstore.Impact already fills ImpactResult.TestCallers, but
// handleImpact never surfaced them. The hand-made graph of this fixture:
//
//	pkg.Hub        2 production callers + 25 test callers (so the new section truncates at 20)
//	pkg.OnlyTests  a production symbol whose only callers are 3 test symbols
//	pkg.NoTests    1 production caller, no test caller
//
// so a test caller can neither move the blast radius nor hide the impact block.
const impactCallersIndex = "impcallers"

func newImpactCallersServer(t *testing.T) *Server {
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
	}
	if err := builder.Build(context.Background(), impactCallersIndex, passages); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, impactCallersIndex, impactCallersIndex+".db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := graphstore.New(st)
	ctx := context.Background()

	hub := graphstore.FileDelta{File: "core/hub.go", Symbols: []graphstore.Symbol{
		gqSym("pkg.Hub", "function", "core/hub.go", 10, false),
		gqSym("pkg.OnlyTests", "function", "core/hub.go", 20, false),
		gqSym("pkg.NoTests", "function", "core/hub.go", 30, false),
	}}
	prod := graphstore.FileDelta{File: "core/prod.go"}
	for _, c := range []string{"pkg.CallerA", "pkg.CallerB"} {
		prod.Symbols = append(prod.Symbols, gqSym(c, "function", "core/prod.go", int64(1+len(prod.Symbols)*5), false))
		prod.Edges = append(prod.Edges, graphstore.Edge{Src: c, Dst: "pkg.Hub"})
	}
	prod.Symbols = append(prod.Symbols, gqSym("pkg.SingleProd", "function", "core/prod.go", 100, false))
	prod.Edges = append(prod.Edges, graphstore.Edge{Src: "pkg.SingleProd", Dst: "pkg.NoTests"})

	// 25 test callers of pkg.Hub and 3 of pkg.OnlyTests: both files are test files.
	tests := graphstore.FileDelta{File: "core/hub_test.go"}
	for i := 0; i < 25; i++ {
		c := fmt.Sprintf("pkg.TestHub%02d", i)
		tests.Symbols = append(tests.Symbols, gqSym(c, "function", "core/hub_test.go", int64(1+i*5), true))
		tests.Edges = append(tests.Edges, graphstore.Edge{Src: c, Dst: "pkg.Hub"})
	}
	onlyTests := graphstore.FileDelta{File: "core/only_test_test.go"}
	for i := 0; i < 3; i++ {
		c := fmt.Sprintf("pkg.Tester%02d", i)
		onlyTests.Symbols = append(onlyTests.Symbols, gqSym(c, "function", "core/only_test_test.go", int64(1+i*5), true))
		onlyTests.Edges = append(onlyTests.Edges, graphstore.Edge{Src: c, Dst: "pkg.OnlyTests"})
	}

	for _, d := range []graphstore.FileDelta{hub, prod, tests, onlyTests} {
		if err := g.ApplyFileDelta(ctx, d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	s := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom", EmbeddingModel: "test-model"})
	s.embedder = embedder
	t.Cleanup(s.Close)
	searcher := gleann.NewSearcher(glCfg, embedder)
	if err := searcher.Load(ctx, impactCallersIndex); err != nil {
		t.Fatal(err)
	}
	if searcher.GraphDB() == nil {
		t.Fatal("expected the searcher to expose the SQLite graph")
	}
	s.searcherMu.Lock()
	s.searchers[impactCallersIndex] = searcher
	s.searcherMu.Unlock()
	return s
}

func TestImpact_ListsAndTruncatesTestCallers(t *testing.T) {
	s := newImpactCallersServer(t)
	out := callTool(t, s.handleImpact, map[string]interface{}{"index": impactCallersIndex, "symbol": "pkg.Hub"})

	for _, want := range []string{
		"=== Direct Callers (2 total) ===",
		"- pkg.CallerA",
		"=== Test Callers (25 total) ===",
		"- pkg.TestHub00",
		"- pkg.TestHub19",
		"... and 5 more test callers",
		"=== Affected Files (1 total: 1 core, 0 submodules/tests) ===",
		"- core/prod.go",
		// Blast radius stays production-only: test callers never expand it.
		"Total blast radius: 2 affected symbols across 1 files",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- pkg.TestHub20") {
		t.Errorf("test callers must cap at 20 items:\n%s", out)
	}
	// Placement: after the transitive callers, before the affected files.
	trans := strings.Index(out, "=== Transitive Callers")
	testers := strings.Index(out, "=== Test Callers")
	files := strings.Index(out, "=== Affected Files")
	blast := strings.Index(out, "Total blast radius")
	if !(trans >= 0 && testers > trans && files > testers && blast > files) {
		t.Errorf("section order is wrong (transitive=%d test=%d files=%d blast=%d):\n%s", trans, testers, files, blast, out)
	}
}

func TestImpact_TestCallersOnlySymbolIsStillReported(t *testing.T) {
	s := newImpactCallersServer(t)
	out := callTool(t, s.handleImpact, map[string]interface{}{"index": impactCallersIndex, "symbol": "pkg.OnlyTests"})

	for _, want := range []string{
		"=== Direct Callers (0 total) ===",
		"=== Transitive Callers (0 total) ===",
		"=== Test Callers (3 total) ===",
		"- pkg.Tester00",
		"- pkg.Tester02",
		"=== Affected Files (0 total: 0 core, 0 submodules/tests) ===",
		"Total blast radius: 0 affected symbols across 0 files",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The "did you mean" hint is for symbols with no callers at all: this one is
	// called by tests, and the tool now says so.
	if strings.Contains(out, "Did you mean") {
		t.Errorf("no suggestion expected when test callers exist:\n%s", out)
	}
}

func TestImpact_NoTestCallersSaysNoneFound(t *testing.T) {
	s := newImpactCallersServer(t)
	out := callTool(t, s.handleImpact, map[string]interface{}{"index": impactCallersIndex, "symbol": "pkg.NoTests"})

	if !strings.Contains(out, "=== Test Callers (0 total) ===\nNone found.\n") {
		t.Errorf("want an explicit empty test-caller section in:\n%s", out)
	}
	if !strings.Contains(out, "Total blast radius: 1 affected symbols across 1 files") {
		t.Errorf("blast radius must stay production-only:\n%s", out)
	}
	if strings.Contains(out, "pkg.TestHub") {
		t.Errorf("unrelated test callers leaked into the answer:\n%s", out)
	}
}
