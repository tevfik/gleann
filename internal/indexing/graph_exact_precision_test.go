package indexing

import (
	"context"
	"database/sql"
	"go/ast"
	"go/token"
	"go/types"
	"hash/fnv"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/tevfik/gleann/internal/graph/indexer"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// TestExactPrecisionOnGleann is the WP-G3 precision check on gleann's own tree:
// for 20 randomly chosen functions it compares the callers stored in the exact
// graph against go/types-derived ground truth computed here, and reports
// precision/recall for both the exact and the syntactic resolver. The ground
// truth includes the test files (Tests: true), so the run also measures the
// callers that live in _test.go files and, separately, the callees whose ONLY
// callers are in them: those are exactly the callers the syntactic resolver
// drops when a test calls a method through a receiver variable or an interface.
// It is a measurement, so it is skipped under -short.
func TestExactPrecisionOnGleann(t *testing.T) {
	if testing.Short() || os.Getenv("GLEANN_GRAPH_PRECISION") == "" {
		t.Skip("precision benchmark: set GLEANN_GRAPH_PRECISION=1 to run")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("gleann tree is not a Go module")
	}

	start := time.Now()
	exactDB := buildGraphOfTree(t, root, true)
	exactBuild := time.Since(start)
	start = time.Now()
	syntDB := buildGraphOfTree(t, root, false)
	syntBuild := time.Since(start)
	truth := groundTruthCallers(t, root)

	// The exact load alone, the cost a full build pays before extracting.
	module := indexer.DetectGoModule(root)
	start = time.Now()
	if r, err := indexer.LoadExact(module, root); err == nil && r != nil {
		t.Logf("TIMING exact graph build=%s syntactic graph build=%s LoadExact=%s",
			exactBuild.Round(time.Millisecond), syntBuild.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	} else {
		t.Logf("TIMING exact graph build=%s syntactic graph build=%s LoadExact failed: %v",
			exactBuild.Round(time.Millisecond), syntBuild.Round(time.Millisecond), err)
	}

	// Deterministically pick 20 targets that have at least one true
	// production caller.
	var targets []string
	for fqn, callers := range truth.nontest {
		if len(callers) > 0 {
			targets = append(targets, fqn)
		}
	}
	targets = pickTargets(targets, 20)
	if len(targets) == 0 {
		t.Skip("no ground-truth callers found")
	}
	t.Logf("GT callees: nontest=%d test=%d all=%d; sampled production targets=%v", len(truth.nontest), len(truth.test), len(truth.all), targets)

	type score struct{ tp, reported, truth int }
	var exact, synt score
	for _, fqn := range targets {
		want := truth.nontest[fqn]
		exactSet := graphCallers(t, exactDB, fqn, 0)
		syntSet := graphCallers(t, syntDB, fqn, 0)
		exact.tp += intersect(exactSet, want)
		exact.reported += len(exactSet)
		exact.truth += len(want)
		synt.tp += intersect(syntSet, want)
		synt.reported += len(syntSet)
		synt.truth += len(want)
	}

	report := func(name string, s score) (p, r float64) {
		if s.reported > 0 {
			p = float64(s.tp) / float64(s.reported)
		}
		if s.truth > 0 {
			r = float64(s.tp) / float64(s.truth)
		}
		t.Logf("PRECISION %-9s precision=%.3f recall=%.3f (tp=%d reported=%d truth=%d over %d functions)",
			name, p, r, s.tp, s.reported, s.truth, len(targets))
		return p, r
	}
	_, exactR := report("exact", exact)
	_, syntR := report("syntactic", synt)

	// Exact resolution must find at least as many true callers as the syntactic
	// resolver (it resolves a superset of the statically resolvable calls).
	if exactR < syntR {
		t.Errorf("exact recall %.3f < syntactic recall %.3f", exactR, syntR)
	}

	// Test-file callers: the same measurement, but the sampled targets have a
	// test-file caller and the graph edges are read from test-declared symbols
	// (is_test = 1). The syntactic resolver cannot follow a method call through
	// a receiver variable or an interface, so these are the edges this item
	// adds.
	var testTargets []string
	for fqn, callers := range truth.test {
		if len(callers) > 0 {
			testTargets = append(testTargets, fqn)
		}
	}
	testTargets = pickTargets(testTargets, 20)
	var exactT, syntT score
	for _, fqn := range testTargets {
		want := truth.test[fqn]
		exactSet := graphCallers(t, exactDB, fqn, 1)
		syntSet := graphCallers(t, syntDB, fqn, 1)
		exactT.tp += intersect(exactSet, want)
		exactT.reported += len(exactSet)
		exactT.truth += len(want)
		syntT.tp += intersect(syntSet, want)
		syntT.reported += len(syntSet)
		syntT.truth += len(want)
	}
	reportTest := func(name string, s score) (p, r float64) {
		if s.reported > 0 {
			p = float64(s.tp) / float64(s.reported)
		}
		if s.truth > 0 {
			r = float64(s.tp) / float64(s.truth)
		}
		t.Logf("PRECISION test-%-4s precision=%.3f recall=%.3f (tp=%d reported=%d truth=%d over %d functions)",
			name, p, r, s.tp, s.reported, s.truth, len(testTargets))
		return p, r
	}
	_, exactTestR := reportTest("exact", exactT)
	_, syntTestR := reportTest("syntactic", syntT)
	if exactTestR < syntTestR {
		t.Errorf("exact test-caller recall %.3f < syntactic %.3f", exactTestR, syntTestR)
	}

	// Callees whose only callers are in _test.go files: tests are the only
	// evidence that they are used at all, and a test calls a method through a
	// receiver far more often than a direct function call, which is what the
	// syntactic resolver drops. The measurement is reported over the tree's own
	// declarations: a standard-library symbol called only from tests (every
	// bufio.Writer.Write of a test) says nothing about the indexer.
	var testOnly, testOnlyAll []string
	for fqn, callers := range truth.all {
		if len(callers) == 0 || len(truth.nontest[fqn]) != 0 {
			continue
		}
		testOnlyAll = append(testOnlyAll, fqn)
		if strings.HasPrefix(fqn, module+".") || strings.HasPrefix(fqn, module+"/") {
			testOnly = append(testOnly, fqn)
		}
	}
	sort.Strings(testOnly)
	sort.Strings(testOnlyAll)
	var exactO, syntO score
	for _, fqn := range testOnly {
		want := truth.test[fqn]
		exactSet := graphCallers(t, exactDB, fqn, 1)
		syntSet := graphCallers(t, syntDB, fqn, 1)
		exactO.tp += intersect(exactSet, want)
		exactO.reported += len(exactSet)
		exactO.truth += len(want)
		syntO.tp += intersect(syntSet, want)
		syntO.reported += len(syntSet)
		syntO.truth += len(want)
	}
	reportOnly := func(name string, s score) (p, r float64) {
		if s.reported > 0 {
			p = float64(s.tp) / float64(s.reported)
		}
		if s.truth > 0 {
			r = float64(s.tp) / float64(s.truth)
		}
		t.Logf("PRECISION test-only-%-9s precision=%.3f recall=%.3f (tp=%d reported=%d truth=%d over %d first-party callees)",
			name, p, r, s.tp, s.reported, s.truth, len(testOnly))
		return p, r
	}
	_, exactOnlyR := reportOnly("exact", exactO)
	_, syntOnlyR := reportOnly("syntactic", syntO)
	sample := testOnly
	if len(sample) > 15 {
		sample = sample[:15]
	}
	t.Logf("TESTONLY %d first-party callees (%d including imported packages) have only _test.go callers; sample: %v",
		len(testOnly), len(testOnlyAll), sample)

	// The item's acceptance criterion: callers reached only from tests resolve
	// exactly. Before test files were type-checked, the exact graph fell back
	// to the syntactic resolver for every _test.go file, so its recall was
	// exactly the syntactic one.
	if len(testOnly) > 0 {
		if exactOnlyR <= syntOnlyR {
			t.Errorf("test-only callers: exact recall %.3f is not better than syntactic %.3f (%d callees)", exactOnlyR, syntOnlyR, len(testOnly))
		}
		if exactOnlyR < 0.95 {
			t.Errorf("test-only callers: exact recall %.3f < 0.95 over %d callees", exactOnlyR, len(testOnly))
		}
	}
}

// buildGraphOfTree builds a full format-2 graph of root into a temp store with
// exact resolution enabled or disabled, and returns the reader DB.
func buildGraphOfTree(t *testing.T, root string, exact bool) *sql.DB {
	t.Helper()
	if exact {
		t.Setenv("GLEANN_GRAPH_EXACT", "1")
	} else {
		t.Setenv("GLEANN_GRAPH_EXACT", "0")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	g := graphstore.New(st)
	module := indexer.DetectGoModule(root)
	if err := fullGraphSQLite(context.Background(), g, module, root, false, newPrinter(nil, nil)); err != nil {
		t.Fatal(err)
	}
	return st.ReaderDB()
}

// callerTruth is the go/types ground truth of a tree: for every callee FQN the
// callers that call it, split into the ones declared in a _test.go file and the
// production ones.
type callerTruth struct {
	all     map[string]map[string]bool
	test    map[string]map[string]bool
	nontest map[string]map[string]bool
}

// groundTruthCallers loads the module with go/packages and, independently of the
// indexer, maps each callee FQN to the set of its caller FQNs. The load
// includes the test files (Tests: true), so the callers declared in _test.go
// files are part of the truth; the sort of a caller into test/nontest follows
// the file that declares it.
//
// The duplicate packages a Tests: true load returns are collapsed the same way
// the indexer does: the test variant of a package ("p [p.test]") supplies a
// package's files (production and _test.go) when it type-checked, the plain
// package does when the variant is broken, the external test package
// ("p_test [p.test]") adds its own files, and the generated main ("p.test") is
// skipped. The FQN prefix of every package is its directory's (the same prefix
// the extractor derives from the file path), so the external test package —
// which the load reports under PkgPath ".../p_test" but which lives in p's
// directory — matches the declarations the extractor emits for it.
func groundTruthCallers(t *testing.T, root string) callerTruth {
	t.Helper()
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo | packages.NeedForTest,
		Dir: root, Fset: fset, Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Skipf("go/packages load failed: %v", err)
	}
	module := indexer.DetectGoModule(root)
	prefix := map[*types.Package]string{}
	for _, p := range pkgs {
		if p.Types == nil || len(p.GoFiles) == 0 {
			continue
		}
		if rel, err := filepath.Rel(root, p.GoFiles[0]); err == nil {
			prefix[p.Types] = prefixOf(module, filepath.ToSlash(rel))
		}
	}
	pkgPrefix := func(p *types.Package) string {
		if s, ok := prefix[p]; ok {
			return s
		}
		return p.Path()
	}
	funcFQN := func(fn *types.Func) string {
		if fn == nil || fn.Pkg() == nil {
			return ""
		}
		pre := pkgPrefix(fn.Pkg())
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			rt := sig.Recv().Type()
			if ptr, ok := rt.(*types.Pointer); ok {
				rt = ptr.Elem()
			}
			if named, ok := rt.(*types.Named); ok {
				return pre + "." + named.Obj().Name() + "." + fn.Name()
			}
		}
		return pre + "." + fn.Name()
	}
	var calleeOf func(info *types.Info, fun ast.Expr) string
	calleeOf = func(info *types.Info, fun ast.Expr) string {
		switch f := fun.(type) {
		case *ast.Ident:
			if fn, ok := info.Uses[f].(*types.Func); ok {
				return funcFQN(fn)
			}
		case *ast.SelectorExpr:
			if sel, ok := info.Selections[f]; ok {
				if fn, ok := sel.Obj().(*types.Func); ok {
					return funcFQN(fn)
				}
			} else if fn, ok := info.Uses[f.Sel].(*types.Func); ok {
				return funcFQN(fn)
			}
		case *ast.IndexExpr:
			return calleeOf(info, f.X)
		case *ast.IndexListExpr:
			return calleeOf(info, f.X)
		case *ast.ParenExpr:
			return calleeOf(info, f.X)
		}
		return ""
	}

	// The duplicate packages of a Tests: true load, collapsed as the indexer
	// does (see selectExactPackages in internal/graph/indexer/go_exact.go).
	isTestMain := func(p *packages.Package) bool {
		return p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test")
	}
	hasProd := map[string]bool{}
	cleanVariant := map[string]bool{}
	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil || len(p.Errors) > 0 || len(p.Syntax) == 0 {
			continue
		}
		if isTestMain(p) {
			continue
		}
		if p.ForTest == "" {
			hasProd[p.PkgPath] = true
		} else if p.PkgPath == p.ForTest {
			cleanVariant[p.PkgPath] = true
		}
	}
	var chosen []*packages.Package
	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil || len(p.Errors) > 0 || len(p.Syntax) == 0 || isTestMain(p) {
			continue
		}
		switch {
		case p.ForTest == "":
			if !hasProd[p.PkgPath] || cleanVariant[p.PkgPath] {
				continue // no files, or the clean test variant supplies them
			}
		case p.PkgPath == p.ForTest:
			if !hasProd[p.PkgPath] {
				continue // a package with only _test.go files
			}
		default:
			if !hasProd[p.ForTest] {
				continue
			}
		}
		chosen = append(chosen, p)
	}

	truth := callerTruth{
		all:     map[string]map[string]bool{},
		test:    map[string]map[string]bool{},
		nontest: map[string]map[string]bool{},
	}
	add := func(m map[string]map[string]bool, callee, caller string) {
		if m[callee] == nil {
			m[callee] = map[string]bool{}
		}
		m[callee][caller] = true
	}
	for _, p := range chosen {
		pre := prefix[p.Types]
		for _, file := range p.Syntax {
			tf := fset.File(file.Pos())
			rel := ""
			if tf != nil {
				rel = filepath.ToSlash(tf.Name())
			}
			fromTest := strings.HasSuffix(rel, "_test.go")
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				caller := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					caller = recvBase(fd.Recv.List[0].Type) + "." + caller
				}
				callerFQN := normFQN(pre + "." + caller)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					callee := calleeOf(p.TypesInfo, call.Fun)
					if callee == "" || callee == callerFQN {
						return true
					}
					add(truth.all, callee, callerFQN)
					if fromTest {
						add(truth.test, callee, callerFQN)
					} else {
						add(truth.nontest, callee, callerFQN)
					}
					return true
				})
			}
		}
	}
	return truth
}

