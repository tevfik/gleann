package indexer

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

// exactTestFixture is a module whose production package has an internal test
// file (package app) and an external one (package app_test). Both call methods
// through a receiver variable and through an interface variable: calls the
// syntactic resolver cannot resolve (it sees no type for the receiver), so the
// files only get those edges when the exact load type-checks them.
var exactTestFixture = map[string]string{
	"go.mod": "module " + exactModule + "\n\ngo 1.21\n",
	"app.go": `package app

// Service is the production type the tests call.
type Service struct{}

// Run is called through a receiver variable and an interface variable.
func (s *Service) Run() int { return 1 }

// Runner is the production interface the tests dispatch through.
type Runner interface{ Run() int }

// NewService returns a Service, so an internal test can call a method of the
// returned value without importing anything.
func NewService() *Service { return &Service{} }

// Use takes the interface.
func Use(r Runner) int { return r.Run() }

// Base is embedded so a call through the embedding type is a promoted method.
type Base struct{}

func (Base) Ping() int { return 3 }

type Embedded struct{ Base }
`,
	"app_test.go": `package app

import "testing"

func TestInternalCalls(t *testing.T) {
	svc := NewService()
	if got := svc.Run(); got != 1 {
		t.Fatalf("Run = %d", got)
	}
	var r Runner = svc
	if got := r.Run(); got != 1 {
		t.Fatalf("Run via interface = %d", got)
	}
	e := Embedded{}
	if got := e.Ping(); got != 3 {
		t.Fatalf("Ping = %d", got)
	}
}
`,
	"ext_test.go": `package app_test

import (
	"testing"

	"example.com/app"
)

// testRunner is declared only in the external test package; it implements the
// production interface.
type testRunner struct{}

func (testRunner) Run() int { return 2 }

// call invokes a method of its own receiver: an exact edge inside the
// external test package.
func (r testRunner) call() int { return r.Run() }

func TestExternalCalls(t *testing.T) {
	svc := &app.Service{}
	if got := svc.Run(); got != 1 {
		t.Fatalf("Run = %d", got)
	}
	var r app.Runner = testRunner{}
	if got := r.Run(); got != 2 {
		t.Fatalf("Run via interface = %d", got)
	}
	if got := app.Use(r); got != 2 {
		t.Fatalf("Use = %d", got)
	}
	h := testRunner{}
	if got := h.call(); got != 2 {
		t.Fatalf("call = %d", got)
	}
}
`,
}

// exactBrokenTestFixture has a production package that type-checks and an
// internal test file that does not: the broken _test.go must not erase the
// exact resolution of the production file (the plain package still compiles).
var exactBrokenTestFixture = map[string]string{
	"go.mod": "module " + exactModule + "\n\ngo 1.21\n",
	"app.go": `package app

type Service struct{}

func (s *Service) Run() int { return 1 }

func NewService() *Service { return &Service{} }

// Drive calls a method through a receiver: exact when app.go type-checks.
func Drive() int { return NewService().Run() }
`,
	"bad_test.go": `package app

import "testing"

func TestBad(t *testing.T) { undefinedName() }
`,
}

func loadExactTestFixture(t *testing.T) (*ExactResolver, string) {
	t.Helper()
	needGo(t)
	root := writeTree(t, exactTestFixture)
	r, err := LoadExact(exactModule, root)
	if err != nil {
		t.Fatalf("LoadExact: %v", err)
	}
	if r == nil {
		t.Skip("go/packages could not type-check the fixture in this environment")
	}
	return r, root
}

// hasExactEdge reports whether fg holds an exact CALLS edge caller -> callee.
func hasExactEdge(fg FileGraph, caller, callee string) bool {
	for _, e := range fg.Calls {
		if e.CallerFQN == caller && e.CalleeFQN == callee {
			return e.Confidence == "exact"
		}
	}
	return false
}

