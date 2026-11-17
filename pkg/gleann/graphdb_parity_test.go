//go:build treesitter && !windows

package gleann_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Parity between the two GraphDB backends: the same call-graph fixture is
// written to Kùzu and to the format-2 SQLite store, and each GraphDB method
// returns the same answer. Kùzu's result order is engine-defined where the
// SQLite store is deterministic, so comparisons are order-insensitive. The
// build tag skips this cleanly when the Kùzu backend is not compiled.

func buildKuzuFixture(t *testing.T) gleann.GraphDB {
	t.Helper()
	db, err := kuzu.Open("")
	if err != nil {
		t.Fatalf("kuzu.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, f := range []string{"pkg/file1.go", "pkg/file2.go"} {
		if err := db.UpsertFile(f, "go"); err != nil {
			t.Fatalf("UpsertFile(%s): %v", f, err)
		}
	}
	syms := []kuzu.SymbolNode{
		{FQN: pfA, Kind: "function", File: "pkg/file1.go", Line: 10, Name: "A"},
		{FQN: pfB, Kind: "function", File: "pkg/file1.go", Line: 20, Name: "B"},
		{FQN: pfC, Kind: "function", File: "pkg/file1.go", Line: 30, Name: "C"},
		{FQN: pfD, Kind: "function", File: "pkg/file2.go", Line: 10, Name: "D"},
	}
	for _, s := range syms {
		if err := db.UpsertSymbol(s); err != nil {
			t.Fatalf("UpsertSymbol(%s): %v", s.FQN, err)
		}
	}
	for _, pair := range [][2]string{
		{"pkg/file1.go", pfA}, {"pkg/file1.go", pfB}, {"pkg/file1.go", pfC}, {"pkg/file2.go", pfD},
	} {
		if err := db.AddDeclares(pair[0], pair[1]); err != nil {
			t.Fatalf("AddDeclares: %v", err)
		}
	}
	for _, pair := range [][2]string{{pfA, pfB}, {pfB, pfC}, {pfD, pfB}} {
		if err := db.AddCalls(pair[0], pair[1]); err != nil {
			t.Fatalf("AddCalls: %v", err)
		}
	}
	return db
}

func calleeFQNs(in []gleann.Callee) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		out = append(out, c.FQN)
	}
	sort.Strings(out)
	return out
}