// graphCallers returns the normalized caller FQNs of calleeFQN stored in the
// graph database, from symbols flagged is_test or not (0: production callers,
// 1: callers declared in a _test.go file).
func graphCallers(t *testing.T, db *sql.DB, calleeFQN string, isTest int) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT e.src FROM edges e JOIN symbols s ON s.fqn = e.src
		WHERE e.kind = 'CALLS' AND e.dst = ? AND s.is_test = ?`, calleeFQN, isTest)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			t.Fatal(err)
		}
		out[normFQN(src)] = true
	}
	return out
}

func intersect(a, b map[string]bool) int {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

// normFQN strips a chunker _partN suffix from the last FQN segment so graph
// caller FQNs (which may be split) match canonical ground-truth FQNs.
func normFQN(fqn string) string {
	i := strings.LastIndex(fqn, ".")
	if i < 0 {
		return fqn
	}
	last := fqn[i+1:]
	if j := strings.LastIndex(last, "_part"); j > 0 {
		if _, err := strconv.Atoi(last[j+5:]); err == nil {
			last = last[:j]
		}
	}
	return fqn[:i+1] + last
}

// prefixOf mirrors indexer.buildPkgPrefixOf for the ground-truth computation.
// The product uses the slash-only path package (fqn.go), so the mirror must not
// use filepath: on Windows filepath.Dir("a/b/c.go") returns "a\b" and the
// ground-truth prefixes would carry backslashes.
func prefixOf(module, relPath string) string {
	dir := path.Dir(strings.ReplaceAll(relPath, "\\", "/"))
	if dir == "." || dir == "/" {
		dir = ""
	}
	switch {
	case module == "":
		return dir
	case dir == "":
		return module
	default:
		return module + "/" + dir
	}
}

// recvBase mirrors the extractor's receiver name (pointer/generic stripped).
func recvBase(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return recvBase(t.X)
	case *ast.IndexExpr:
		return recvBase(t.X)
	case *ast.IndexListExpr:
		return recvBase(t.X)
	}
	return ""
}

// TestGroundTruthCallersCoversTestFiles guards the ground truth itself: without
// _test.go callers the precision benchmark would silently stop measuring the
// edges this item adds. It runs on a small fixture, not on the tree.
func TestGroundTruthCallersCoversTestFiles(t *testing.T) {
	needGoToolchain(t)
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/truth\n\ngo 1.21\n",
		"app.go": `package truth

