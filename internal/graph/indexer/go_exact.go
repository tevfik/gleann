package indexer

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/tools/go/packages"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// ExactResolver carries the go/types result of loading a whole Go module once
// per full build (WP-G3). It upgrades the syntactic per-file resolver to exact
// resolution: CALLS edges are mapped through types.Info Uses/Selections (so
// interface dispatch, promoted methods, aliased/dot imports, generics and
// shadowing all resolve correctly) and IMPLEMENTS edges are derived from method
// sets (types.Implements) in addition to the syntactic embedding edges.
//
// It is best-effort: LoadExact returns a nil resolver (not a fatal error) when
// the tree is not a type-checkable Go module, and any package that fails to
// type-check is simply absent from files, so those files keep the syntactic
// resolver. No load failure aborts the graph build.
type ExactResolver struct {
	module string
	root   string
	fset   *token.FileSet

	// files maps a slash-separated path relative to root to its type-checked
	// AST + info. Only packages that type-checked cleanly are present.
	files map[string]*exactFile

	// pkgPrefix maps a local (in-module) *types.Package to the FQN prefix the
	// extractor uses for it (module + "/" + dir, matching buildFQNOf). Packages
	// not in this map are external and use their import path as the prefix.
	pkgPrefix map[*types.Package]string

	// implements buckets method-set IMPLEMENTS edges by the implementor's
	// declaration file (relative path), so each file's delta carries its own.
	implements map[string][]gtypes.EdgeImplements
}

// exactFile is the type-checked view of one source file.
type exactFile struct {
	r      *ExactResolver
	file   *ast.File
	info   *types.Info
	prefix string
}

// LoadExact loads and type-checks the module rooted at root once. It returns
// (nil, nil) when root is not a Go module, and (nil, err) when the go/packages
// load itself fails; callers treat either as "fall back to the syntactic
// resolver". Packages with type errors are skipped individually (their files
// stay syntactic) rather than failing the whole load.
// exactLoadTimeout bounds the go/packages load of a full build.
const exactLoadTimeout = 2 * time.Minute

func LoadExact(module, root string) (*ExactResolver, error) {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return nil, nil // not a module: syntactic fallback
	}
	fset := token.NewFileSet()
	// go/packages runs `go list`: without a go toolchain the load fails and
	// the build stays syntactic. The indexer must never reach the network or
	// edit go.mod/go.sum, so module downloads are off and the module graph is
	// read-only; a dependency missing from the module cache makes its
	// importers fall back to the syntactic resolver. The load is bounded.
	ctx, cancel := context.WithTimeout(context.Background(), exactLoadTimeout)
	defer cancel()
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo,
		Dir:   root,
		Env:   append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local"),
		Fset:  fset,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, nil
	}
	r := &ExactResolver{
		module:     module,
		root:       root,
		fset:       fset,
		files:      make(map[string]*exactFile),
		pkgPrefix:  make(map[*types.Package]string),
		implements: make(map[string][]gtypes.EdgeImplements),
	}
	// Every package matched by ./... is part of the indexed module; anything a
	// callee references outside this set is external (stdlib / third-party).
	local := make(map[*types.Package]bool)
	for _, p := range pkgs {
		if p.Types != nil {
			local[p.Types] = true
		}
	}
	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil || len(p.Errors) > 0 || len(p.Syntax) == 0 {
			continue // package with type errors: keep the syntactic resolver
		}
		prefix := r.prefixOf(p)
		r.pkgPrefix[p.Types] = prefix
		for _, f := range p.Syntax {
			tf := fset.File(f.Pos())
			if tf == nil {
				continue
			}
			rel := relPathOf(root, tf.Name())
			r.files[rel] = &exactFile{r: r, file: f, info: p.TypesInfo, prefix: prefix}
		}
	}
	if len(r.files) == 0 {
		return nil, nil // nothing type-checked: syntactic fallback
	}
	r.computeImplements(pkgs, local)
	return r, nil
}

