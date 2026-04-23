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

// helper: setupGraph creates an in-memory DB and populates it with a
// small call graph:
//
//	file1: A → B → C   (CALLS chain)
//	file2: D → B        (cross-file caller)
//
// Returns the open DB and a cleanup function.
func setupGraph(t *testing.T) *kgraph.DB {
	t.Helper()
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Files
	for _, f := range []string{"pkg/file1.go", "pkg/file2.go"} {
		if err := db.UpsertFile(f, "go"); err != nil {
			t.Fatalf("UpsertFile(%s): %v", f, err)
		}
	}

	// Symbols
	syms := []kgraph.SymbolNode{
		{FQN: "mod/pkg.A", Kind: "function", File: "pkg/file1.go", Line: 10, Name: "A"},
		{FQN: "mod/pkg.B", Kind: "function", File: "pkg/file1.go", Line: 20, Name: "B"},
		{FQN: "mod/pkg.C", Kind: "function", File: "pkg/file1.go", Line: 30, Name: "C"},
		{FQN: "mod/pkg.D", Kind: "function", File: "pkg/file2.go", Line: 10, Name: "D"},
	}
	for _, s := range syms {
		if err := db.UpsertSymbol(s); err != nil {
			t.Fatalf("UpsertSymbol(%s): %v", s.FQN, err)
		}
	}

	// Declares
	// Use individual calls for each file→symbol pair:
	for _, pair := range [][2]string{
		{"pkg/file1.go", "mod/pkg.A"},
		{"pkg/file1.go", "mod/pkg.B"},
		{"pkg/file1.go", "mod/pkg.C"},
		{"pkg/file2.go", "mod/pkg.D"},
	} {
		if err := db.AddDeclares(pair[0], pair[1]); err != nil {
			t.Fatalf("AddDeclares(%s, %s): %v", pair[0], pair[1], err)
		}
	}

	// Calls: A→B, B→C, D→B
	for _, pair := range [][2]string{
		{"mod/pkg.A", "mod/pkg.B"},
		{"mod/pkg.B", "mod/pkg.C"},
		{"mod/pkg.D", "mod/pkg.B"},
	} {
		if err := db.AddCalls(pair[0], pair[1]); err != nil {
			t.Fatalf("AddCalls(%s→%s): %v", pair[0], pair[1], err)
		}
	}

	return db
}

func TestNeighbors(t *testing.T) {
	db := setupGraph(t)
	defer db.Close()

	// Depth-1 neighbors of B should include A (caller), C (callee), D (caller)
	edges, err := db.Neighbors("mod/pkg.B", 1)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}

	if len(edges) < 3 {
		t.Errorf("expected ≥3 edges around B, got %d: %+v", len(edges), edges)
	}

	seenA, seenC, seenD := false, false, false
	for _, e := range edges {
		if e.From == "mod/pkg.A" || e.To == "mod/pkg.A" {
			seenA = true
		}
		if e.From == "mod/pkg.C" || e.To == "mod/pkg.C" {
			seenC = true
		}
		if e.From == "mod/pkg.D" || e.To == "mod/pkg.D" {
			seenD = true
		}
	}
	if !seenA {
		t.Error("expected to see A as neighbor of B")
	}
	if !seenC {
		t.Error("expected to see C as neighbor of B")
	}
	if !seenD {
		t.Error("expected to see D as neighbor of B")
	}
}

func TestShortestPath(t *testing.T) {
	db := setupGraph(t)
	defer db.Close()

	// Path from A to C should go A→B→C
	path, err := db.ShortestPath("mod/pkg.A", "mod/pkg.C")
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}

	if len(path) != 2 {
		t.Fatalf("expected 2 hops (A→B→C), got %d: %+v", len(path), path)
	}
	if path[0].From != "mod/pkg.A" || path[0].To != "mod/pkg.B" {
		t.Errorf("hop 0: expected A→B, got %s→%s", path[0].From, path[0].To)
	}
	if path[1].From != "mod/pkg.B" || path[1].To != "mod/pkg.C" {
		t.Errorf("hop 1: expected B→C, got %s→%s", path[1].From, path[1].To)
	}

	// Path from C to D (not directly connected by CALLS outgoing from C)
	// but via reverse: C←B←D
	path2, err := db.ShortestPath("mod/pkg.C", "mod/pkg.D")
	if err != nil {
		t.Fatalf("ShortestPath C→D: %v", err)
	}
	if len(path2) < 1 {
		t.Errorf("expected path C→D, got none")
	}

	// Non-existent path
	_, err = db.ShortestPath("mod/pkg.A", "nonexistent.Foo")
	if err == nil {
		t.Error("expected error for non-existent target, got nil")
	}
}

func TestSymbolSearch(t *testing.T) {
	db := setupGraph(t)
	defer db.Close()

	// Search with a lowercase substring that matches stored FQN (mod/pkg.X)
	results, err := db.SymbolSearch("mod/pkg")
	if err != nil {
		t.Fatalf("SymbolSearch: %v", err)
	}

	if len(results) < 4 {
		t.Errorf("expected ≥4 symbols matching 'mod/pkg', got %d", len(results))
	}

	// Search for non-existent pattern
	results2, err := db.SymbolSearch("zzz_nonexistent_zzz")
	if err != nil {
		t.Fatalf("SymbolSearch(nonexistent): %v", err)
	}
	if len(results2) != 0 {
		t.Errorf("expected 0 symbols for nonexistent search, got %d", len(results2))
	}
}