// The exact load must type-check the internal _test.go files of a package: a
// call through a receiver variable and through an interface variable resolves
// to the method the test names, not to the empty set the syntactic resolver
// produces for them.
func TestLoadExactTypeChecksInternalTestFiles(t *testing.T) {
	r, root := loadExactTestFixture(t)
	for _, rel := range []string{"app.go", "app_test.go", "ext_test.go"} {
		if !r.HasFile(rel) {
			t.Fatalf("HasFile(%q) = false; want the load to cover it (files: %v)", rel, sortedFileKeys(r))
		}
	}
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "app_test.go"), exactTestFixture["app_test.go"], r)

	caller := exactModule + ".TestInternalCalls"
	for _, want := range []string{
		exactModule + ".NewService",
		exactModule + ".Service.Run", // receiver variable: svc.Run()
		exactModule + ".Runner.Run",  // interface variable: r.Run()
		exactModule + ".Base.Ping",   // promoted method: e.Ping()
	} {
		if !hasExactEdge(fg, caller, want) {
			t.Errorf("internal test: no exact CALLS %s -> %s; calls: %v", caller, want, callsOf(fg, caller))
		}
	}
	// The test function is a declared, anchored, test-flagged symbol.
	var sym *gtypes.SymbolNode
	for i := range fg.Symbols {
		if fg.Symbols[i].FQN == caller {
			sym = &fg.Symbols[i]
		}
	}
	if sym == nil {
		t.Fatalf("test function symbol %s missing", caller)
	}
	if sym.File != "app_test.go" || !sym.IsTest || sym.Line == 0 || sym.StartCol == 0 {
		t.Errorf("test symbol not anchored/flagged: %+v", sym)
	}
}

// An external _test package (package app_test) is a separate package whose
// files share the directory's FQN prefix: its calls into the production
// package and its own helper methods must resolve exactly too.
func TestLoadExactTypeChecksExternalTestPackage(t *testing.T) {
	r, root := loadExactTestFixture(t)
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "ext_test.go"), exactTestFixture["ext_test.go"], r)

	caller := exactModule + ".TestExternalCalls"
	for _, want := range []string{
		exactModule + ".Service.Run",     // receiver variable: svc.Run()
		exactModule + ".Runner.Run",      // interface variable: r.Run()
		exactModule + ".testRunner.call", // method value/call on a test-local type
		exactModule + ".Use",             // package-qualified call: exact too
	} {
		if !hasExactEdge(fg, caller, want) {
			t.Errorf("external test: no exact CALLS %s -> %s; calls: %v", caller, want, callsOf(fg, caller))
		}
	}
	// The external test's helper method calls its own receiver's method.
	if !hasExactEdge(fg, exactModule+".testRunner.call", exactModule+".testRunner.Run") {
		t.Errorf("external test helper: no exact CALLS testRunner.call -> testRunner.Run; calls: %v",
			callsOf(fg, exactModule+".testRunner.call"))
	}
	// The helper is declared by the external test file with the directory
	// prefix, the same one its calls are charged to.
	var helper *gtypes.SymbolNode
	for i := range fg.Symbols {
		if fg.Symbols[i].FQN == exactModule+".testRunner.call" {
			helper = &fg.Symbols[i]
		}
	}
	if helper == nil || helper.File != "ext_test.go" || !helper.IsTest {
		t.Errorf("external test helper symbol missing or misplaced: %+v", helper)
	}
}

// Type-checking the test variants must not duplicate the production IMPLEMENTS
// edges nor invent cross-variant ones: production types are judged once (from
// the plain package), test-only types once (from their test package).
func TestLoadExactTestVariantsKeepImplementsClean(t *testing.T) {
	r, _ := loadExactTestFixture(t)
	for rel, edges := range r.implements {
		seen := make(map[string]bool, len(edges))
		for _, e := range edges {
			key := e.ImplFQN + "→" + e.IfaceFQN
			if seen[key] {
				t.Errorf("%s: duplicate IMPLEMENTS edge %s", rel, key)
			}
			seen[key] = true
		}
	}
	var prod []string
	for _, e := range r.ImplementsFor("app.go") {
		prod = append(prod, e.ImplFQN+"→"+e.IfaceFQN+" "+e.Confidence)
	}
	sort.Strings(prod)
	want := []string{exactModule + ".Service→" + exactModule + ".Runner exact"}
	if strings.Join(prod, "\n") != strings.Join(want, "\n") {
		t.Errorf("production IMPLEMENTS of app.go = %q, want %q", prod, want)
	}
	// The external test-only type implements the production interface, and the
	// edge is bucketed under the test file that declares it.
	var ext []string
	for _, e := range r.ImplementsFor("ext_test.go") {
		ext = append(ext, e.ImplFQN+"→"+e.IfaceFQN+" "+e.Confidence)
	}
	if want := exactModule + ".testRunner→" + exactModule + ".Runner exact"; !strings.Contains(strings.Join(ext, "\n"), want) {
		t.Errorf("external test IMPLEMENTS of ext_test.go = %q, want %q", ext, want)
	}
}