// prefixOf derives the extractor FQN prefix of a local package from its
// directory relative to root, exactly as buildFQNOf/buildPkgPrefixOf do, so
// exact edges land on the same symbol FQNs the declarations use.
func (r *ExactResolver) prefixOf(p *packages.Package) string {
	for _, f := range p.Syntax {
		if tf := r.fset.File(f.Pos()); tf != nil {
			return buildPkgPrefixOf(r.module, relPathOf(r.root, tf.Name()))
		}
	}
	return p.PkgPath
}

// HasFile reports whether relPath type-checked and can be resolved exactly.
func (r *ExactResolver) HasFile(relPath string) bool {
	if r == nil {
		return false
	}
	_, ok := r.files[relPath]
	return ok
}

// prefixFor returns the FQN prefix of a callee's package: the dir-based prefix
// for local packages, the import path for external ones (matching the syntactic
// resolver's import forging, so external callees stay dst_external).
func (r *ExactResolver) prefixFor(p *types.Package) string {
	if pref, ok := r.pkgPrefix[p]; ok {
		return pref
	}
	return p.Path()
}

// funcFQN renders a *types.Func in the extractor's FQN format:
// <prefix>.<Name> for functions and <prefix>.<Recv>.<Name> for methods, where
// the receiver is the bare (pointer-stripped, generic-origin) type name.
func (r *ExactResolver) funcFQN(fn *types.Func) string {
	if fn == nil || fn.Pkg() == nil {
		return "" // builtin (append, len, ...) or universe scope
	}
	prefix := r.prefixFor(fn.Pkg())
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		if recv := recvTypeName(sig.Recv().Type()); recv != "" {
			return prefix + "." + recv + "." + fn.Name()
		}
	}
	return prefix + "." + fn.Name()
}

// recvTypeName strips a pointer receiver and returns the named type's base
// name (generic instantiations keep their origin name, e.g. Stack not Stack[int]).
func recvTypeName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// calleeOfExpr resolves the callee of a call expression (or a method value) to
// its FQN using type information. It returns "" when the target is not a
// statically known function/method (dynamic func values, builtins, shadowed
// locals), so those calls produce no exact edge.
func (ef *exactFile) calleeOfExpr(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		// Uses maps to the *shadowing* object when a local redeclares a
		// package-level name, so a shadowed local func does not resolve to the
		// package-level one (it is a *types.Var, not a *types.Func).
		if fn, ok := ef.info.Uses[f].(*types.Func); ok {
			return ef.r.funcFQN(fn)
		}
		return ""
	case *ast.SelectorExpr:
		// Method call / method value / method expression: Selections gives the
		// selected method. For interface dispatch this is the interface method;
		// for a promoted (embedded) method it is the declaring type's method.
		if sel, ok := ef.info.Selections[f]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return ef.r.funcFQN(fn)
			}
			return ""
		}
		// Package-qualified call pkg.F (also aliased and dot-imported names,
		// which Uses resolves to the imported object directly).
		if fn, ok := ef.info.Uses[f.Sel].(*types.Func); ok {
			return ef.r.funcFQN(fn)
		}
		return ""
	case *ast.IndexExpr: // generic instantiation F[T](...)
		return ef.calleeOfExpr(f.X)
	case *ast.IndexListExpr: // F[T1, T2](...)
		return ef.calleeOfExpr(f.X)
	case *ast.ParenExpr:
		return ef.calleeOfExpr(f.X)
	}
	return ""
}

