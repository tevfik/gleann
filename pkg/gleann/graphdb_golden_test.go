package gleann_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Fixtures of the golden test (every build): the SQLite side of the fixtures
// the former Kùzu/SQLite parity test used.

const (
	pfA = "mod/pkg.A"
	pfB = "mod/pkg.B"
	pfC = "mod/pkg.C"
	pfD = "mod/pkg.D"
)

// resolutionSyms and resolutionCalls are the fixture shared by the name-
// resolution parity test: Go-style FQNs ('.' and '/' qualifiers), one
// Rust-style '::' FQN, two symbols sharing the base name "Close", and one
// symbol whose FQN is exactly "StoreA.Close" (so exact-vs-suffix preference is
// observable). The call graph gives every seed an edge, so each step of the
// three-step lookup is exercised.
var resolutionSyms = []struct{ fqn, name, file, kind string }{
	{"pkg/a.StoreA.Close", "Close", "pkg/a/store.go", "method"},
	{"pkg/b.StoreB.Close", "Close", "pkg/b/store.go", "method"},
	{"StoreA.Close", "Close", "pkg/extra.go", "method"},
	{"pkg/a.WorkerA.Run", "Run", "pkg/a/worker.go", "method"},
	{"pkg/b.WorkerB.Run", "Run", "pkg/b/worker.go", "method"},
	{"pkg/extra.Extra", "Extra", "pkg/extra.go", "function"},
	{"mod/util.Search", "Search", "mod/util/search.go", "function"},
	{"mod/pkg.Lookup", "Lookup", "mod/pkg/lookup.go", "function"},
	{"crate::mod::Thing", "Thing", "crate/mod.rs", "method"},
	{"crate::mod::Caller", "Caller", "crate/mod.rs", "function"},
}

var resolutionCalls = [][2]string{
	{"pkg/a.WorkerA.Run", "pkg/a.StoreA.Close"},
	{"pkg/b.WorkerB.Run", "pkg/b.StoreB.Close"},
	{"pkg/extra.Extra", "StoreA.Close"},
	{"mod/pkg.Lookup", "mod/util.Search"},
	{"crate::mod::Caller", "crate::mod::Thing"},
}