// The synthetic test main binary go/packages generates (a package named main in
// a GOCACHE file) must never enter the resolver: its generated _testmain.go is
// not a source file of the tree, and counting it would corrupt FailedFiles and
// the graph.
func TestLoadExactTestMainNotResolved(t *testing.T) {
	r, _ := loadExactTestFixture(t)
	for rel := range r.files {
		if strings.HasPrefix(rel, "..") || strings.Contains(rel, "go-build") || !strings.HasSuffix(rel, ".go") {
			t.Errorf("resolver holds a generated file: %q", rel)
		}
	}
	if n, first := r.Skipped(); n != 0 || first != "" {
		t.Errorf("clean test variants counted as skipped: %d, %q", n, first)
	}
}

// A broken _test.go makes the test variant of its package fail to type-check;
// the production file of that package must stay exact (the plain package
// compiles), and only the broken test file is reported for retry.
func TestLoadExactBrokenTestFileKeepsProductionExact(t *testing.T) {
	needGo(t)
	root := writeTree(t, exactBrokenTestFixture)
	r, err := LoadExact(exactModule, root)
	if err != nil {
		t.Fatalf("LoadExact: %v", err)
	}
	if r == nil {
		t.Skip("go/packages could not type-check the fixture in this environment")
	}
	if !r.HasFile("app.go") || r.HasFile("bad_test.go") {
		t.Fatalf("HasFile(app.go)=%v HasFile(bad_test.go)=%v; want true, false (files: %v)",
			r.HasFile("app.go"), r.HasFile("bad_test.go"), sortedFileKeys(r))
	}
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "app.go"), exactBrokenTestFixture["app.go"], r)
	if !hasExactEdge(fg, exactModule+".Drive", exactModule+".Service.Run") {
		t.Errorf("production file lost exact resolution; calls: %v", callsOf(fg, exactModule+".Drive"))
	}
	for rel := range r.files {
		if strings.Contains(rel, "go-build") {
			t.Errorf("resolver holds a generated file: %q", rel)
		}
	}
}

