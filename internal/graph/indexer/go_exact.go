package indexer

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
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
// sets (types.Implements) in addition to the syntactic embedding edges. The
// load includes the _test.go files of each package, so a test's calls through a
// receiver, a variable or an interface resolve exactly as well.
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

	// modules is the set of Go modules the resolver covers (a full load covers
	// every module under the index root, a sync the modules of the loaded
	// directories). The extractor reads it to prefix each file with the real
	// module path, also for files that fall back to the syntactic resolver.
	modules *ModuleIndex

	// partial is set by LoadExactDirs: only some packages of the module were
	// type-checked, so the method-set IMPLEMENTS edges cover only the
	// interfaces this load saw (JudgedInterface).
	partial bool
	// judgedPrefix holds the FQN prefixes of the packages a partial load
	// type-checked from source (every interface of them was compared), and
	// judgedIface the FQNs of the interfaces it compared in the packages they
	// import (exported ones only: those come from export data).
	judgedPrefix map[string]bool
	judgedIface  map[string]bool
	// skipped counts the packages of a partial load that have Go files but
	// did not type-check; firstErr is the first error of the first of them.
	skipped  int
	firstErr string
	// failedFiles lists the source files (slash paths relative to root) of
	// the skipped packages: the files that keep the syntactic resolver
	// because their package had errors, as opposed to files excluded by
	// build tags (which are not part of the package at all).
	failedFiles []string
}

// exactFile is the type-checked view of one source file.
type exactFile struct {
	r      *ExactResolver
	file   *ast.File
	info   *types.Info
	prefix string
}

// exactLoadTimeout bounds the go/packages load of a full build.
const exactLoadTimeout = 2 * time.Minute

// exactSyncLoadTimeout bounds one go/packages load of an incremental sync
// (LoadExactDirs). With a warm Go build cache such a load takes about a second;
// the bound is for a cold one, where `go list -export` first compiles the
// dependencies of the changed packages. What it compiled before the bound is
// cached, so the next sync gets further.
const exactSyncLoadTimeout = time.Minute

// errNothingTypeChecked is returned by LoadExact when root is a Go module but
// no package of it type-checked, so no file can be resolved exactly.
var errNothingTypeChecked = errors.New("no Go package type-checked")

// LoadError reports a failed exact load together with the source files of the
// packages that did not type-check (slash paths relative to the root). An
// incremental sync reads them to retry those files once their packages are
// fixed; files excluded by build tags are not among them. It wraps the load's
// error (errors.Is/As keep working).
type LoadError struct {
	Err         error
	FailedFiles []string
}

func (e *LoadError) Error() string { return e.Err.Error() }
func (e *LoadError) Unwrap() error { return e.Err }

// exactLoadEnv is the environment `go list` runs in. The indexer must never
// reach the network, edit go.mod/go.sum or write the module cache:
//   - GOPROXY=off disables module downloads, GONOPROXY=none and an empty
//     GOPRIVATE stop the user's private-module patterns from bypassing it:
//     a GOPRIVATE/GONOPROXY match is fetched straight from its VCS even with
//     GOPROXY=off, contacting the network and writing the module cache on
//     the way (review G4). GONOSUMDB=* keeps checksum-database lookups off
//     the network too, without breaking the use of a toolchain already in
//     the module cache (GOSUMDB=off refuses to verify it).
//   - The module graph is read-only: -mod=readonly, or -mod=vendor when the
//     tree vendors its dependencies (-mod=readonly cannot load a vendored
//     package that is missing from the module cache). The user's other
//     GOFLAGS — -tags=... above all — are preserved; only their -mod choice
//     is replaced, so the load type-checks the same files the user builds.
//
// GOTOOLCHAIN is left as the user has it. The default (auto) lets a go command
// older than the module's go line run the toolchain the module asks for when
// that toolchain is already in the module cache; with GOPROXY=off a toolchain
// that is not there is not fetched, the load fails and the build stays
// syntactic. Forcing GOTOOLCHAIN=local here made that load fail on every
// machine whose installed go is older than go.mod's go line, although `go
// build` works there.
func exactLoadEnv(environ []string, root string) []string {
	var userFlags string
	out := make([]string, 0, len(environ)+5)
	for _, e := range environ {
		switch {
		case strings.HasPrefix(e, "GOFLAGS="):
			userFlags = strings.TrimPrefix(e, "GOFLAGS=") // the last entry wins, as in exec
		case strings.HasPrefix(e, "GOPROXY="), strings.HasPrefix(e, "GOPRIVATE="),
			strings.HasPrefix(e, "GONOPROXY="), strings.HasPrefix(e, "GONOSUMDB="):
			// Replaced below; the user's values must not open a route out.
		default:
			out = append(out, e)
		}
	}
	var flags []string
	skipValue := false
	for _, f := range strings.Fields(userFlags) {
		if skipValue {
			skipValue = false
			continue
		}
		switch {
		case f == "-mod":
			skipValue = true
		case strings.HasPrefix(f, "-mod="):
			// Replaced by the offline read-only choice below.
		default:
			flags = append(flags, f)
		}
	}
	mod := "-mod=readonly"
	if _, err := os.Stat(filepath.Join(root, "vendor", "modules.txt")); err == nil {
		mod = "-mod=vendor"
	}
	flags = append(flags, mod)
	return append(out,
		"GOPROXY=off",
		"GOPRIVATE=",
		"GONOPROXY=none",
		"GONOSUMDB=*",
		"GOFLAGS="+strings.Join(flags, " "),
	)
}