func TestGraphDBParity(t *testing.T) {
	kuzuDB := buildKuzuFixture(t)
	sqliteDB := buildGraphstoreFixture(t)

	// Callees(A) → [B] in both backends.
	k, err := kuzuDB.Callees(pfA)
	if err != nil {
		t.Fatalf("kuzu Callees: %v", err)
	}
	s, err := sqliteDB.Callees(pfA)
	if err != nil {
		t.Fatalf("sqlite Callees: %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(k), calleeFQNs(s)) {
		t.Fatalf("Callees(A): kuzu %v, sqlite %v", calleeFQNs(k), calleeFQNs(s))
	}

	// Callers(B) → [A, D] in both backends.
	k, err = kuzuDB.Callers(pfB)
	if err != nil {
		t.Fatalf("kuzu Callers: %v", err)
	}
	s, err = sqliteDB.Callers(pfB)
	if err != nil {
		t.Fatalf("sqlite Callers: %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(k), calleeFQNs(s)) {
		t.Fatalf("Callers(B): kuzu %v, sqlite %v", calleeFQNs(k), calleeFQNs(s))
	}

	// SymbolsInFile(file1) → [A, B, C].
	k, err = kuzuDB.SymbolsInFile("pkg/file1.go")
	if err != nil {
		t.Fatalf("kuzu SymbolsInFile: %v", err)
	}
	s, err = sqliteDB.SymbolsInFile("pkg/file1.go")
	if err != nil {
		t.Fatalf("sqlite SymbolsInFile: %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(k), calleeFQNs(s)) {
		t.Fatalf("SymbolsInFile: kuzu %v, sqlite %v", calleeFQNs(k), calleeFQNs(s))
	}

	// SymbolSearch("mod/pkg") → 4 matches in both.
	k, err = kuzuDB.SymbolSearch("mod/pkg")
	if err != nil {
		t.Fatalf("kuzu SymbolSearch: %v", err)
	}
	s, err = sqliteDB.SymbolSearch("mod/pkg")
	if err != nil {
		t.Fatalf("sqlite SymbolSearch: %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(k), calleeFQNs(s)) {
		t.Fatalf("SymbolSearch: kuzu %v, sqlite %v", calleeFQNs(k), calleeFQNs(s))
	}

	// Stats.
	ks, err := kuzuDB.Stats()
	if err != nil {
		t.Fatalf("kuzu Stats: %v", err)
	}
	ss, err := sqliteDB.Stats()
	if err != nil {
		t.Fatalf("sqlite Stats: %v", err)
	}
	if *ks != *ss {
		t.Fatalf("Stats: kuzu %+v, sqlite %+v", ks, ss)
	}

	// Impact(C) direct/transitive callers.
	ki, err := kuzuDB.Impact(pfC, 5)
	if err != nil {
		t.Fatalf("kuzu Impact: %v", err)
	}
	si, err := sqliteDB.Impact(pfC, 5)
	if err != nil {
		t.Fatalf("sqlite Impact: %v", err)
	}
	sort.Strings(ki.DirectCallers)
	sort.Strings(si.DirectCallers)
	sort.Strings(ki.TransitiveCallers)
	sort.Strings(si.TransitiveCallers)
	if !reflect.DeepEqual(ki.DirectCallers, si.DirectCallers) ||
		!reflect.DeepEqual(ki.TransitiveCallers, si.TransitiveCallers) {
		t.Fatalf("Impact(C): kuzu direct=%v trans=%v, sqlite direct=%v trans=%v",
			ki.DirectCallers, ki.TransitiveCallers, si.DirectCallers, si.TransitiveCallers)
	}

	// ShortestPath(A, C) → A→B→C.
	kp, err := kuzuDB.ShortestPath(pfA, pfC)
	if err != nil {
		t.Fatalf("kuzu ShortestPath: %v", err)
	}
	sp, err := sqliteDB.ShortestPath(pfA, pfC)
	if err != nil {
		t.Fatalf("sqlite ShortestPath: %v", err)
	}
	if !reflect.DeepEqual(kp, sp) {
		t.Fatalf("ShortestPath(A,C): kuzu %+v, sqlite %+v", kp, sp)
	}

	// Neighbors(B) reach the same set of edge endpoints.
	kn, err := kuzuDB.Neighbors(pfB, 1)
	if err != nil {
		t.Fatalf("kuzu Neighbors: %v", err)
	}
	sn, err := sqliteDB.Neighbors(pfB, 1)
	if err != nil {
		t.Fatalf("sqlite Neighbors: %v", err)
	}
	sortEdges := func(es []gleann.GraphEdge) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.From+"\x00"+e.To+"\x00"+e.Relation)
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(sortEdges(kn), sortEdges(sn)) {
		t.Fatalf("Neighbors(B): kuzu %v, sqlite %v", sortEdges(kn), sortEdges(sn))
	}
}

func buildKuzuResolutionFixture(t *testing.T) gleann.GraphDB {
	t.Helper()
	db, err := kuzu.Open("")
	if err != nil {
		t.Fatalf("kuzu.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for i, s := range resolutionSyms {
		if err := db.UpsertFile(s.file, "go"); err != nil {
			t.Fatalf("UpsertFile(%s): %v", s.file, err)
		}
		if err := db.UpsertSymbol(kuzu.SymbolNode{FQN: s.fqn, Kind: s.kind, File: s.file, Line: int64(i + 1), Name: s.name}); err != nil {
			t.Fatalf("UpsertSymbol(%s): %v", s.fqn, err)
		}
		if err := db.AddDeclares(s.file, s.fqn); err != nil {
			t.Fatalf("AddDeclares(%s): %v", s.fqn, err)
		}
	}
	for _, pair := range resolutionCalls {
		if err := db.AddCalls(pair[0], pair[1]); err != nil {
			t.Fatalf("AddCalls(%s, %s): %v", pair[0], pair[1], err)
		}
	}
	return db
}

// TestGraphDBNameResolutionParity checks that both backends resolve a user
// symbol reference identically: exact FQN, then a qualified suffix, then the
// base name, first non-empty step wins and an ambiguous base name is a union.
func TestGraphDBNameResolutionParity(t *testing.T) {
	kuzuDB := buildKuzuResolutionFixture(t)
	sqliteDB := buildGraphstoreResolutionFixture(t)

	queries := []string{
		"Run",            // ambiguous base name (callees)
		"WorkerA.Run",    // Type.Method suffix ('.')
		"a.StoreA.Close", // path-qualified suffix ('/')
		"StoreA.Close",   // exact FQN preferred over suffix
		"Close",          // ambiguous base name (callers)
		"Search",         // unique short name
		"mod::Thing",     // double-colon suffix
		"NoSuchSymbol",   // no match
	}
	for _, q := range queries {
		kc, err := kuzuDB.Callees(q)
		if err != nil {
			t.Fatalf("kuzu Callees(%q): %v", q, err)
		}
		sc, err := sqliteDB.Callees(q)
		if err != nil {
			t.Fatalf("sqlite Callees(%q): %v", q, err)
		}
		if !reflect.DeepEqual(calleeFQNs(kc), calleeFQNs(sc)) {
			t.Fatalf("Callees(%q): kuzu %v, sqlite %v", q, calleeFQNs(kc), calleeFQNs(sc))
		}

		kr, err := kuzuDB.Callers(q)
		if err != nil {
			t.Fatalf("kuzu Callers(%q): %v", q, err)
		}
		sr, err := sqliteDB.Callers(q)
		if err != nil {
			t.Fatalf("sqlite Callers(%q): %v", q, err)
		}
		if !reflect.DeepEqual(calleeFQNs(kr), calleeFQNs(sr)) {
			t.Fatalf("Callers(%q): kuzu %v, sqlite %v", q, calleeFQNs(kr), calleeFQNs(sr))
		}

		ki, err := kuzuDB.Impact(q, 3)
		if err != nil {
			t.Fatalf("kuzu Impact(%q): %v", q, err)
		}
		si, err := sqliteDB.Impact(q, 3)
		if err != nil {
			t.Fatalf("sqlite Impact(%q): %v", q, err)
		}
		sort.Strings(ki.DirectCallers)
		sort.Strings(si.DirectCallers)
		if !reflect.DeepEqual(ki.DirectCallers, si.DirectCallers) {
			t.Fatalf("Impact(%q): kuzu direct %v, sqlite direct %v", q, ki.DirectCallers, si.DirectCallers)
		}
	}
}

// buildSelfRecursionKuzu writes a direct self edge (rec -> rec) and a longer
// cycle (a -> b -> a) into a Kùzu graph and returns its GraphDB.
func buildSelfRecursionKuzu(t *testing.T) gleann.GraphDB {
	t.Helper()
	db, err := kuzu.Open("")
	if err != nil {
		t.Fatalf("kuzu.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.UpsertFile("pkg/a.go", "go"); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	syms := []kuzu.SymbolNode{
		{FQN: "mod/pkg.Rec", Kind: "function", File: "pkg/a.go", Line: 10, Name: "Rec"},
		{FQN: "mod/pkg.A", Kind: "function", File: "pkg/a.go", Line: 20, Name: "A"},
		{FQN: "mod/pkg.B", Kind: "function", File: "pkg/a.go", Line: 30, Name: "B"},
	}
	for _, s := range syms {
		if err := db.UpsertSymbol(s); err != nil {
			t.Fatalf("UpsertSymbol(%s): %v", s.FQN, err)
		}
		if err := db.AddDeclares("pkg/a.go", s.FQN); err != nil {
			t.Fatalf("AddDeclares(%s): %v", s.FQN, err)
		}
	}
	for _, pair := range [][2]string{{"mod/pkg.Rec", "mod/pkg.Rec"}, {"mod/pkg.A", "mod/pkg.B"}, {"mod/pkg.B", "mod/pkg.A"}} {
		if err := db.AddCalls(pair[0], pair[1]); err != nil {
			t.Fatalf("AddCalls: %v", err)
		}
	}
	return db
}

// TestGraphDBParitySelfRecursion checks that the SQLite backend matches Kùzu
// for a direct self edge and a longer cycle.
func TestGraphDBParitySelfRecursion(t *testing.T) {
	kuzuDB := buildSelfRecursionKuzu(t)
	sqliteDB := buildSelfRecursionGraphstore(t)

	for _, fqn := range []string{"mod/pkg.Rec", "mod/pkg.A"} {
		kc, err := kuzuDB.Callees(fqn)
		if err != nil {
			t.Fatalf("kuzu Callees(%s): %v", fqn, err)
		}
		sc, err := sqliteDB.Callees(fqn)
		if err != nil {
			t.Fatalf("sqlite Callees(%s): %v", fqn, err)
		}
		if !reflect.DeepEqual(calleeFQNs(kc), calleeFQNs(sc)) {
			t.Fatalf("Callees(%s): kuzu %v, sqlite %v", fqn, calleeFQNs(kc), calleeFQNs(sc))
		}

		kr, err := kuzuDB.Callers(fqn)
		if err != nil {
			t.Fatalf("kuzu Callers(%s): %v", fqn, err)
		}
		sr, err := sqliteDB.Callers(fqn)
		if err != nil {
			t.Fatalf("sqlite Callers(%s): %v", fqn, err)
		}
		if !reflect.DeepEqual(calleeFQNs(kr), calleeFQNs(sr)) {
			t.Fatalf("Callers(%s): kuzu %v, sqlite %v", fqn, calleeFQNs(kr), calleeFQNs(sr))
		}

		ki, err := kuzuDB.Impact(fqn, 5)
		if err != nil {
			t.Fatalf("kuzu Impact(%s): %v", fqn, err)
		}
		si, err := sqliteDB.Impact(fqn, 5)
		if err != nil {
			t.Fatalf("sqlite Impact(%s): %v", fqn, err)
		}
		sort.Strings(ki.DirectCallers)
		sort.Strings(si.DirectCallers)
		sort.Strings(ki.TransitiveCallers)
		sort.Strings(si.TransitiveCallers)
		if !reflect.DeepEqual(ki.DirectCallers, si.DirectCallers) ||
			!reflect.DeepEqual(ki.TransitiveCallers, si.TransitiveCallers) {
			t.Fatalf("Impact(%s): kuzu direct=%v trans=%v, sqlite direct=%v trans=%v",
				fqn, ki.DirectCallers, ki.TransitiveCallers, si.DirectCallers, si.TransitiveCallers)
		}
	}
}

// TestGraphDBParityMultiSeedImpact: a symbol that is a direct caller of one
// resolved seed but transitive of another must be direct only, in both
// backends.
func TestGraphDBParityMultiSeedImpact(t *testing.T) {
	kuzuDB := buildMultiSeedKuzu(t)
	sqliteDB := buildMultiSeedGraphstore(t)

	ki, err := kuzuDB.Impact("Target", 3)
	if err != nil {
		t.Fatalf("kuzu Impact: %v", err)
	}
	si, err := sqliteDB.Impact("Target", 3)
	if err != nil {
		t.Fatalf("sqlite Impact: %v", err)
	}
	sort.Strings(ki.DirectCallers)
	sort.Strings(si.DirectCallers)
	sort.Strings(ki.TransitiveCallers)
	sort.Strings(si.TransitiveCallers)
	if !reflect.DeepEqual(ki.DirectCallers, si.DirectCallers) ||
		!reflect.DeepEqual(ki.TransitiveCallers, si.TransitiveCallers) {
		t.Fatalf("Impact(Target): kuzu direct=%v trans=%v, sqlite direct=%v trans=%v",
			ki.DirectCallers, ki.TransitiveCallers, si.DirectCallers, si.TransitiveCallers)
	}
}

// multiSeedSyms/Edges: C -> X -> SeedA (C transitive of SeedA) and C -> SeedB
// (C direct of SeedB); both seeds share the base name "Target".
func buildMultiSeedKuzu(t *testing.T) gleann.GraphDB {
	t.Helper()
	db, err := kuzu.Open("")
	if err != nil {
		t.Fatalf("kuzu.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.UpsertFile("pkg/a.go", "go"); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	syms := []kuzu.SymbolNode{
		{FQN: "mod/pkg.SeedA", Kind: "function", File: "pkg/a.go", Line: 10, Name: "Target"},
		{FQN: "mod/pkg.SeedB", Kind: "function", File: "pkg/a.go", Line: 20, Name: "Target"},
		{FQN: "mod/pkg.C", Kind: "function", File: "pkg/a.go", Line: 30, Name: "C"},
		{FQN: "mod/pkg.X", Kind: "function", File: "pkg/a.go", Line: 40, Name: "X"},
	}
	for _, s := range syms {
		if err := db.UpsertSymbol(s); err != nil {
			t.Fatalf("UpsertSymbol(%s): %v", s.FQN, err)
		}
		if err := db.AddDeclares("pkg/a.go", s.FQN); err != nil {
			t.Fatalf("AddDeclares(%s): %v", s.FQN, err)
		}
	}
	for _, pair := range [][2]string{
		{"mod/pkg.C", "mod/pkg.X"},
		{"mod/pkg.X", "mod/pkg.SeedA"},
		{"mod/pkg.C", "mod/pkg.SeedB"},
	} {
		if err := db.AddCalls(pair[0], pair[1]); err != nil {
			t.Fatalf("AddCalls: %v", err)
		}
	}
	return db
}

// kuzuGoldenFixture builds the Kùzu side of a golden scenario.
func kuzuGoldenFixture(t *testing.T, scenario string) gleann.GraphDB {
	switch scenario {
	case "basic":
		return buildKuzuFixture(t)
	case "resolution":
		return buildKuzuResolutionFixture(t)
	case "selfrecursion":
		return buildSelfRecursionKuzu(t)
	case "multiseed":
		return buildMultiSeedKuzu(t)
	}
	t.Fatalf("unknown golden scenario %q", scenario)
	return nil
}

// TestGraphDBGoldenKuzu regenerates the golden answers (with -update) or
// proves the committed files are what Kùzu answers today.
func TestGraphDBGoldenKuzu(t *testing.T) {
	for _, sc := range graphGoldenScenarios {
		t.Run(sc, func(t *testing.T) {
			compareGraphGolden(t, sc, kuzuGoldenFixture(t, sc), true)
		})
	}
}