// LoadExactDirs must cover the same test files as the full load, with the same
// CALLS edges, and report a broken _test.go for retry instead of dropping the
// exact resolution of the whole package.
func TestLoadExactDirsIncludesTestFiles(t *testing.T) {
	full, root := loadExactTestFixture(t)
	part, err := LoadExactDirs(exactModule, root, []string{"."})
	if err != nil || part == nil {
		t.Fatalf("LoadExactDirs(.) = %v, %v", part, err)
	}
	for _, rel := range []string{"app.go", "app_test.go", "ext_test.go"} {
		if !part.HasFile(rel) {
			t.Fatalf("partial load does not cover %q", rel)
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		want := sortedCalls(ExtractFileExact(exactModule, root, abs, exactTestFixture[rel], full))
		got := sortedCalls(ExtractFileExact(exactModule, root, abs, exactTestFixture[rel], part))
		if len(want) == 0 {
			t.Fatalf("%s: no calls in the full load", rel)
		}
		if len(got) != len(want) {
			t.Errorf("%s: partial load has %d calls, full load %d", rel, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: call %d differs: partial %+v, full %+v", rel, i, got[i], want[i])
			}
		}
	}
	// No generated test main, and nothing skipped: every package type-checked.
	for rel := range part.files {
		if strings.HasPrefix(rel, "..") || strings.Contains(rel, "go-build") || !strings.HasSuffix(rel, ".go") {
			t.Errorf("partial resolver holds a generated file: %q", rel)
		}
	}
	if n, first := part.Skipped(); n != 0 || first != "" {
		t.Errorf("Skipped() = %d, %q; want 0", n, first)
	}
	if failed := part.FailedFiles(); len(failed) != 0 {
		t.Errorf("FailedFiles() = %v; want none", failed)
	}
}

// The partial load of a package with a broken _test.go keeps the production
// file exact and reports only the test file: the retry set is not the whole
// package, and the production edges do not regress to "extracted".
func TestLoadExactDirsBrokenTestFileReportsOnlyTheTest(t *testing.T) {
	needGo(t)
	root := writeTree(t, exactBrokenTestFixture)
	r, err := LoadExactDirs(exactModule, root, []string{"."})
	if err != nil {
		t.Fatalf("LoadExactDirs: %v", err)
	}
	if r == nil || !r.HasFile("app.go") || r.HasFile("bad_test.go") {
		t.Fatalf("partial load = %v; want app.go exact and bad_test.go absent", r)
	}
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "app.go"), exactBrokenTestFixture["app.go"], r)
	if !hasExactEdge(fg, exactModule+".Drive", exactModule+".Service.Run") {
		t.Errorf("production file lost exact resolution; calls: %v", callsOf(fg, exactModule+".Drive"))
	}
	if failed := r.FailedFiles(); len(failed) != 1 || failed[0] != "bad_test.go" {
		t.Errorf("FailedFiles() = %v, want [bad_test.go]", failed)
	}
	if n, first := r.Skipped(); n != 1 || !strings.Contains(first, "undefinedName") {
		t.Errorf("Skipped() = %d, %q; want 1 and the type error", n, first)
	}
}

// A package with only _test.go files has no production view: ./... reports it
// with no Go files, and a directory load of it yields no resolver (a sync that
// touches one of its files must stay quiet and syntactic), so neither load
// resolves it exactly.
func TestLoadExactTestOnlyPackageStaysSyntactic(t *testing.T) {
	needGo(t)
	files := map[string]string{}
	for rel, content := range exactTestFixture {
		files[rel] = content
	}
	files["onlytest/x_test.go"] = "package onlytest\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
	root := writeTree(t, files)
	full, err := LoadExact(exactModule, root)
	if err != nil || full == nil {
		t.Fatalf("LoadExact = %v, %v", full, err)
	}
	if full.HasFile("onlytest/x_test.go") {
		t.Error("full load resolved a test-only package")
	}
	part, err := LoadExactDirs(exactModule, root, []string{"onlytest"})
	if part != nil || err != nil {
		t.Errorf("LoadExactDirs(onlytest) = %v, %v; want nil, nil", part, err)
	}
}