// exactLoadEnvForModule is exactLoadEnv rooted at one module's directory, with
// GOWORK pinned explicitly so a module is loaded in the workspace discovered
// under the index root (or outside any workspace when none covers it). Pinning
// it keeps a go.work above the index root from changing the load, and keeps a
// nested module independent of a root module's workspace.
//
// In workspace mode Go consults only the workspace vendor directory, not the
// module's own vendor directory: the -mod choice must be the workspace's. A
// workspace without vendor/modules.txt therefore loads with -mod=readonly,
// even when the module itself vendors its dependencies.
func exactLoadEnvForModule(environ []string, modRoot, workFile string) []string {
	env := exactLoadEnv(environ, modRoot)
	if workFile == "" {
		return setEnvValue(env, "GOWORK", "off")
	}
	mod := "-mod=readonly"
	if _, err := os.Stat(filepath.Join(filepath.Dir(workFile), "vendor", "modules.txt")); err == nil {
		mod = "-mod=vendor"
	}
	return setEnvValue(replaceModFlag(env, mod), "GOWORK", workFile)
}

// setEnvValue replaces every existing key= entry with key=value (the last entry
// wins in an exec environment) without modifying env.
func setEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, key+"=") {
			out = append(out, e)
		}
	}
	return append(out, key+"="+value)
}

// replaceModFlag replaces the -mod choice of the GOFLAGS entry with mod,
// keeping every other flag (the user's -tags included). exactLoadEnv always
// writes one GOFLAGS entry.
func replaceModFlag(env []string, mod string) []string {
	for i, e := range env {
		if !strings.HasPrefix(e, "GOFLAGS=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(e, "GOFLAGS="))
		out := make([]string, 0, len(fields)+1)
		skipValue := false
		for _, f := range fields {
			if skipValue {
				skipValue = false
				continue
			}
			switch {
			case f == "-mod":
				skipValue = true
			case strings.HasPrefix(f, "-mod="):
			default:
				out = append(out, f)
			}
		}
		env[i] = "GOFLAGS=" + strings.Join(append(out, mod), " ")
	}
	return env
}

// exactPackageKind classifies one package of a packages.Load result taken with
// Tests: true. The loader then returns up to four packages per directory: the
// plain package, its test variant (the same package compiled with its internal
// _test.go files, "p [p.test]"), the external test package (package foo_test,
// "p_test [p.test]") and the synthetic test main binary go generates ("p.test",
// a main package whose only syntax is a _testmain.go in GOCACHE).
//
// ForTest is read from the load, so the mode of every exact load includes
// packages.NeedForTest.
type exactPackageKind int

const (
	// exactPlain is a package without its test files: the production view.
	exactPlain exactPackageKind = iota
	// exactInternalTest is "p [p.test]": p compiled with its internal
	// _test.go files. It holds the production files too.
	exactInternalTest
	// exactExternalTest is "p_test [p.test]": the external test package,
	// whose _test.go files live in the directory of p.
	exactExternalTest
	// exactTestMain is the generated runner. It declares no symbol of the
	// tree and its syntax file is a GOCACHE path: it is never resolved.
	exactTestMain
)

// exactLoadPaths is the PkgPath set exactKindOf needs to tell the generated
// test runner from a real main package whose path ends ".test".
func exactLoadPaths(pkgs []*packages.Package) map[string]bool {
	paths := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		if p.PkgPath != "" {
			paths[p.PkgPath] = true
		}
	}
	return paths
}