func TestStats(t *testing.T) {
	db := setupGraph(t)
	defer db.Close()

	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if stats.Files != 2 {
		t.Errorf("expected 2 files, got %d", stats.Files)
	}
	if stats.Symbols != 4 {
		t.Errorf("expected 4 symbols, got %d", stats.Symbols)
	}
	if stats.CallEdges != 3 {
		t.Errorf("expected 3 call edges, got %d", stats.CallEdges)
	}
	if stats.DeclareEdges != 4 {
		t.Errorf("expected 4 declare edges, got %d", stats.DeclareEdges)
	}
}

func TestImpact(t *testing.T) {
	db := setupGraph(t)
	defer db.Close()

	// Impact of changing C: who calls C? B calls C, and A/D call B.
	result, err := db.Impact("mod/pkg.C", 5)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}

	if result.Symbol != "mod/pkg.C" {
		t.Errorf("symbol should be mod/pkg.C, got %s", result.Symbol)
	}

	// Direct callers of C: B
	foundB := false
	for _, c := range result.DirectCallers {
		if c == "mod/pkg.B" {
			foundB = true
		}
	}
	if !foundB {
		t.Errorf("expected B as direct caller of C, got %v", result.DirectCallers)
	}

	// Transitive: A and D call B which calls C
	transitiveSet := make(map[string]bool)
	for _, c := range result.TransitiveCallers {
		transitiveSet[c] = true
	}
	if !transitiveSet["mod/pkg.A"] && !transitiveSet["mod/pkg.D"] {
		t.Errorf("expected A or D as transitive callers, got %v", result.TransitiveCallers)
	}

	// Affected files should include file1.go and file2.go
	if len(result.AffectedFiles) < 1 {
		t.Errorf("expected ≥1 affected files, got %v", result.AffectedFiles)
	}
}

func TestRemoveFileSymbols(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Setup: two files, each with symbols.
	if err := db.UpsertFile("pkg/a.go", "go"); err != nil {
		t.Fatalf("UpsertFile a: %v", err)
	}
	if err := db.UpsertFile("pkg/b.go", "go"); err != nil {
		t.Fatalf("UpsertFile b: %v", err)
	}

	symA1 := kgraph.SymbolNode{FQN: "mod.FuncA1", Kind: "function", File: "pkg/a.go", Line: 1, Name: "FuncA1"}
	symA2 := kgraph.SymbolNode{FQN: "mod.FuncA2", Kind: "function", File: "pkg/a.go", Line: 10, Name: "FuncA2"}
	symB1 := kgraph.SymbolNode{FQN: "mod.FuncB1", Kind: "function", File: "pkg/b.go", Line: 1, Name: "FuncB1"}

	for _, s := range []kgraph.SymbolNode{symA1, symA2, symB1} {
		if err := db.UpsertSymbol(s); err != nil {
			t.Fatalf("UpsertSymbol %s: %v", s.Name, err)
		}
	}
	for _, s := range []kgraph.SymbolNode{symA1, symA2} {
		if err := db.AddDeclares("pkg/a.go", s.FQN); err != nil {
			t.Fatalf("AddDeclares %s: %v", s.FQN, err)
		}
	}
	if err := db.AddDeclares("pkg/b.go", symB1.FQN); err != nil {
		t.Fatalf("AddDeclares B1: %v", err)
	}
	// A1 calls B1
	if err := db.AddCalls(symA1.FQN, symB1.FQN); err != nil {
		t.Fatalf("AddCalls: %v", err)
	}

	// Verify both files have symbols.
	symsA, _ := db.SymbolsInFile("pkg/a.go")
	symsB, _ := db.SymbolsInFile("pkg/b.go")
	if len(symsA) != 2 {
		t.Fatalf("expected 2 symbols in a.go, got %d", len(symsA))
	}
	if len(symsB) != 1 {
		t.Fatalf("expected 1 symbol in b.go, got %d", len(symsB))
	}

	// Remove file a.go symbols.
	if err := db.RemoveFileSymbols("pkg/a.go"); err != nil {
		t.Fatalf("RemoveFileSymbols: %v", err)
	}

	// a.go symbols should be gone.
	symsA2, _ := db.SymbolsInFile("pkg/a.go")
	if len(symsA2) != 0 {
		t.Errorf("expected 0 symbols in a.go after removal, got %d", len(symsA2))
	}

	// b.go symbols should still exist.
	symsB2, _ := db.SymbolsInFile("pkg/b.go")
	if len(symsB2) != 1 {
		t.Errorf("expected 1 symbol in b.go after removal of a.go, got %d", len(symsB2))
	}
}

func TestRemoveFileSymbols_NonexistentFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Should not error for a file that doesn't exist.
	if err := db.RemoveFileSymbols("nonexistent.go"); err != nil {
		t.Errorf("RemoveFileSymbols for nonexistent file should not error, got: %v", err)
	}
}