type Service struct{}

func (s *Service) Run() int { return 1 }

type Runner interface{ Run() int }

func NewService() *Service { return &Service{} }

// Use gives the ground truth a production caller, so the test can check both
// halves of the split.
func Use(r Runner) int { return r.Run() }
`,
		"app_test.go": `package truth

import "testing"

func TestInternal(t *testing.T) {
	svc := NewService()
	_ = svc.Run()
	var r Runner = svc
	_ = r.Run()
}
`,
		"ext_test.go": `package truth_test

import (
	"testing"

	"example.com/truth"
)

type fake struct{}

func (fake) Run() int { return 2 }

func TestExternal(t *testing.T) {
	_ = truth.NewService().Run()
	var r truth.Runner = fake{}
	_ = r.Run()
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	truth := groundTruthCallers(t, root)
	for _, want := range [][2]string{
		{"example.com/truth.Service.Run", "example.com/truth.TestInternal"},
		{"example.com/truth.Runner.Run", "example.com/truth.TestInternal"},
		{"example.com/truth.Service.Run", "example.com/truth.TestExternal"},
		{"example.com/truth.Runner.Run", "example.com/truth.TestExternal"},
	} {
		if !truth.test[want[0]][want[1]] {
			t.Errorf("ground truth misses test caller %s -> %s (test callers: %v)",
				want[1], want[0], truth.test[want[0]])
		}
	}
	if len(truth.test) == 0 {
		t.Fatal("ground truth has no test-file callers")
	}
	if len(truth.nontest) == 0 {
		t.Fatal("ground truth has no production callers")
	}
}

