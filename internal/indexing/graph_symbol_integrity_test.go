package indexing

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

func integrityFileGraph(mod string) indexer.FileGraph {
	caller := mod + ".F"
	call := func(dst string) gtypes.EdgeCalls {
		return gtypes.EdgeCalls{CallerFQN: caller, CalleeFQN: dst, Confidence: "extracted"}
	}
	return indexer.FileGraph{
		File:    gtypes.FileNode{Path: "f.go", Lang: "go"},
		Symbols: []gtypes.SymbolNode{{FQN: caller, Kind: "function", File: "f.go", Name: "F", Line: 1, EndLine: 3}},
		Calls: []gtypes.EdgeCalls{
			call(mod + ".Real"), call(mod + ".len"), call(mod + ".int64"), call("path/filepath.Join"), call(mod + ".Iface.M"),
		},
	}
}

// Issue #12: one logical declaration is one graph symbol (the chunker splits an
// oversized function into passages for search, never into symbols), and an
// in-repo call target that is not a declared symbol (builtin, conversion,
// local, guess) leaves neither a graph_nodes row nor an edge, so no phantom is
// mislabelled third-party.

const integrityModule = "example.com/integrity"

// bigFunc returns a function far above the chunker's size limit, so the
// chunker cuts it into several _partN pieces. It calls an in-repo helper, a
// standard-library function, builtins, a conversion and a local closure.
func bigFunc(name string, lines int) string {
	var b strings.Builder
	b.WriteString("func " + name + "(base string) string {\n")
	b.WriteString("\tvar acc []int\n\tlocal := func(n int) int { return n + 1 }\n")
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "\tacc = append(acc, local(%d)+len(base)+int(int64(%d)))\n", i, i)
	}
	b.WriteString("\thelper()\n\treturn filepath.Join(base, fmt.Sprint(acc))\n}\n")
	return b.String()
}

func integritySource(extra string) string {
	return "package integrity\n\nimport (\n\t\"fmt\"\n\t\"path/filepath\"\n)\n\n" + bigFunc("Big", 200) + extra
}

func writeIntegrityModule(t *testing.T, dir, bigExtra string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+integrityModule+"\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "big.go"), integritySource(bigExtra))
	writeFile(t, filepath.Join(dir, "helper.go"), "package integrity\n\nfunc helper() {}\n")
	// Calls Big from a file that never changes in the sync below.
	writeFile(t, filepath.Join(dir, "caller.go"), "package integrity\n\nfunc Caller() string { return Big(\"x\") }\n")
}

func edgeCount(t *testing.T, db *sql.DB, srcName, dstFQN string, external int) int {
	t.Helper()
	return countRows(t, db, `SELECT count(*) FROM graph_edges e
		JOIN symbols s ON s.id = e.src
		LEFT JOIN symbols d ON d.id = e.dst
		LEFT JOIN graph_nodes n ON n.id = e.dst
		WHERE s.name = ? AND coalesce(d.fqn, n.fqn) = ? AND e.dst_external = ?`, srcName, dstFQN, external)
}

func assertSymbolIntegrity(t *testing.T, db *sql.DB, wantBigLines [2]int) {
	t.Helper()
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE name GLOB '*_part[0-9]*' OR fqn GLOB '*_part[0-9]*'`); n != 0 {
		t.Errorf("%d split _partN symbols", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE name = 'Big'`); n != 1 {
		t.Fatalf("Big is %d symbols, want exactly 1", n)
	}
	var start, end int
	if err := db.QueryRow(`SELECT start_line, end_line FROM symbols WHERE name = 'Big'`).Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	if start != wantBigLines[0] || end != wantBigLines[1] {
		t.Errorf("Big spans lines %d-%d, want the whole declaration %d-%d", start, end, wantBigLines[0], wantBigLines[1])
	}
	// No in-repo phantom: every undeclared target is outside the module.
	if n := countRows(t, db, `SELECT count(*) FROM graph_nodes WHERE fqn GLOB ?`, integrityModule+"*"); n != 0 {
		t.Errorf("%d in-repo graph_nodes (phantom targets)", n)
	}
	for _, builtin := range []string{"len", "append", "int", "int64", "local"} {
		if n := countRows(t, db, `SELECT count(*) FROM graph_nodes WHERE fqn LIKE ?`, "%."+builtin); n != 0 {
			t.Errorf("phantom node for builtin/local %q", builtin)
		}
	}
	// Real edges are kept: split function -> declared helper (internal),
	// -> stdlib (external); unchanged file -> the logical symbol.
	if n := edgeCount(t, db, "Big", integrityModule+".helper", 0); n != 1 {
		t.Errorf("Big -> helper internal edges: %d, want 1", n)
	}
	if n := edgeCount(t, db, "Big", "path/filepath.Join", 1); n != 1 {
		t.Errorf("Big -> filepath.Join external edges: %d, want 1", n)
	}
	if n := edgeCount(t, db, "Caller", integrityModule+".Big", 0); n != 1 {
		t.Errorf("Caller -> Big internal edges: %d, want 1", n)
	}
	assertNoOrphans(t, db)
}