// collectGoCallQueriesExact is the go/types counterpart of collectGoCallQueries.
// It walks the type-checked AST and resolves every call (and method value) to
// the callee's FQN, labelling edges "exact". Caller FQNs are computed with the
// same chunk-based maps as the syntactic collector so edges attach to the same
// declared symbols (including chunker-split _part1 functions).
func collectGoCallQueriesExact(module, relPath, source string, chunks []chunking.CodeChunk, ef *exactFile) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls) {
	localFQN, splitFQN, pkgPrefix := buildGoFQNMaps(module, relPath, chunks)
	seen := make(map[string]bool)
	for _, decl := range ef.file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Body == nil {
			continue
		}
		callerFQN := goCallerFQN(funcDecl, localFQN, splitFQN, pkgPrefix)
		ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
			var calleeFQN string
			switch e := n.(type) {
			case *ast.CallExpr:
				calleeFQN = ef.calleeOfExpr(e.Fun)
			case *ast.SelectorExpr:
				// A method value (x.M) or method expression (T.M) references a
				// method even without a call; resolve it too. Duplicates with
				// the CallExpr case collapse via seen.
				if sel, ok := ef.info.Selections[e]; ok {
					if fn, ok := sel.Obj().(*types.Func); ok {
						calleeFQN = ef.r.funcFQN(fn)
					}
				}
			}
			if calleeFQN == "" || calleeFQN == callerFQN {
				return true
			}
			edgeKey := callerFQN + "→" + calleeFQN
			if seen[edgeKey] {
				return true
			}
			seen[edgeKey] = true
			nodes = append(nodes, calleeStub(calleeFQN))
			edges = append(edges, gtypes.EdgeCalls{CallerFQN: callerFQN, CalleeFQN: calleeFQN, Confidence: "exact"})
			return true
		})
	}
	return nodes, edges
}

// computeImplements derives method-set IMPLEMENTS edges: for every named
// non-interface type T and every interface I with at least one method in the
// module, T IMPLEMENTS I when T or *T implements I (types.Implements). Edges are
// bucketed by the implementor's declaration file. The empty interface is
// skipped (every type implements it, which would be noise and quadratic blow-up).
func (r *ExactResolver) computeImplements(pkgs []*packages.Package, local map[*types.Package]bool) {
	type namedType struct {
		obj    *types.TypeName
		named  *types.Named
		iface  *types.Interface // non-nil for interfaces
		prefix string
		rel    string
	}
	var concretes, ifaces []namedType
	for _, p := range pkgs {
		if p.Types == nil || len(p.Errors) > 0 || !local[p.Types] {
			continue
		}
		prefix := r.pkgPrefix[p.Types]
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			pos := r.fset.Position(tn.Pos())
			nt := namedType{obj: tn, named: named, prefix: prefix, rel: relPathOf(r.root, pos.Filename)}
			if iface, ok := named.Underlying().(*types.Interface); ok {
				if iface.NumMethods() >= 1 { // skip the empty interface
					nt.iface = iface
					ifaces = append(ifaces, nt)
				}
			} else {
				concretes = append(concretes, nt)
			}
		}
	}
	for _, c := range concretes {
		ptr := types.NewPointer(c.named)
		cFQN := c.prefix + "." + c.obj.Name()
		for _, i := range ifaces {
			if c.obj == i.obj {
				continue
			}
			if types.Implements(c.named, i.iface) || types.Implements(ptr, i.iface) {
				r.implements[c.rel] = append(r.implements[c.rel], gtypes.EdgeImplements{
					ImplFQN:    cFQN,
					IfaceFQN:   i.prefix + "." + i.obj.Name(),
					Confidence: "exact",
				})
			}
		}
	}
}

// ImplementsFor returns the method-set IMPLEMENTS edges declared in relPath.
func (r *ExactResolver) ImplementsFor(relPath string) []gtypes.EdgeImplements {
	if r == nil {
		return nil
	}
	return r.implements[relPath]
}

// dedupeImplements keeps one edge per (ImplFQN, IfaceFQN), preferring an
// "exact" (method-set) edge over an "extracted" (embedding) one so the label
// reflects the strongest evidence.
func dedupeImplements(in []gtypes.EdgeImplements) []gtypes.EdgeImplements {
	pos := make(map[string]int, len(in))
	out := make([]gtypes.EdgeImplements, 0, len(in))
	for _, e := range in {
		key := e.ImplFQN + "→" + e.IfaceFQN
		if i, ok := pos[key]; ok {
			if e.Confidence == "exact" && out[i].Confidence != "exact" {
				out[i] = e
			}
			continue
		}
		pos[key] = len(out)
		out = append(out, e)
	}
	return out
}