func buildGraphstoreFixture(t *testing.T) gleann.GraphDB {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	gs := graphstore.New(st)
	sym := func(fqn, name, file string, line int64) graphstore.Symbol {
		return graphstore.Symbol{
			FQN: fqn, Name: name, Kind: "function", File: file,
			StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40,
		}
	}
	must := func(d graphstore.FileDelta) {
		if err := gs.ApplyFileDelta(context.Background(), d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	// Declarations first (cross-file edges resolve), then the full deltas.
	must(graphstore.FileDelta{File: "pkg/file1.go", Symbols: []graphstore.Symbol{
		sym(pfA, "A", "pkg/file1.go", 10), sym(pfB, "B", "pkg/file1.go", 20), sym(pfC, "C", "pkg/file1.go", 30),
	}})
	must(graphstore.FileDelta{File: "pkg/file2.go", Symbols: []graphstore.Symbol{
		sym(pfD, "D", "pkg/file2.go", 10),
	}})
	must(graphstore.FileDelta{File: "pkg/file1.go", Symbols: []graphstore.Symbol{
		sym(pfA, "A", "pkg/file1.go", 10), sym(pfB, "B", "pkg/file1.go", 20), sym(pfC, "C", "pkg/file1.go", 30),
	}, Edges: []graphstore.Edge{{Src: pfA, Dst: pfB}, {Src: pfB, Dst: pfC}}})
	must(graphstore.FileDelta{File: "pkg/file2.go", Symbols: []graphstore.Symbol{
		sym(pfD, "D", "pkg/file2.go", 10),
	}, Edges: []graphstore.Edge{{Src: pfD, Dst: pfB}}})
	st.Close()

	meta := gleann.IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := gleann.NewSearcher(gleann.Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.GraphDB()
}

func buildGraphstoreResolutionFixture(t *testing.T) gleann.GraphDB {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	gs := graphstore.New(st)
	byFile := make(map[string][]graphstore.Symbol)
	edgesByFile := make(map[string][]graphstore.Edge)
	for i, s := range resolutionSyms {
		byFile[s.file] = append(byFile[s.file], graphstore.Symbol{
			FQN: s.fqn, Name: s.name, Kind: s.kind, File: s.file,
			StartLine: int64(i + 1), EndLine: int64(i + 2), StartCol: 1, ByteOffset: int64(i + 1),
		})
	}
	for _, pair := range resolutionCalls {
		file := ""
		for _, s := range resolutionSyms {
			if s.fqn == pair[0] {
				file = s.file
			}
		}
		edgesByFile[file] = append(edgesByFile[file], graphstore.Edge{Src: pair[0], Dst: pair[1]})
	}
	var files []string
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	must := func(d graphstore.FileDelta) {
		if err := gs.ApplyFileDelta(context.Background(), d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	for _, f := range files {
		must(graphstore.FileDelta{File: f, Symbols: byFile[f]})
	}
	for _, f := range files {
		must(graphstore.FileDelta{File: f, Symbols: byFile[f], Edges: edgesByFile[f]})
	}
	st.Close()

	meta := gleann.IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := gleann.NewSearcher(gleann.Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.GraphDB()
}

// buildSelfRecursionGraphstore writes the same graph into a format-2 SQLite
// store and returns the GraphDB served through a Loaded searcher.
func buildSelfRecursionGraphstore(t *testing.T) gleann.GraphDB {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	gs := graphstore.New(st)
	sym := func(fqn, name, file string, line int64) graphstore.Symbol {
		return graphstore.Symbol{FQN: fqn, Name: name, Kind: "function", File: file,
			StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40}
	}
	syms := []graphstore.Symbol{
		sym("mod/pkg.Rec", "Rec", "pkg/a.go", 10),
		sym("mod/pkg.A", "A", "pkg/a.go", 20),
		sym("mod/pkg.B", "B", "pkg/a.go", 30),
	}
	must := func(d graphstore.FileDelta) {
		if err := gs.ApplyFileDelta(context.Background(), d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	must(graphstore.FileDelta{File: "pkg/a.go", Symbols: syms})
	must(graphstore.FileDelta{File: "pkg/a.go", Symbols: syms, Edges: []graphstore.Edge{
		{Src: "mod/pkg.Rec", Dst: "mod/pkg.Rec"},
		{Src: "mod/pkg.A", Dst: "mod/pkg.B"},
		{Src: "mod/pkg.B", Dst: "mod/pkg.A"},
	}})
	st.Close()

	meta := gleann.IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := gleann.NewSearcher(gleann.Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.GraphDB()
}

func buildMultiSeedGraphstore(t *testing.T) gleann.GraphDB {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	gs := graphstore.New(st)
	sym := func(fqn, name, file string, line int64) graphstore.Symbol {
		return graphstore.Symbol{FQN: fqn, Name: name, Kind: "function", File: file,
			StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40}
	}
	syms := []graphstore.Symbol{
		sym("mod/pkg.SeedA", "Target", "pkg/a.go", 10),
		sym("mod/pkg.SeedB", "Target", "pkg/a.go", 20),
		sym("mod/pkg.C", "C", "pkg/a.go", 30),
		sym("mod/pkg.X", "X", "pkg/a.go", 40),
	}
	must := func(d graphstore.FileDelta) {
		if err := gs.ApplyFileDelta(context.Background(), d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	must(graphstore.FileDelta{File: "pkg/a.go", Symbols: syms})
	must(graphstore.FileDelta{File: "pkg/a.go", Symbols: syms, Edges: []graphstore.Edge{
		{Src: "mod/pkg.C", Dst: "mod/pkg.X"},
		{Src: "mod/pkg.X", Dst: "mod/pkg.SeedA"},
		{Src: "mod/pkg.C", Dst: "mod/pkg.SeedB"},
	}})
	st.Close()

	meta := gleann.IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := gleann.NewSearcher(gleann.Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.GraphDB()
}

// ---------------------------------------------------------------------------
// Golden answers (RFC 004 A0)
//
// The answers of the former Kùzu GraphDB backend for the fixtures above are
// frozen as JSON under testdata/graph_golden/ (Kùzu was removed in RFC 004
// A3, so they can no longer be regenerated and there is no -update).
// TestGraphDBGoldenSQLite (every build) checks the format-2 SQLite GraphDB
// against them. Set-like results are sorted before they are compared;
// ShortestPath keeps its order.
// ---------------------------------------------------------------------------

const graphGoldenDir = "testdata/graph_golden"

// graphGoldenScenarios lists the golden files, one per fixture.
var graphGoldenScenarios = []string{"basic", "resolution", "selfrecursion", "multiseed"}

func sortedCallees(in []gleann.Callee) []gleann.Callee {
	out := append([]gleann.Callee{}, in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].FQN != out[j].FQN {
			return out[i].FQN < out[j].FQN
		}
		return out[i].File < out[j].File
	})
	return out
}

func sortedEdges(in []gleann.GraphEdge) []gleann.GraphEdge {
	out := append([]gleann.GraphEdge{}, in...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Relation < b.Relation
	})
	return out
}

func sortedImpact(in *gleann.ImpactResult) *gleann.ImpactResult {
	if in == nil {
		return nil
	}
	cp := *in
	norm := func(s []string) []string {
		out := append([]string{}, s...)
		sort.Strings(out)
		return out
	}
	cp.DirectCallers = norm(cp.DirectCallers)
	cp.TestCallers = norm(cp.TestCallers)
	cp.TransitiveCallers = norm(cp.TransitiveCallers)
	cp.AffectedFiles = norm(cp.AffectedFiles)
	return &cp
}

// graphSnapshot runs the scenario's queries against db and returns the
// normalised answers keyed by "Method(args)".
func graphSnapshot(t *testing.T, db gleann.GraphDB, scenario string) map[string]any {
	t.Helper()
	out := map[string]any{}
	rec := func(key string, v any, err error) {
		if err != nil {
			out[key] = map[string]string{"error": err.Error()}
			return
		}
		// A nil slice and an empty slice are the same answer ("nothing"); the
		// backends differ only in which one they return (Kùzu: nil for
		// ShortestPath with no path, SQLite: empty).
		if ps, ok := v.([]gleann.PathStep); ok && len(ps) == 0 {
			v = []gleann.PathStep{}
		}
		switch x := v.(type) {
		case []gleann.Callee:
			v = sortedCallees(x)
		case []gleann.GraphEdge:
			v = sortedEdges(x)
		case *gleann.ImpactResult:
			v = sortedImpact(x)
		}
		out[key] = v
	}
	callees := func(sym string) {
		v, err := db.Callees(sym)
		rec(fmt.Sprintf("Callees(%s)", sym), v, err)
	}
	callers := func(sym string) {
		v, err := db.Callers(sym)
		rec(fmt.Sprintf("Callers(%s)", sym), v, err)
	}
	impact := func(sym string, depth int) {
		v, err := db.Impact(sym, depth)
		rec(fmt.Sprintf("Impact(%s,%d)", sym, depth), v, err)
	}
	switch scenario {
	case "basic":
		for _, s := range []string{pfA, pfB, pfC, pfD} {
			callees(s)
			callers(s)
		}
		for _, f := range []string{"pkg/file1.go", "pkg/file2.go", "pkg/none.go"} {
			v, err := db.SymbolsInFile(f)
			rec(fmt.Sprintf("SymbolsInFile(%s)", f), v, err)
		}
		for _, p := range []string{"mod/pkg", "B", "nomatch"} {
			v, err := db.SymbolSearch(p)
			rec(fmt.Sprintf("SymbolSearch(%s)", p), v, err)
		}
		st, err := db.Stats()
		rec("Stats()", st, err)
		impact(pfC, 5)
		impact(pfC, 1)
		impact(pfB, 5)
		impact(pfA, 3)
		for _, ft := range [][2]string{{pfA, pfC}, {pfD, pfC}, {pfC, pfA}, {pfA, pfA}} {
			v, err := db.ShortestPath(ft[0], ft[1])
			rec(fmt.Sprintf("ShortestPath(%s,%s)", ft[0], ft[1]), v, err)
		}
		for _, n := range []struct {
			fqn   string
			depth int
		}{{pfB, 1}, {pfB, 2}, {pfA, 2}, {pfC, 1}} {
			v, err := db.Neighbors(n.fqn, n.depth)
			rec(fmt.Sprintf("Neighbors(%s,%d)", n.fqn, n.depth), v, err)
		}
	case "resolution":
		for _, q := range []string{
			"Run", "WorkerA.Run", "a.StoreA.Close", "StoreA.Close",
			"Close", "Search", "mod::Thing", "NoSuchSymbol",
		} {
			callees(q)
			callers(q)
			impact(q, 3)
		}
	case "selfrecursion":
		for _, s := range []string{"mod/pkg.Rec", "mod/pkg.A", "mod/pkg.B"} {
			callees(s)
			callers(s)
			impact(s, 5)
		}
	case "multiseed":
		callers("Target")
		impact("Target", 3)
		impact("mod/pkg.SeedA", 3)
		impact("mod/pkg.SeedB", 3)
	default:
		t.Fatalf("unknown golden scenario %q", scenario)
	}
	return out
}

func marshalGolden(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	return append(b, '\n')
}

// compareGraphGolden compares the snapshot of one scenario with its golden.
func compareGraphGolden(t *testing.T, scenario string, db gleann.GraphDB) {
	t.Helper()
	got := marshalGolden(t, graphSnapshot(t, db, scenario))
	path := filepath.Join(graphGoldenDir, scenario+".json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(want) == string(got) {
		return
	}
	var w, g map[string]json.RawMessage
	_ = json.Unmarshal(want, &w)
	_ = json.Unmarshal(got, &g)
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !reflect.DeepEqual(string(w[k]), string(g[k])) {
			t.Errorf("%s: %s differs from golden\n--- golden\n%s\n--- got\n%s", scenario, k, w[k], g[k])
		}
	}
	for k := range g {
		if _, ok := w[k]; !ok {
			t.Errorf("%s: %s not in golden", scenario, k)
		}
	}
}

// graphstoreGoldenFixture builds the SQLite side of a scenario.
func graphstoreGoldenFixture(t *testing.T, scenario string) gleann.GraphDB {
	switch scenario {
	case "basic":
		return buildGraphstoreFixture(t)
	case "resolution":
		return buildGraphstoreResolutionFixture(t)
	case "selfrecursion":
		return buildSelfRecursionGraphstore(t)
	case "multiseed":
		return buildMultiSeedGraphstore(t)
	}
	t.Fatalf("unknown golden scenario %q", scenario)
	return nil
}

// TestGraphDBGoldenSQLite checks the format-2 SQLite GraphDB against the
// answers Kùzu gave for the same fixtures (RFC 004 A0). Any difference is a
// behaviour change that has to be explained in the commit that introduces it.
func TestGraphDBGoldenSQLite(t *testing.T) {
	for _, sc := range graphGoldenScenarios {
		t.Run(sc, func(t *testing.T) {
			compareGraphGolden(t, sc, graphstoreGoldenFixture(t, sc))
		})
	}
}