func buildIntegrityGraph(t *testing.T, bigExtra string) (*testEnv, string) {
	t.Helper()
	env := newEnv(t)
	docs := t.TempDir()
	writeIntegrityModule(t, docs, bigExtra)
	if _, err := Build(context.Background(), BuildOptions{
		Options: env.options(nil), Name: "g", DocsDir: docs, InstantOnly: true, Graph: true,
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return env, docs
}

func TestGraphSplitFunctionIsOneSymbolFullBuild(t *testing.T) {
	env, _ := buildIntegrityGraph(t, "")
	// Big opens on line 8 of the generated file and closes 205 lines later.
	assertSymbolIntegrity(t, openGraphDB(t, env), [2]int{8, 8 + 205})
}

// The incremental path (syntactic resolver, ApplyFileDelta) must keep the same
// invariants after the file holding a split function is edited.
func TestGraphSplitFunctionIsOneSymbolAfterSync(t *testing.T) {
	env, docs := buildIntegrityGraph(t, "")
	assertSymbolIntegrity(t, openGraphDB(t, env), [2]int{8, 8 + 205})

	// Grow the split function and add a declaration after it.
	var b strings.Builder
	b.WriteString("package integrity\n\nimport (\n\t\"fmt\"\n\t\"path/filepath\"\n)\n\n")
	b.WriteString(bigFunc("Big", 260))
	b.WriteString("\nfunc After() {}\n")
	writeFile(t, filepath.Join(docs, "big.go"), b.String())
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	db := openGraphDB(t, env)
	assertSymbolIntegrity(t, db, [2]int{8, 8 + 265})
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE name = 'After'`); n != 1 {
		t.Errorf("After: %d rows, want 1", n)
	}

	// Deleting the file retracts the symbol and its edges, and leaves the
	// unchanged caller without a dangling edge.
	writeFile(t, filepath.Join(docs, "big.go"), "package integrity\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	db = openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE name = 'Big' OR fqn GLOB '*_part[0-9]*'`); n != 0 {
		t.Errorf("Big survived its removal: %d rows", n)
	}
	assertNoOrphans(t, db)
	if n := countRows(t, db, `SELECT count(*) FROM graph_nodes WHERE fqn GLOB ?`, integrityModule+"*"); n != 0 {
		t.Errorf("%d in-repo graph_nodes after Big was removed", n)
	}
}

// A call into a declared symbol of another file stays internal, while a call
// that resolves to nothing declared is dropped rather than stored as external.
func TestFileGraphToDeltaDropsUnanchoredInRepoTargets(t *testing.T) {
	const mod = "example.com/m"
	fg := integrityFileGraph(mod)
	declared := map[string]bool{mod + ".Real": true, mod + ".Iface": true}
	d := fileGraphToDelta(fg, mod, "h",
		func(string) bool { return true },
		func(fqn string) bool { return declared[fqn] },
		nil)
	got := map[string]bool{}
	for _, e := range d.Edges {
		got[e.Dst+fmt.Sprintf("/ext=%v", e.DstExternal)] = true
	}
	want := map[string]bool{
		mod + ".Real/ext=false":       true, // declared in another file: internal
		mod + ".Iface/ext=false":      true, // interface method call bound to the declared interface
		"path/filepath.Join/ext=true": true, // third-party/stdlib: external
	}
	if len(got) != len(want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing edge %s in %v", k, got)
		}
	}
}