// exactKindOf reports which variant of a package p is. paths holds the PkgPath
// of every package of the same load: the generated test runner is the only
// main package whose path minus ".test" is another package of the load. A real
// main package in a directory named runner.test, or the root package of a
// module whose path is example.com/zz.test, has no such sibling package and
// stays exactPlain, so it is resolved exactly instead of being dropped. A
// package literally named runner.test does get its own generated runner,
// PkgPath "module/runner.test.test", whose base is present: that one is
// excluded as it should be.
func exactKindOf(p *packages.Package, paths map[string]bool) exactPackageKind {
	if p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test") &&
		paths[strings.TrimSuffix(p.PkgPath, ".test")] {
		return exactTestMain
	}
	switch {
	case p.ForTest == "":
		return exactPlain
	case p.PkgPath == p.ForTest:
		return exactInternalTest
	default:
		return exactExternalTest
	}
}

// exactClean reports whether p type-checked, so its files can be resolved
// exactly.
func exactClean(p *packages.Package) bool {
	return p.Types != nil && p.TypesInfo != nil && len(p.Errors) == 0 && len(p.Syntax) > 0
}

// exactSelection is the split of one packages.Load(Tests: true) result.
//
// Residual IMPLEMENTS limitation (detailed on exactImplementsTypes): a type
// declared only in a package's internal _test.go is judged from its test
// variant, while an interface of an importing package is judged from the plain
// package. The type's method signature and the interface name different
// *types.Named objects, so the cross-package X IMPLEMENTS q.IQ edge is not
// derived. The same package's interfaces, and an external p_test type when p
// has no internal tests, are unaffected.
type exactSelection struct {
	// files holds the packages whose Syntax the load records. The test
	// variant of a package with test files stands in for the plain package
	// (it holds the production files AND the _test.go files, so every file is
	// recorded once), unless the variant did not type-check: then the plain
	// package keeps the production files exact and only the broken test files
	// are reported. The external test package adds its own files.
	files []*packages.Package
	// types holds the packages whose declared types feed the method-set
	// IMPLEMENTS edges: the plain package of every package that compiled (the
	// production view) and the test packages whose _test.go files resolve
	// exactly (they contribute their test-only declarations).
	types []*packages.Package
	// failed holds the packages with type errors: their files keep the
	// syntactic resolver and are the retry set of an incremental sync.
	failed []*packages.Package
}

// selectExactPackages partitions a load result taken with Tests: true into the
// packages that resolve files exactly, the ones that feed IMPLEMENTS and the
// ones that failed. A package with only _test.go files is left out entirely: it
// has no production view (its plain package reports no Go files), the full
// load skips it too, and a directory load of it yields no resolver, so a build
// and a sync agree on the syntactic fallback. The generated test main is
// dropped so its GOCACHE _testmain.go never enters r.files, FailedFiles or
// computeImplements.
func selectExactPackages(pkgs []*packages.Package) exactSelection {
	var sel exactSelection
	paths := exactLoadPaths(pkgs)
	hasProd := make(map[string]bool, len(pkgs))      // PkgPath -> a plain package with Go files exists
	cleanVariant := make(map[string]bool, len(pkgs)) // PkgPath -> a clean internal test variant exists
	for _, p := range pkgs {
		switch exactKindOf(p, paths) {
		case exactPlain:
			if len(p.GoFiles) > 0 || len(p.CompiledGoFiles) > 0 {
				hasProd[p.PkgPath] = true
			}
		case exactInternalTest:
			if exactClean(p) {
				cleanVariant[p.PkgPath] = true
			}
		}
	}
	for _, p := range pkgs {
		kind := exactKindOf(p, paths)
		if kind == exactTestMain {
			continue // generated runner: never a file, a type or a retry entry
		}
		if kind == exactPlain && !hasProd[p.PkgPath] {
			// No buildable Go file: a directory build tags exclude or one
			// holding only _test.go files. "./..." has no production view of
			// it, so it is not a skipped package either.
			continue
		}
		if kind != exactPlain && !hasProd[p.ForTest] {
			continue // a test package next to no production files
		}
		if kind == exactPlain && cleanVariant[p.PkgPath] {
			continue // its files come from the clean test variant
		}
		if !exactClean(p) {
			sel.failed = append(sel.failed, p)
			continue
		}
		sel.files = append(sel.files, p)
	}
	// The IMPLEMENTS sources: the plain package of every package that
	// compiled, plus the test packages that resolve files.
	for _, p := range pkgs {
		kind := exactKindOf(p, paths)
		if kind == exactTestMain || !exactClean(p) {
			continue
		}
		if kind != exactPlain && !hasProd[p.ForTest] {
			continue
		}
		sel.types = append(sel.types, p)
	}
	return sel
}

