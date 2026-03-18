//go:build treesitter && !windows

package kuzu_test

import (
	"testing"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
)

func TestKuzuPoc(t *testing.T) {
	// Use in-memory mode for testing (pass empty string).
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Upsert a source file.
	if err := db.UpsertFile("internal/vault/store.go", "go"); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	// Upsert two symbols.
	sym1 := kgraph.SymbolNode{
		FQN:  "github.com/tevfik/gleann/internal/vault.Store.Put",
		Kind: "method",
		File: "internal/vault/store.go",
		Line: 42,
		Name: "Put",
		Doc:  "Put inserts a document into the vault.",
	}
	sym2 := kgraph.SymbolNode{
		FQN:  "github.com/tevfik/gleann/internal/vault.encode",
		Kind: "function",
		File: "internal/vault/store.go",
		Line: 12,
		Name: "encode",
	}
	if err := db.UpsertSymbol(sym1); err != nil {
		t.Fatalf("UpsertSymbol sym1: %v", err)
	}
	if err := db.UpsertSymbol(sym2); err != nil {
		t.Fatalf("UpsertSymbol sym2: %v", err)
	}

	// Record the DECLARES relationship.
	if err := db.AddDeclares("internal/vault/store.go", sym1.FQN); err != nil {
		t.Fatalf("AddDeclares: %v", err)
	}
	if err := db.AddDeclares("internal/vault/store.go", sym2.FQN); err != nil {
		t.Fatalf("AddDeclares: %v", err)
	}

	// Record that Put calls encode.
	if err := db.AddCalls(sym1.FQN, sym2.FQN); err != nil {
		t.Fatalf("AddCalls: %v", err)
	}

	// Query: get all callees of Put.
	callees, err := db.Callees(sym1.FQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if len(callees) != 1 || callees[0].FQN != sym2.FQN {
		t.Errorf("expected [%s] callees, got %+v", sym2.FQN, callees)
	}

	// Query: get all symbols in file.
	symbols, err := db.SymbolsInFile("internal/vault/store.go")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	if len(symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(symbols))
	}

	t.Logf("✅ KuzuDB PoC passed: %d callees for Put, %d symbols in file", len(callees), len(symbols))
}
