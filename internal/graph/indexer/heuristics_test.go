//go:build treesitter

package indexer_test

import (
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

// The heuristics (applyHeuristics, detectSourceHint) are package-private;
// these tests check their public effect: the Weight of the symbols that
// ExtractFile returns after a real parse.

// declared returns the anchored declarations of an extracted file.
func declared(fg indexer.FileGraph) []gtypes.SymbolNode {
	var out []gtypes.SymbolNode
	for _, s := range fg.Symbols {
		if s.File != "" {
			out = append(out, s)
		}
	}
	return out
}

func weights(t *testing.T, module, root, absPath, src string) []gtypes.SymbolNode {
	t.Helper()
	syms := declared(indexer.ExtractFile(module, root, absPath, src))
	for _, s := range syms {
		t.Logf("  [%s] %s weight=%.3f", s.Kind, s.FQN, s.Weight)
	}
	return syms
}

func TestHeuristicsGoInterfaceOutranksFunction(t *testing.T) {
	const src = `package mypkg

type Greeter interface {
	Greet(name string) string
}

func helper() string { return "hi" }
`
	syms := weights(t, "github.com/tevfik/gleann", "/fake/root", "/fake/root/p/g.go", src)
	var ifaceW, fnW float64
	var ifaceFound, fnFound bool
	for _, s := range syms {
		if s.Name == "Greeter" {
			ifaceW, ifaceFound = s.Weight, true
		}
		if s.Name == "helper" {
			fnW, fnFound = s.Weight, true
		}
	}
	if !ifaceFound {
		t.Fatalf("Greeter interface not found in %+v", syms)
	}
	if !fnFound {
		t.Fatalf("helper function not found in %+v", syms)
	}
	if ifaceW <= fnW {
		t.Errorf("expected interface weight (%.3f) > function weight (%.3f)", ifaceW, fnW)
	}
	if ifaceW < 1.5 || ifaceW > 1.9 {
		t.Errorf("interface weight out of expected band [1.5, 1.9]: %.3f", ifaceW)
	}
	if fnW < 0.99 || fnW > 1.05 {
		t.Errorf("function weight should be ~1.0, got %.3f", fnW)
	}
}

func TestHeuristicsPythonDecoratorBoost(t *testing.T) {
	const src = `@app.route("/")
def index():
    return "hi"

def plain():
    return "x"
`
	syms := weights(t, "github.com/tevfik/gleann", "/r", "/r/mod.py", src)
	var decorated, plainFn float64
	var decFound, plainFound bool
	for _, s := range syms {
		if s.Name == "index" {
			decorated, decFound = s.Weight, true
		}
		if s.Name == "plain" {
			plainFn, plainFound = s.Weight, true
		}
	}
	if !decFound {
		t.Fatalf("decorated 'index' not found: %+v", syms)
	}
	if !plainFound {
		t.Fatalf("plain function not found: %+v", syms)
	}
	// Decorated gets +0.4 plus base 1.0 = 1.4 (the chunker surfaces the
	// decorated_definition wrapper as NodeType).
	if decorated < 1.3 {
		t.Errorf("decorated function weight (%.3f) should be ≥ 1.3", decorated)
	}
	if plainFn >= decorated {
		t.Errorf("decorated weight (%.3f) should beat plain (%.3f)", decorated, plainFn)
	}
}

func TestHeuristicsWeightIsBounded(t *testing.T) {
	const src = `package mypkg
type Iface interface{ Foo() }
type Iface2 interface{ Bar() }
func helper() {}
func other() {}
func (r Iface) Foo() {}
`
	for _, s := range weights(t, "github.com/tevfik/gleann", "/fake/root", "/fake/root/p/over.go", src) {
		if s.Weight > 2.0 {
			t.Errorf("%s: weight %.3f exceeded cap 2.0", s.FQN, s.Weight)
		}
		if s.Weight < 0.99 {
			t.Errorf("%s: weight %.3f below neutral baseline 1.0", s.FQN, s.Weight)
		}
	}
}

func TestHeuristicsWeightNeverZero(t *testing.T) {
	const src = `package mypkg
var x = 1
const Y = 2
type T struct{}
`
	syms := weights(t, "github.com/tevfik/gleann", "/fake/root", "/fake/root/p/v.go", src)
	if len(syms) == 0 {
		t.Fatal("no symbols extracted")
	}
	for _, s := range syms {
		if s.Weight == 0 {
			t.Errorf("%s: zero weight (the neutral default is 1.0)", s.FQN)
		}
	}
}

func TestHeuristicsSingleFunctionInRange(t *testing.T) {
	const src = `package mypkg
func onlyFunc() {}
`
	for _, s := range weights(t, "github.com/tevfik/gleann", "/fake/root", "/fake/root/p/unk.go", src) {
		if s.Weight < 0.99 || s.Weight > 2.0 {
			t.Errorf("%s: weight %.3f out of [0.99, 2.0]", s.FQN, s.Weight)
		}
	}
}
