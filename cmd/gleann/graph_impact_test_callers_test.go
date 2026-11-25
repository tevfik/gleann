package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// Roadmap 3.6: `gleann graph explain` must print the test callers the store
// already computes. The hand-made graph-only index below gives three shapes:
//
//	impcallers.Hub        production callers + 25 test callers (truncation at 20)
//	impcallers.OnlyTests  test callers only, so no affected file to hang the block on
//	impcallers.NoTests    production callers, no test caller (prints "Test callers: 0")

// newImpSymIndex writes a graph-only format-2 index and returns its index dir.
func newImpSymIndex(t *testing.T) (indexDir, name string) {
	t.Helper()
	name = "impcallers"
	dir := t.TempDir()
	sub := filepath.Join(dir, name)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, name+".meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(sub, name+".db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := graphstore.New(st)
	ctx := context.Background()

	sym := func(fqn, file string, line int64, isTest bool) graphstore.Symbol {
		return graphstore.Symbol{
			FQN: fqn, Kind: "function", File: file, Name: fqn[strings.LastIndex(fqn, ".")+1:],
			StartLine: line, EndLine: line + 3, StartCol: 1, ByteOffset: line * 30, IsTest: isTest,
		}
	}

	decls := graphstore.FileDelta{File: "core/hub.go", Symbols: []graphstore.Symbol{
		sym(name+".Hub", "core/hub.go", 10, false),
		sym(name+".OnlyTests", "core/hub.go", 20, false),
		sym(name+".NoTests", "core/hub.go", 30, false),
	}}
	prod := graphstore.FileDelta{File: "core/prod.go"}
	for i, c := range []string{name + ".CallerA", name + ".CallerB"} {
		prod.Symbols = append(prod.Symbols, sym(c, "core/prod.go", int64(1+i*5), false))
		prod.Edges = append(prod.Edges, graphstore.Edge{Src: c, Dst: name + ".Hub"})
	}
	prod.Symbols = append(prod.Symbols, sym(name+".SingleProd", "core/prod.go", 100, false))
	prod.Edges = append(prod.Edges, graphstore.Edge{Src: name + ".SingleProd", Dst: name + ".NoTests"})

	tests := graphstore.FileDelta{File: "core/hub_test.go"}
	for i := 0; i < 25; i++ {
		c := fmt.Sprintf("%s.TestHub%02d", name, i)
		tests.Symbols = append(tests.Symbols, sym(c, "core/hub_test.go", int64(1+i*5), true))
		tests.Edges = append(tests.Edges, graphstore.Edge{Src: c, Dst: name + ".Hub"})
	}
	onlyTests := graphstore.FileDelta{File: "core/only_test_test.go"}
	for i := 0; i < 3; i++ {
		c := fmt.Sprintf("%s.Tester%02d", name, i)
		onlyTests.Symbols = append(onlyTests.Symbols, sym(c, "core/only_test_test.go", int64(1+i*5), true))
		onlyTests.Edges = append(onlyTests.Edges, graphstore.Edge{Src: c, Dst: name + ".OnlyTests"})
	}

	for _, d := range []graphstore.FileDelta{decls, prod, tests, onlyTests} {
		if err := g.ApplyFileDelta(ctx, d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, name
}

// explainIn runs `gleann graph explain <symbol> --index <name> --index-dir <dir>`.
func explainIn(t *testing.T, indexDir, name, symbol string) cliResult {
	t.Helper()
	e := newCLIEnv(t)
	return e.run("graph", "explain", symbol, "--index", name, "--index-dir", indexDir)
}

func TestCLICGraphExplainTestCallers(t *testing.T) {
	dir, name := newImpSymIndex(t)

	// 25 test callers: the count is printed and the list is capped at 20.
	r := explainIn(t, dir, name, name+".Hub")
	wantExit(t, r, 0)
	mustContain(t, "explain hub impact", r.Out,
		"Impact (blast radius, depth=3):",
		"Direct callers (production only): 2",
		"Transitive callers: 0",
		"Affected files:     1",
		"Test callers:       25",
		"🧪 impcallers.TestHub00",
		"🧪 impcallers.TestHub19",
		"... and 5 more test callers",
	)
	mustNotContain(t, "explain hub test caller cap", r.Out, "🧪 impcallers.TestHub20")

	// A symbol whose only callers are tests still gets an impact block: the
	// blast radius has no affected file to print, but the test callers do.
	r = explainIn(t, dir, name, name+".OnlyTests")
	wantExit(t, r, 0)
	mustContain(t, "explain test-only symbol", r.Out,
		"Impact (blast radius, depth=3):",
		"Direct callers (production only): 0",
		"Affected files:     0",
		"Test callers:       3",
		"🧪 impcallers.Tester00",
		"🧪 impcallers.Tester02",
	)

	// No test callers: say so instead of pretending nothing happened.
	r = explainIn(t, dir, name, name+".NoTests")
	wantExit(t, r, 0)
	mustContain(t, "explain without test callers", r.Out,
		"Impact (blast radius, depth=3):",
		"Direct callers (production only): 1",
		"Test callers:       0",
	)
	mustNotContain(t, "no test caller rows", r.Out, "🧪 impcallers.")
}