// Finding 2: a real main package in a directory whose name ends ".test" is a
// user package, not go's generated test runner. It must be resolved exactly by
// both loaders while the generated "<lib>.test" runner of a package with tests
// stays excluded. Before the fix exactKindOf keyed only on Name == "main" and
// the ".test" PkgPath suffix, so this package was silently dropped.
func TestLoadExactRealMainInDotTestDirectory(t *testing.T) {
	needGo(t)
	files := map[string]string{
		"go.mod": "module " + exactModule + "\n\ngo 1.21\n",
		"lib/lib.go": `package lib

// S is the type the real main package calls.
type S struct{}

// Run is called through a receiver variable.
func (s *S) Run() int { return 1 }

// New returns an S.
func New() *S { return &S{} }
`,
		// A test file in lib makes go/packages synthesize the lib.test runner,
		// so the fixture also proves the real runner detection still works.
		"lib/lib_test.go": `package lib

import "testing"

func TestRun(t *testing.T) { _ = New().Run() }
`,
		"runner.test/main.go": `package main

import "example.com/app/lib"

func main() { _ = lib.New().Run() }
`,
	}
	root := writeTree(t, files)
	const rel = "runner.test/main.go"
	caller := exactModule + "/runner.test.main"
	callee := exactModule + "/lib.S.Run"

	full, err := LoadExact(exactModule, root)
	if err != nil || full == nil {
		t.Fatalf("LoadExact = %v, %v", full, err)
	}
	if !full.HasFile(rel) {
		t.Fatalf("real main package in a *.test directory is dropped (files: %v)", sortedFileKeys(full))
	}
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "runner.test", "main.go"), files[rel], full)
	if !hasExactEdge(fg, caller, callee) {
		t.Errorf("full load: no exact CALLS %s -> %s; calls: %v", caller, callee, callsOf(fg, caller))
	}
	assertNoGeneratedFiles(t, full)

	part, err := LoadExactDirs(exactModule, root, []string{"runner.test"})
	if err != nil || part == nil {
		t.Fatalf("LoadExactDirs(runner.test) = %v, %v; want the real main package", part, err)
	}
	if !part.HasFile(rel) {
		t.Fatalf("partial load drops the real main package (files: %v)", sortedFileKeys(part))
	}
	fg = ExtractFileExact(exactModule, root, filepath.Join(root, "runner.test", "main.go"), files[rel], part)
	if !hasExactEdge(fg, caller, callee) {
		t.Errorf("partial load: no exact CALLS %s -> %s; calls: %v", caller, callee, callsOf(fg, caller))
	}
	if n, first := part.Skipped(); n != 0 || first != "" {
		t.Errorf("Skipped() = %d, %q; want 0", n, first)
	}
	assertNoGeneratedFiles(t, part)
}

// Finding 2: a root main package of a module whose path itself ends ".test"
// (example.com/zz.test) is a real package. Only the generated
// "<package>.test" runner of another package of the load may be dropped.
func TestLoadExactMainPackageOfModuleEndingInDotTest(t *testing.T) {
	needGo(t)
	const mod = "example.com/zz.test"
	files := map[string]string{
		"go.mod": "module " + mod + "\n\ngo 1.21\n",
		"main.go": `package main

import "example.com/zz.test/lib"

func main() { _ = lib.New().Run() }
`,
		"lib/lib.go": `package lib

// S is the type the main package calls.
type S struct{}

// Run is called through a receiver variable.
func (s *S) Run() int { return 1 }

// New returns an S.
func New() *S { return &S{} }
`,
		// The test of a package whose path ends ".test" produces the synthetic
		// "<path>.test" runner; it must still be excluded.
		"lib/lib_test.go": `package lib

import "testing"

func TestRun(t *testing.T) { _ = New().Run() }
`,
	}
	root := writeTree(t, files)
	const rel = "main.go"
	caller := mod + ".main"
	callee := mod + "/lib.S.Run"

	full, err := LoadExact(mod, root)
	if err != nil || full == nil {
		t.Fatalf("LoadExact = %v, %v", full, err)
	}
	if !full.HasFile(rel) {
		t.Fatalf("main package of module %s is dropped (files: %v)", mod, sortedFileKeys(full))
	}
	fg := ExtractFileExact(mod, root, filepath.Join(root, "main.go"), files[rel], full)
	if !hasExactEdge(fg, caller, callee) {
		t.Errorf("full load: no exact CALLS %s -> %s; calls: %v", caller, callee, callsOf(fg, caller))
	}
	assertNoGeneratedFiles(t, full)

	part, err := LoadExactDirs(mod, root, []string{"."})
	if err != nil || part == nil {
		t.Fatalf("LoadExactDirs(.) = %v, %v; want the real main package", part, err)
	}
	if !part.HasFile(rel) {
		t.Fatalf("partial load drops the main package of module %s (files: %v)", mod, sortedFileKeys(part))
	}
	fg = ExtractFileExact(mod, root, filepath.Join(root, "main.go"), files[rel], part)
	if !hasExactEdge(fg, caller, callee) {
		t.Errorf("partial load: no exact CALLS %s -> %s; calls: %v", caller, callee, callsOf(fg, caller))
	}
	assertNoGeneratedFiles(t, part)
}

