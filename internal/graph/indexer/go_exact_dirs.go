package indexer

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

// LoadExactDirs is LoadExact for an incremental sync: it loads only the
// packages in dirs (slash-separated directories relative to root, "." for the
// root itself) instead of the whole tree.
//
// The directories are routed to the Go module that owns them (the deepest
// enclosing module, so a nested submodule is loaded by its own go.mod), and
// each module is type-checked from that module's root with its own offline
// environment. The results are merged into one resolver rooted at root, so a
// caller keeps addressing files and directories relative to the index root.
//
// The listed packages are type-checked from source, test files included, so
// their types.Info is the one a full load produces and the CALLS edges of their
// files come out the same. The packages they import are not type-checked from
// source: go/packages reads their compiler export data (`go list -export`,
// served from the Go build cache). That is what makes the load cheap:
// type-checking the dependency closure from source, as LoadExact does, costs
// about as much for one package as for the module. The price is that `go list
// -export` compiles what the Go build cache does not hold yet, the listed
// packages included; a later `go build` of the tree reuses it.
//
// Method-set IMPLEMENTS edges are computed for the types of the listed
// packages against the interfaces this load can see: every interface of the
// listed packages and the exported interfaces of the module packages they
// import directly. Interfaces declared elsewhere in the module (in a package
// that imports a listed one, typically) are not loaded; JudgedInterface tells
// the caller which stored edges this load did not re-derive.
//
// It returns (nil, nil) when root holds no Go module or no directory belongs to
// a module, and (nil, err) when every load fails or no listed package
// type-checks (a *LoadError wrapping errNothingTypeChecked, carrying the files
// of the packages that did not type-check). A module that fails while another
// succeeds returns the merged resolver together with the error. Directories
// the Go tool's "./..." does not descend into (testdata, vendor, names starting
// with "." or "_", directories without buildable Go files) are left to the
// syntactic resolver. A listed package with errors is skipped and counted
// (Skipped, FailedFiles); a package whose _test.go files alone are broken keeps
// its production files exact and reports just those test files, so the sync
// retries them without falling back for the whole package.
func LoadExactDirs(module, root string, dirs []string) (*ExactResolver, error) {
	return loadExactDirs(module, root, dirs, exactSyncLoadTimeout)
}

// LoadExactDirsModules is LoadExactDirs for a caller that already discovered
// the modules of the tree (an incremental sync does, to route each changed
// directory), so the tree is not walked on every load of the same sync.
func LoadExactDirsModules(mods *ModuleIndex, fallback string, dirs []string) (*ExactResolver, error) {
	return loadExactDirsModules(mods, fallback, dirs, exactSyncLoadTimeout)
}

// loadExactDirs is LoadExactDirs with the bound of the go/packages load as a
// parameter.
func loadExactDirs(module, root string, dirs []string, timeout time.Duration) (*ExactResolver, error) {
	mods, err := DiscoverModules(root)
	if err != nil {
		return nil, fmt.Errorf("discovering Go modules: %w", err)
	}
	return loadExactDirsModules(mods, module, dirs, timeout)
}

// loadExactDirsModules is loadExactDirs over an already discovered module
// index.
func loadExactDirsModules(mods *ModuleIndex, module string, dirs []string, timeout time.Duration) (*ExactResolver, error) {
	if mods.Empty() {
		return nil, nil
	}
	combined := newExactResolver(mods, module, true)
	var errs []error
	var failedFiles []string
	routed := false
	for _, m := range mods.Modules() {
		relDirs := moduleDirs(mods, m, dirs)
		if len(relDirs) == 0 {
			continue
		}
		routed = true
		r, err := loadExactDirsModule(m, relDirs, timeout)
		if err != nil {
			var le *LoadError
			if errors.As(err, &le) {
				for _, f := range le.FailedFiles {
					failedFiles = append(failedFiles, joinModuleRel(m.Rel, f))
				}
			}
			errs = append(errs, fmt.Errorf("loading module %s: %w", m.Path, err))
			continue
		}
		if r == nil {
			continue
		}
		combined.mergePartial(r, m)
	}
	if !routed {
		return nil, nil // no directory belongs to a module
	}
	if len(combined.files) == 0 {
		if len(errs) > 0 {
			return nil, joinLoadErrors(errors.Join(errs...), failedFiles)
		}
		return nil, nil // nothing the full build would resolve exactly either
	}
	if len(errs) > 0 {
		return combined, joinLoadErrors(errors.Join(errs...), failedFiles)
	}
	return combined, nil
}