// exactFailedFiles returns the source files a retry must cover for a package
// that did not type-check. A failed test variant reports only its _test.go
// files: the production files are covered by the plain package, which compiled.
func exactFailedFiles(p *packages.Package) []string {
	if p.ForTest == "" || p.PkgPath != p.ForTest {
		return p.GoFiles
	}
	out := make([]string, 0, len(p.GoFiles))
	for _, f := range p.GoFiles {
		if strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

// exactLoadError builds the "nothing type-checked" error of a load whose
// packages all failed, naming the first type error. It returns nil when no
// package reported an error: the module simply holds no Go package.
func exactLoadError(failed []*packages.Package) error {
	for _, p := range failed {
		if len(p.Errors) > 0 {
			return fmt.Errorf("%w: %d packages, first error: %s", errNothingTypeChecked, len(failed), p.Errors[0].Msg)
		}
	}
	return nil
}

// LoadExact loads and type-checks every Go module discovered under root once
// (nested submodules and go.work workspace modules included). The load includes
// the _test.go files of each package (Tests: true), so calls a test makes
// through a receiver, a variable or an interface resolve exactly too; a broken
// _test.go keeps only its own file syntactic, never the package's production
// files. It returns (nil, nil) when root holds no Go module or no module holds
// a Go package, and (nil, err) when every load fails or no package
// type-checks; callers treat either as "fall back to the syntactic resolver"
// and report the error. A load that fails for one module while another
// succeeds returns the combined resolver together with the error, so the
// failed module's files stay syntactic and the others keep exact resolution.
// Packages with type errors are skipped individually (their files stay
// syntactic) rather than failing the whole load.
func LoadExact(module, root string) (*ExactResolver, error) {
	mods, err := DiscoverModules(root)
	if err != nil {
		return nil, fmt.Errorf("discovering Go modules: %w", err)
	}
	return LoadExactModules(mods, module)
}

// LoadExactModules is LoadExact for a caller that already discovered the
// modules of the tree (fullGraphSQLite does, to prefix every file), so the tree
// is not walked twice.
func LoadExactModules(mods *ModuleIndex, fallback string) (*ExactResolver, error) {
	return loadExactModules(mods, fallback, exactLoadTimeout)
}

// loadExactModules loads each module with its own root and environment and
// merges the results into one resolver rooted at the index root.
func loadExactModules(mods *ModuleIndex, fallback string, timeout time.Duration) (*ExactResolver, error) {
	if mods.Empty() {
		return nil, nil
	}
	combined := newExactResolver(mods, fallback, false)
	var errs []error
	for _, m := range mods.Modules() {
		r, err := loadExactModule(m, timeout)
		if err != nil {
			errs = append(errs, fmt.Errorf("loading module %s: %w", m.Path, err))
			continue
		}
		if r == nil {
			continue
		}
		combined.mergeModule(r, m)
	}
	if len(combined.files) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, nil
	}
	if len(errs) > 0 {
		return combined, errors.Join(errs...)
	}
	return combined, nil
}

// loadExactModule type-checks the module m, rooted at m.Dir. The environment is
// derived from that directory, so a vendored module gets -mod=vendor while a
// sibling module gets -mod=readonly, and the workspace is pinned to the one
// discovered under the index root.
//
// The load includes the test files (Tests: true): a call a test makes through a
// receiver, a variable or an interface is exactly the call the syntactic
// resolver drops, so _test.go files need go/types too. The load returns the
// plain packages and their test variants; selectExactPackages decides which one
// supplies a package's files (see there).
func loadExactModule(m Module, timeout time.Duration) (*ExactResolver, error) {
	fset := token.NewFileSet()
	// go/packages runs `go list`: without a go toolchain the load fails and
	// the build stays syntactic. A dependency missing from the module cache
	// makes its importers fall back to the syntactic resolver (exactLoadEnv).
	// The load is bounded.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo | packages.NeedForTest,
		Dir:   m.Dir,
		Env:   exactLoadEnvForModule(os.Environ(), m.Dir, m.Work),
		Fset:  fset,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading Go packages: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, nil
	}
	r := &ExactResolver{
		module:     m.Path,
		root:       m.Dir,
		fset:       fset,
		files:      make(map[string]*exactFile),
		pkgPrefix:  make(map[*types.Package]string),
		implements: make(map[string][]gtypes.EdgeImplements),
	}
	// Register the prefix of every package of the load, the plain view of a
	// package with test files included: a package without test files imports
	// that plain view, so a call from it resolves the callee to p.Types, while
	// a call in a test variant or the external test package resolves it to the
	// variant. Both map to the same FQN prefix, which is what an exact edge
	// needs. The generated test main is left out.
	paths := exactLoadPaths(pkgs)
	for _, p := range pkgs {
		if p.Types == nil || exactKindOf(p, paths) == exactTestMain {
			continue
		}
		r.pkgPrefix[p.Types] = r.prefixOf(p)
	}
	sel := selectExactPackages(pkgs)
	for _, p := range sel.files {
		r.addSyntaxFiles(p, r.pkgPrefix[p.Types])
	}
	if len(r.files) == 0 {
		// Nothing type-checked: syntactic fallback. Name the first error so
		// the caller can say why (e.g. a go line newer than the Go release
		// gleann was built with).
		if err := exactLoadError(sel.failed); err != nil {
			return nil, err
		}
		return nil, nil // packages without Go files
	}
	r.addExactImplements(sel.types, nil)
	return r, nil
}