// assertNoGeneratedFiles fails when a resolver holds go's generated test
// runner (its _testmain.go lives in the build cache, outside the index root).
func assertNoGeneratedFiles(t *testing.T, r *ExactResolver) {
	t.Helper()
	for rel := range r.files {
		if strings.HasPrefix(rel, "..") || strings.Contains(rel, "go-build") {
			t.Errorf("resolver holds a generated file: %q", rel)
		}
	}
}

func sortedFileKeys(r *ExactResolver) []string {
	out := make([]string, 0, len(r.files))
	for rel := range r.files {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// svcTestSrc is the external test package of the mixed-module fixture below.
const svcTestSrc = `package svc_test

import (
	"testing"

	"example.com/app/svc"
)

func TestClient(t *testing.T) {
	c := &svc.Client{}
	if got := c.Do(); got != 1 {
		t.Fatalf("Do = %d", got)
	}
	var d svc.Doer = c
	if got := d.Do(); got != 1 {
		t.Fatalf("Do via interface = %d", got)
	}
}
`

// A broken _test.go in one package of a mixed module must not disturb the
// others, and an external test package in a subdirectory must resolve calls
// under its directory prefix (not its "..._test" package path). Both loaders
// are checked: a full build and a sync of one directory.
func TestLoadExactMixedModuleWithBrokenTestAndNestedExternalTest(t *testing.T) {
	needGo(t)
	const mod = exactModule
	root := writeTree(t, map[string]string{
		"go.mod": "module " + mod + "\n\ngo 1.21\n",
		"svc/svc.go": `package svc

type Client struct{}

func (c *Client) Do() int { return 1 }

type Doer interface{ Do() int }
`,
		"svc/client_test.go":    svcTestSrc,
		"broken/broken.go":      "package broken\n\nfunc A() {}\n",
		"broken/broken_test.go": "package broken\n\nimport \"testing\"\n\nfunc TestBad(t *testing.T) { undefinedName() }\n",
	})

	checkSvc := func(t *testing.T, r *ExactResolver) {
		t.Helper()
		if !r.HasFile("svc/svc.go") || !r.HasFile("svc/client_test.go") {
			t.Fatalf("svc not covered: %v", sortedFileKeys(r))
		}
		fg := ExtractFileExact(mod, root, filepath.Join(root, "svc", "client_test.go"), svcTestSrc, r)
		caller := mod + "/svc.TestClient"
		for _, want := range []string{mod + "/svc.Client.Do", mod + "/svc.Doer.Do"} {
			if !hasExactEdge(fg, caller, want) {
				t.Errorf("nested external test: no exact CALLS %s -> %s; calls: %v", caller, want, callsOf(fg, caller))
			}
		}
	}

	full, err := LoadExact(mod, root)
	if err != nil || full == nil {
		t.Fatalf("LoadExact = %v, %v", full, err)
	}
	checkSvc(t, full)
	if full.HasFile("broken/broken_test.go") || !full.HasFile("broken/broken.go") {
		t.Errorf("broken package: broken.go=%v broken_test.go=%v; want true, false",
			full.HasFile("broken/broken.go"), full.HasFile("broken/broken_test.go"))
	}

	part, err := LoadExactDirs(mod, root, []string{"svc"})
	if err != nil || part == nil {
		t.Fatalf("LoadExactDirs(svc) = %v, %v", part, err)
	}
	checkSvc(t, part)
	if n, first := part.Skipped(); n != 0 || first != "" {
		t.Errorf("sync of svc counted the broken package: %d, %q", n, first)
	}

	broken, err := LoadExactDirs(mod, root, []string{"broken"})
	if err != nil || broken == nil {
		t.Fatalf("LoadExactDirs(broken) = %v, %v", broken, err)
	}
	if !broken.HasFile("broken/broken.go") {
		t.Error("production file of the broken package lost exact resolution")
	}
	if failed := broken.FailedFiles(); len(failed) != 1 || failed[0] != "broken/broken_test.go" {
		t.Errorf("FailedFiles() = %v, want [broken/broken_test.go]", failed)
	}
}
