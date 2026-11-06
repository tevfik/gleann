//go:build treesitter

package indexer

import (
	"testing"

	"github.com/tevfik/gleann/internal/graph/kuzu"
)

// C-03: a callee stub must not shadow the real declaration, in either order.
func TestDedupeSymbolsPrefersDeclaration(t *testing.T) {
	stub := kuzu.SymbolNode{FQN: "p.Helper", Kind: "function", Name: "Helper"}
	decl := kuzu.SymbolNode{FQN: "p.Helper", Kind: "function", Name: "Helper", File: "m.go", Line: 3}
	for name, in := range map[string][]kuzu.SymbolNode{
		"stub first": {stub, decl},
		"decl first": {decl, stub},
	} {
		out := dedupeSymbols(in)
		if len(out) != 1 || out[0].File != "m.go" || out[0].Line != 3 {
			t.Errorf("%s: got %+v", name, out)
		}
	}
	if got := dedupeSymbols([]kuzu.SymbolNode{{FQN: ""}, stub}); len(got) != 1 {
		t.Errorf("empty FQN kept: %+v", got)
	}
}