// newExactResolver returns an empty resolver covering mods. partial marks a
// resolver that type-checked only some packages of the modules (LoadExactDirs).
func newExactResolver(mods *ModuleIndex, fallback string, partial bool) *ExactResolver {
	r := &ExactResolver{
		module:     fallback,
		root:       mods.Root(),
		files:      make(map[string]*exactFile),
		pkgPrefix:  make(map[*types.Package]string),
		implements: make(map[string][]gtypes.EdgeImplements),
		modules:    mods,
		partial:    partial,
	}
	if partial {
		r.judgedPrefix = make(map[string]bool)
		r.judgedIface = make(map[string]bool)
	}
	return r
}

// mergeModule copies the per-module resolver src into r, rewriting its
// module-relative file keys to index-root-relative ones. The exactFile values
// keep pointing at src, whose pkgPrefix holds the prefixes of their packages.
func (r *ExactResolver) mergeModule(src *ExactResolver, m Module) {
	for rel, ef := range src.files {
		r.files[joinModuleRel(m.Rel, rel)] = ef
	}
	for pkg, prefix := range src.pkgPrefix {
		r.pkgPrefix[pkg] = prefix
	}
	for rel, edges := range src.implements {
		top := joinModuleRel(m.Rel, rel)
		r.implements[top] = append(r.implements[top], edges...)
	}
}

// mergePartial is mergeModule for a partial load: it also merges the skipped
// package count, the retry set and the judged-interface sets.
func (r *ExactResolver) mergePartial(src *ExactResolver, m Module) {
	r.mergeModule(src, m)
	r.skipped += src.skipped
	if r.firstErr == "" {
		r.firstErr = src.firstErr
	}
	for _, f := range src.failedFiles {
		r.failedFiles = append(r.failedFiles, joinModuleRel(m.Rel, f))
	}
	for prefix := range src.judgedPrefix {
		r.judgedPrefix[prefix] = true
	}
	for iface := range src.judgedIface {
		r.judgedIface[iface] = true
	}
}

// joinModuleRel joins a module directory (relative to the index root) and a
// path relative to that module, yielding an index-root-relative slash path.
func joinModuleRel(dir, rel string) string {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" || rel == "." {
		return dir
	}
	if dir == "" {
		return rel
	}
	return dir + "/" + rel
}

