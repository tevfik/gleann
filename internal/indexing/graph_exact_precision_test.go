package indexing

import (
	"context"
	"database/sql"
	"go/ast"
	"go/token"
	"go/types"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/tevfik/gleann/internal/graph/indexer"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// TestExactPrecisionOnGleann is the WP-G3 precision check on gleann's own tree:
// for 20 randomly chosen functions it compares the callers stored in the exact
// graph against go/types-derived ground truth computed here, and reports
// precision/recall for both the exact and the syntactic resolver. It is a
// measurement, so it is skipped under -short.
func TestExactPrecisionOnGleann(t *testing.T) {
	if testing.Short() || os.Getenv("GLEANN_GRAPH_PRECISION") == "" {
		t.Skip("precision benchmark: set GLEANN_GRAPH_PRECISION=1 to run")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("gleann tree is not a Go module")
	}

	exactDB := buildGraphOfTree(t, root, true)
	syntDB := buildGraphOfTree(t, root, false)
	truth := groundTruthCallers(t, root)

	// Deterministically pick 20 targets that have at least one true caller.
	var targets []string
	for fqn, callers := range truth {
		if len(callers) > 0 {
			targets = append(targets, fqn)
		}
	}
	sort.Strings(targets)
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(targets), func(i, j int) { targets[i], targets[j] = targets[j], targets[i] })
	if len(targets) > 20 {
		targets = targets[:20]
	}
	if len(targets) == 0 {
		t.Skip("no ground-truth callers found")
	}

	type score struct{ tp, reported, truth int }
	var exact, synt score
	for _, fqn := range targets {
		want := truth[fqn]
		exactSet := graphCallers(t, exactDB, fqn)
		syntSet := graphCallers(t, syntDB, fqn)
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
	if err := fullGraphSQLite(context.Background(), g, module, root, false); err != nil {
		t.Fatal(err)
	}
	return st.ReaderDB()
}

// groundTruthCallers loads the module with go/packages and, independently of the
// indexer, maps each callee FQN to the set of its caller FQNs (non-test files).
func groundTruthCallers(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: root, Fset: fset, Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Skipf("go/packages load failed: %v", err)
	}
	module := indexer.DetectGoModule(root)
	prefix := map[*types.Package]string{}
	for _, p := range pkgs {
		if p.Types == nil || len(p.Syntax) == 0 {
			continue
		}
		if tf := fset.File(p.Syntax[0].Pos()); tf != nil {
			rel, _ := filepath.Rel(root, tf.Name())
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

	truth := map[string]map[string]bool{}
	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil || len(p.Errors) > 0 {
			continue
		}
		pre := prefix[p.Types]
		for _, file := range p.Syntax {
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
					if truth[callee] == nil {
						truth[callee] = map[string]bool{}
					}
					truth[callee][callerFQN] = true
					return true
				})
			}
		}
	}
	return truth
}

// graphCallers returns the normalized non-test caller FQNs of calleeFQN stored
// in the graph database.
func graphCallers(t *testing.T, db *sql.DB, calleeFQN string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT e.src FROM edges e JOIN symbols s ON s.fqn = e.src
		WHERE e.kind = 'CALLS' AND e.dst = ? AND s.is_test = 0`, calleeFQN)
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
func prefixOf(module, relPath string) string {
	dir := filepath.Dir(relPath)
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