// pickTargets returns at most n FQNs, ordered by a hash of the name. A plain
// shuffle over a sorted slice would depend on the length of the set, and the
// set is the tree under measurement: adding a function of the item under test
// must not reshuffle the whole sample (it would change every before/after
// number). A hash order only changes when the new name ranks in the first n.
func pickTargets(fqns []string, n int) []string {
	type ranked struct {
		fqn  string
		hash uint64
	}
	rankedFQNs := make([]ranked, len(fqns))
	for i, fqn := range fqns {
		h := fnv.New64a()
		_, _ = h.Write([]byte(fqn))
		rankedFQNs[i] = ranked{fqn: fqn, hash: h.Sum64()}
	}
	sort.Slice(rankedFQNs, func(i, j int) bool {
		if rankedFQNs[i].hash != rankedFQNs[j].hash {
			return rankedFQNs[i].hash < rankedFQNs[j].hash
		}
		return rankedFQNs[i].fqn < rankedFQNs[j].fqn
	})
	out := make([]string, 0, min(n, len(rankedFQNs)))
	for i := 0; i < len(rankedFQNs) && i < n; i++ {
		out = append(out, rankedFQNs[i].fqn)
	}
	return out
}

func needGoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
}

// TestGroundTruthOnGleann is a diagnostic for the precision benchmark: it
// computes the go/types ground truth of the tree alone and reports its size, so
// a change in the sampled targets is visible without rebuilding two graphs.
// Skipped unless GLEANN_GRAPH_PRECISION is set.
func TestGroundTruthOnGleann(t *testing.T) {
	if testing.Short() || os.Getenv("GLEANN_GRAPH_PRECISION") == "" {
		t.Skip("measurement: set GLEANN_GRAPH_PRECISION=1 to run")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("gleann tree is not a Go module")
	}
	start := time.Now()
	truth := groundTruthCallers(t, root)
	t.Logf("GT callees: nontest=%d test=%d all=%d (%s)", len(truth.nontest), len(truth.test), len(truth.all), time.Since(start).Round(time.Millisecond))
	var nontest, test, both int
	for fqn, callers := range truth.all {
		if len(callers) == 0 {
			continue
		}
		switch {
		case len(truth.nontest[fqn]) > 0 && len(truth.test[fqn]) > 0:
			both++
		case len(truth.nontest[fqn]) > 0:
			nontest++
		default:
			test++
		}
	}
	t.Logf("GT callees: production-only=%d test-only=%d both=%d", nontest, test, both)
}

// TestExactLoadTimingOnGleann times a warm full exact load of the tree (the
// go build cache is already populated), so a change to what the load
// type-checks shows its cost. Skipped unless GLEANN_GRAPH_PRECISION is set.
func TestExactLoadTimingOnGleann(t *testing.T) {
	if testing.Short() || os.Getenv("GLEANN_GRAPH_PRECISION") == "" {
		t.Skip("measurement: set GLEANN_GRAPH_PRECISION=1 to run")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("gleann tree is not a Go module")
	}
	module := indexer.DetectGoModule(root)
	for i := 1; i <= 3; i++ {
		start := time.Now()
		r, err := indexer.LoadExact(module, root)
		if err != nil || r == nil {
			t.Fatalf("LoadExact run %d = %v, %v", i, r, err)
		}
		t.Logf("TIMING LoadExact run %d: %s", i, time.Since(start).Round(time.Millisecond))
	}
}