// Modules returns the Go modules this resolver covers, or nil for a nil
// resolver. The extractor uses it to prefix every file with the real module
// path, including files that keep the syntactic resolver.
func (r *ExactResolver) Modules() *ModuleIndex {
	if r == nil {
		return nil
	}
	return r.modules
}

// prefixOf derives the extractor FQN prefix of a local package from its
// directory relative to root, exactly as buildFQNOf/buildPkgPrefixOf do, so
// exact edges land on the same symbol FQNs the declarations use. The
// directory comes from GoFiles, the original sources: a cgo package's syntax
// files are the cgo-generated ones in GOCACHE, whose paths would corrupt the
// prefix (review G2). When no source file lies inside the root, the package's
// import path is the prefix; for a package of the indexed module the two
// agree.
func (r *ExactResolver) prefixOf(p *packages.Package) string {
	for _, f := range p.GoFiles {
		rel := relPathOf(r.root, f)
		if !strings.HasPrefix(rel, "..") {
			return buildPkgPrefixOf(r.module, rel)
		}
	}
	return p.PkgPath
}

// addSyntaxFiles records the type-checked ASTs of p under their original
// repo-relative paths. go/packages parses the compiled files: for a cgo
// package those are the cgo-generated ones in GOCACHE, named by content hash
// (CompiledGoFiles does not name their sources either). Their declarations
// keep the original positions — cgo emits //line directives — so a generated
// file is mapped back through its first declaration anchored in one of the
// package's GoFiles; generated files without such a declaration
// (_cgo_gotypes.go, _cgo_import.go) declare no source symbol and are skipped.
func (r *ExactResolver) addSyntaxFiles(p *packages.Package, prefix string) {
	goFiles := make(map[string]bool, len(p.GoFiles))
	for _, f := range p.GoFiles {
		goFiles[relPathOf(r.root, f)] = true
	}
	for _, f := range p.Syntax {
		tf := r.fset.File(f.Pos())
		if tf == nil {
			continue
		}
		rel := relPathOf(r.root, tf.Name())
		if strings.HasPrefix(rel, "..") {
			rel = r.cgoSourceOf(f, goFiles)
			if rel == "" {
				continue
			}
		}
		r.files[rel] = &exactFile{r: r, file: f, info: p.TypesInfo, prefix: prefix}
	}
}