// loadExactDirsModule is the partial load of one module, rooted at m.Dir with
// dirs relative to that module.
func loadExactDirsModule(m Module, dirs []string, timeout time.Duration) (*ExactResolver, error) {
	patterns := dirPatterns(m.Dir, dirs)
	if len(patterns) == 0 {
		return nil, nil
	}
	fset := token.NewFileSet()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cfg := &packages.Config{
		Context: ctx,
		// No NeedDeps: only the listed packages get syntax and types.Info,
		// their imports are satisfied from export data. NeedModule tells a
		// package of this module from one of a nested or third-party module.
		// Tests: true adds the test variants, so the _test.go files of a
		// listed package resolve exactly too (a sync of a test file must not
		// fall back to the syntactic resolver whenever its directory is
		// loaded for another file).
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedModule |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedForTest,
		Dir:   m.Dir,
		Env:   exactLoadEnvForModule(os.Environ(), m.Dir, m.Work),
		Fset:  fset,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("loading Go packages: no result within %s: %w", timeout, err)
		}
		return nil, fmt.Errorf("loading Go packages: %w", err)
	}
	r := &ExactResolver{
		module:       m.Path,
		root:         m.Dir,
		fset:         fset,
		files:        make(map[string]*exactFile),
		pkgPrefix:    make(map[*types.Package]string),
		implements:   make(map[string][]gtypes.EdgeImplements),
		partial:      true,
		judgedPrefix: make(map[string]bool),
		judgedIface:  make(map[string]bool),
	}
	// The prefix of every package of the load is registered, the plain view of
	// a package with test files included: a call in a test variant or in the
	// external test package resolves its callee to the variant, a call in a
	// package without test files to the plain package. Both map to the same
	// FQN prefix (prefixOf), which is what an exact edge needs. The generated
	// test main is left out.
	paths := exactLoadPaths(pkgs)
	for _, p := range pkgs {
		if p.Types == nil || exactKindOf(p, paths) == exactTestMain {
			continue
		}
		r.pkgPrefix[p.Types] = r.prefixOf(p)
	}
	listed := make(map[*types.Package]bool) // every listed package, clean or not
	for _, p := range pkgs {
		if p.Types != nil {
			listed[p.Types] = true
		}
	}
	sel := selectExactPackages(pkgs)
	for _, p := range sel.files {
		prefix := r.pkgPrefix[p.Types]
		r.judgedPrefix[prefix] = true
		r.addSyntaxFiles(p, prefix)
	}
	for _, p := range sel.failed {
		// Package with errors: its files keep the syntactic resolver. GoFiles
		// names the original sources (for a cgo package too), so the retry set
		// holds repo paths, not GOCACHE ones, and not the files build tags
		// exclude. A failed test variant reports only its _test.go files: the
		// production files are covered by the plain package, which compiled.
		r.skipped++
		if r.firstErr == "" && len(p.Errors) > 0 {
			r.firstErr = p.Errors[0].Error()
		}
		for _, f := range exactFailedFiles(p) {
			r.failedFiles = append(r.failedFiles, relPathOf(m.Dir, f))
		}
	}
	if len(r.files) == 0 {
		if r.skipped > 0 {
			return nil, &LoadError{
				Err:         fmt.Errorf("%w: %d packages, first error: %s", errNothingTypeChecked, r.skipped, r.firstErr),
				FailedFiles: r.FailedFiles(),
			}
		}
		return nil, nil // nothing the full build would resolve exactly either
	}

	// Method-set IMPLEMENTS: the types of the listed packages against their
	// own interfaces and the exported interfaces of their direct imports
	// inside the module. A direct import is complete in export data; a
	// package reached only through another one holds just the objects that
	// one refers to, so it is not judged.
	concretes, ifaces := r.exactImplementsTypes(sel.types)
	deps := make(map[string]*packages.Package)
	for _, p := range sel.types {
		if p.ForTest != "" {
			continue // the imports of _test.go files are not re-judged here
		}
		for _, imp := range p.Imports {
			if imp.Types == nil || listed[imp.Types] || !imp.Types.Complete() || len(imp.Errors) > 0 {
				continue
			}
			// Only packages of the indexed module are judged, the same set
			// the full build's "./..." matches from the module root.
			if imp.Module == nil || imp.Module.Path != m.Path {
				continue // standard library, third-party or another workspace module
			}
			deps[imp.PkgPath] = imp
		}
	}
	depPaths := make([]string, 0, len(deps))
	for path := range deps {
		depPaths = append(depPaths, path)
	}
	sort.Strings(depPaths) // deterministic edge order
	for _, path := range depPaths {
		// A package of the module that was not listed: its FQN prefix is its
		// import path (prefixFor), which is module + "/" + directory.
		_, i := r.namedTypes(deps[path].Types, path, true, false)
		for _, it := range i {
			r.judgedIface[it.prefix+"."+it.obj.Name()] = true
		}
		ifaces = append(ifaces, i...)
	}
	r.addImplements(concretes, ifaces)
	return r, nil
}

// moduleDirs routes index-root-relative directories to one module and returns
// them relative to that module ("" becomes "."). Directories owned by another
// module are left out.
func moduleDirs(mods *ModuleIndex, m Module, dirs []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, dir := range dirs {
		dir = cleanModuleRel(dir)
		owner, ok := mods.ModuleFor(dir)
		if !ok || owner.Dir != m.Dir {
			continue
		}
		rel := owner.RelPath(dir)
		if rel == "" {
			rel = "."
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// joinLoadErrors wraps a failed multi-module load in a *LoadError carrying the
// union of the failed files, so an incremental sync can retry them once their
// packages are fixed.
func joinLoadErrors(err error, failedFiles []string) error {
	if len(failedFiles) == 0 {
		return err
	}
	failed := append([]string(nil), failedFiles...)
	sort.Strings(failed)
	return &LoadError{Err: err, FailedFiles: failed}
}

// dirPatterns turns package directories (relative to root) into go list
// patterns, sorted and without duplicates. It drops the directories "./..."
// does not descend into (an element named testdata or vendor, or starting
// with "." or "_") and the ones that do not exist, so an incremental load
// resolves exactly the packages a full load would.
func dirPatterns(root string, dirs []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, dir := range dirs {
		dir = strings.Trim(filepath.ToSlash(filepath.Clean(filepath.FromSlash(dir))), "/")
		if dir == "" {
			dir = "."
		}
		if seen[dir] || strings.HasPrefix(dir, "..") {
			continue
		}
		seen[dir] = true
		if dir != "." {
			skip := false
			for _, elem := range strings.Split(dir, "/") {
				if elem == "testdata" || elem == "vendor" || strings.HasPrefix(elem, ".") || strings.HasPrefix(elem, "_") {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil || !fi.IsDir() {
			continue
		}
		if dir == "." {
			out = append(out, ".")
		} else {
			out = append(out, "./"+dir)
		}
	}
	sort.Strings(out)
	return out
}

// Partial reports whether the resolver comes from LoadExactDirs, i.e. it
// type-checked only some packages of the module.
func (r *ExactResolver) Partial() bool {
	return r != nil && r.partial
}

// JudgedInterface reports whether the load compared the module's types
// against the interface ifaceFQN when it derived the method-set IMPLEMENTS
// edges. A full load judges every interface. A partial load judges the
// interfaces of the packages it type-checked from source and the exported
// interfaces of their direct imports; an IMPLEMENTS edge to any other
// interface is neither confirmed nor refuted by it.
func (r *ExactResolver) JudgedInterface(ifaceFQN string) bool {
	if r == nil {
		return false
	}
	if !r.partial {
		return true
	}
	if r.judgedIface[ifaceFQN] {
		return true
	}
	i := strings.LastIndex(ifaceFQN, ".")
	return i > 0 && r.judgedPrefix[ifaceFQN[:i]]
}

// Skipped returns how many packages of a partial load have Go files but did
// not type-check (their files keep the syntactic resolver) and the first
// error of the first of them.
func (r *ExactResolver) Skipped() (n int, firstErr string) {
	if r == nil {
		return 0, ""
	}
	return r.skipped, r.firstErr
}

// FailedFiles returns the source files (slash paths relative to the root,
// sorted) of the packages that did not type-check: the files an incremental
// sync retries once their packages are fixed. Files excluded by build tags
// are not among them — their package type-checked, they are simply not part
// of it. When no package type-checked at all, LoadExactDirs returns the same
// list in the LoadError it returns instead of a resolver.
func (r *ExactResolver) FailedFiles() []string {
	if r == nil {
		return nil
	}
	out := append([]string(nil), r.failedFiles...)
	sort.Strings(out)
	return out
}

// MergeImplements appends extra to edges and keeps one edge per (ImplFQN,
// IfaceFQN), an "exact" one winning over an "extracted" one. The incremental
// sync uses it to carry a file's stored method-set edges over a partial load.
func MergeImplements(edges, extra []gtypes.EdgeImplements) []gtypes.EdgeImplements {
	return dedupeImplements(append(edges, extra...))
}