// cgoSourceOf maps one cgo-generated syntax file back to the original source
// it was generated from: the first declaration whose //line-adjusted position
// names one of the package's GoFiles.
func (r *ExactResolver) cgoSourceOf(f *ast.File, goFiles map[string]bool) string {
	for _, decl := range f.Decls {
		pos := r.fset.Position(decl.Pos())
		if pos.Filename == "" {
			continue
		}
		if rel := relPathOf(r.root, pos.Filename); goFiles[rel] {
			return rel
		}
	}
	return ""
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
// declared symbols (a chunker-split function is one logical symbol).
func collectGoCallQueriesExact(module, relPath, source string, chunks []chunking.CodeChunk, ef *exactFile) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls) {
	localFQN, pkgPrefix := buildGoFQNMaps(module, relPath, chunks)
	seen := make(map[string]bool)
	for _, decl := range ef.file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Body == nil {
			continue
		}
		callerFQN := goCallerFQN(funcDecl, localFQN, pkgPrefix)
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

// namedType is a package-level named type considered for method-set
// IMPLEMENTS: a concrete type (iface nil) or an interface with at least one
// method.
type namedType struct {
	obj    *types.TypeName
	named  *types.Named
	iface  *types.Interface // non-nil for interfaces
	prefix string
	rel    string
}

// exactImplementsTypes lists the method-set IMPLEMENTS sources of pkgs
// (exactSelection.types): the production declarations of every plain package
// that compiled and the _test.go declarations of the clean test packages.
// Judging the production declarations from the plain package keeps the edges of
// non-test files exactly the ones a load without test files derives (same type
// objects, no duplicate edge, no judging a variant's T against the plain
// package's I or vice versa). A test variant's interfaces are compared too,
// production ones included: a test-only type's method signature names the
// variant's objects, so it matches the variant's copy of an interface rather
// than the plain one. The duplicate FQN pair that results collapses in
// addImplements.
//
// Residual cross-package limitation: a type X declared only in p's internal
// _test.go has a method M() T whose signature names the test variant's
// p [p.test].T. An interface q.IQ = interface{ M() p.T } declared in an
// importing package q names the plain p.T. types.Implements requires the
// method signatures to be identical, and the two p.T objects are distinct
// *types.Named, so the X IMPLEMENTS q.IQ edge is not derived. This holds
// whether or not q has test files of its own: q's variant copy of the
// interface still names the plain p.T, so it does not heal the pair. It affects
// only the internal-test variant of p: when p has no internal tests, an
// external p_test type imports the plain p and the objects are identical, so
// its edge is derived. The gap is a missing edge (the syntactic resolver never
// had it) and has no instance in this repository.
func (r *ExactResolver) exactImplementsTypes(pkgs []*packages.Package) (concretes, ifaces []namedType) {
	for _, p := range pkgs {
		if p.Types == nil || len(p.Errors) > 0 {
			continue
		}
		prefix, ok := r.pkgPrefix[p.Types]
		if !ok {
			continue // the generated test main
		}
		// Every package in sel.types is plain or a test package; a package
		// with ForTest set is a test variant/external test package.
		testVariant := p.ForTest != ""
		c, _ := r.namedTypes(p.Types, prefix, false, testVariant)
		concretes = append(concretes, c...)
		_, i := r.namedTypes(p.Types, prefix, false, false)
		ifaces = append(ifaces, i...)
	}
	return concretes, ifaces
}

// addExactImplements records the method-set IMPLEMENTS edges of one load's
// packages; extra holds interfaces of packages the load did not type-check from
// source (a partial load's direct imports).
func (r *ExactResolver) addExactImplements(pkgs []*packages.Package, extra []namedType) {
	concretes, ifaces := r.exactImplementsTypes(pkgs)
	r.addImplements(concretes, append(ifaces, extra...))
}

// namedTypes lists the named types declared at package level in pkg, split
// into concrete types and interfaces with at least one method. prefix is the
// FQN prefix of pkg. With exportedOnly the unexported names are left out: a
// package read from export data holds only some of them. With testsOnly only
// the declarations of _test.go files are kept: the production declarations of a
// package are judged from its plain package, so its test variant contributes
// just the test-only ones.
func (r *ExactResolver) namedTypes(pkg *types.Package, prefix string, exportedOnly, testsOnly bool) (concretes, ifaces []namedType) {
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() || (exportedOnly && !tn.Exported()) {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		pos := r.fset.Position(tn.Pos())
		rel := relPathOf(r.root, pos.Filename)
		if testsOnly && !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		nt := namedType{obj: tn, named: named, prefix: prefix, rel: rel}
		if iface, ok := named.Underlying().(*types.Interface); ok {
			if iface.NumMethods() >= 1 { // skip the empty interface
				nt.iface = iface
				ifaces = append(ifaces, nt)
			}
		} else {
			concretes = append(concretes, nt)
		}
	}
	return concretes, ifaces
}

// addImplements records T IMPLEMENTS I for every concrete type T and
// interface I of the two lists where T or *T implements I, bucketed by T's
// declaration file. One edge per (implementor file, ImplFQN, IfaceFQN) is kept:
// the same pair can match twice when a test variant's interface and the plain
// package's copy of it both accept T.
func (r *ExactResolver) addImplements(concretes, ifaces []namedType) {
	seen := make(map[string]bool, len(concretes)*4)
	for _, c := range concretes {
		ptr := types.NewPointer(c.named)
		cFQN := c.prefix + "." + c.obj.Name()
		for _, i := range ifaces {
			if c.obj == i.obj {
				continue
			}
			if !types.Implements(c.named, i.iface) && !types.Implements(ptr, i.iface) {
				continue
			}
			iFQN := i.prefix + "." + i.obj.Name()
			key := c.rel + "→" + cFQN + "→" + iFQN
			if seen[key] {
				continue
			}
			seen[key] = true
			r.implements[c.rel] = append(r.implements[c.rel], gtypes.EdgeImplements{
				ImplFQN:    cFQN,
				IfaceFQN:   iFQN,
				Confidence: "exact",
			})
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
