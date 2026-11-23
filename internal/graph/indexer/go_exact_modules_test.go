package indexer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// nestedModulesFixture is a root module that imports a nested module under
// sub/. Go's "./..." deliberately excludes the nested module from the root
// module's package list, so only a module-aware load can type-check its files.
var nestedModulesFixture = map[string]string{
	"go.mod": "module example.com/outer\n\ngo 1.21\n\nrequire example.com/inner v0.0.0\n\nreplace example.com/inner => ./sub\n",
	"outer.go": `package outer

import "example.com/inner"

// UseInner calls a function of the nested module.
func UseInner() int { return inner.Helper() }

// UseConcrete calls a method of a type of the nested module.
func UseConcrete(w inner.Worker) int { return w.Do() }
`,
	"sub/go.mod": "module example.com/inner\n\ngo 1.21\n",
	"sub/inner.go": `package inner

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function through no receiver of its own.
func (Worker) Do() int { return helper() }

// Helper is exported and imported by the root module.
func Helper() int { return 1 }

func helper() int { return 2 }

// Run dispatches through the interface.
func Run(d Doer) int { return d.Do() }
`,
}

// rootlessModulesFixture has no go.mod at its root: the only modules are
// services/a and services/b, which a root-only load never sees.
var rootlessModulesFixture = map[string]string{
	"services/a/go.mod": "module example.com/a\n\ngo 1.21\n",
	"services/a/a.go": `package a

type Doer interface{ Do() int }

type Worker struct{}

func (Worker) Do() int { return 1 }

func Run(d Doer) int { return d.Do() }
`,
	"services/b/go.mod": "module example.com/b\n\ngo 1.21\n",
	"services/b/b.go": `package b

type Runner interface{ Run() int }

type Job struct{}

func (Job) Run() int { return 2 }

func Execute(r Runner) int { return r.Run() }
`,
}

// workspaceModulesFixture links example.com/wsa and example.com/wsb through a
// go.work file; only workspace mode lets the root module import the other one
// without a require/replace line.
var workspaceModulesFixture = map[string]string{
	"go.work": "go 1.21\n\nuse (\n\t.\n\t./wb\n)\n",
	"go.mod":  "module example.com/wsa\n\ngo 1.21\n",
	"app.go": `package wsa

import "example.com/wsb"

// Use calls a method of another workspace module's type.
func Use(b wsb.Buf) int { return b.Read() }

// Via dispatches through an interface of another workspace module.
func Via(r wsb.Reader) int { return r.Read() }
`,
	"wb/go.mod": "module example.com/wsb\n\ngo 1.21\n",
	"wb/wb.go": `package wsb

type Reader interface{ Read() int }

type Buf struct{}

func (Buf) Read() int { return 0 }
`,
}

// isolateGoHome points the Go tool's user directories at the test's temp dir,
// so a load never reads or writes the developer's HOME, module cache or
// gleann configuration.
func isolateGoHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(home, ".gleann", "memory"))
}

// declaredFQNs lists the FQNs of the symbols a FileGraph declares (callee
// stubs have File == "").
func declaredFQNs(fg FileGraph) []string {
	var out []string
	for _, s := range fg.Symbols {
		if s.File != "" {
			out = append(out, s.FQN)
		}
	}
	return out
}

// A root module plus a nested submodule must both be loaded: every file is
// resolved with go/types and prefixed with its own module path, never with the
// root module path followed by the nested directory.
func TestLoadExactNestedSubmodule(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, nestedModulesFixture)

	r, err := LoadExact("example.com/outer", root)
	if err != nil {
		t.Fatalf("LoadExact: %v", err)
	}
	if r == nil {
		t.Fatal("LoadExact did not load the root module and the nested submodule")
	}
	for _, rel := range []string{"outer.go", "sub/inner.go"} {
		if !r.HasFile(rel) {
			t.Errorf("HasFile(%q) = false; the nested module was not type-checked", rel)
		}
	}

	subSrc := nestedModulesFixture["sub/inner.go"]
	fg := ExtractFileExact("example.com/outer", root, filepath.Join(root, "sub", "inner.go"), subSrc, r)
	calls := callsOf(fg, "example.com/inner.Run")
	if calls["example.com/inner.Doer.Do"] != "exact" {
		t.Errorf("interface dispatch Run -> inner.Doer = %v, want exact", calls)
	}
	if calls := callsOf(fg, "example.com/inner.Worker.Do"); calls["example.com/inner.helper"] != "exact" {
		t.Errorf("receiver call Worker.Do -> helper missing exact edge: %v", calls)
	}
	for _, fqn := range declaredFQNs(fg) {
		if strings.HasPrefix(fqn, "example.com/outer/sub.") {
			t.Errorf("nested symbol got the root module prefix: %s", fqn)
		}
		if !strings.HasPrefix(fqn, "example.com/inner.") {
			t.Errorf("nested symbol not prefixed with its real module path: %s", fqn)
		}
	}
	wantImpl := "example.com/inner.Worker -> example.com/inner.Doer exact"
	found := false
	for _, k := range implKeys(fg.Implements) {
		if k == wantImpl {
			found = true
		}
	}
	if !found {
		t.Errorf("missing method-set IMPLEMENTS %q in %v", wantImpl, implKeys(fg.Implements))
	}

	rootSrc := nestedModulesFixture["outer.go"]
	rfg := ExtractFileExact("example.com/outer", root, filepath.Join(root, "outer.go"), rootSrc, r)
	rootCalls := callsOf(rfg, "example.com/outer.UseInner")
	if rootCalls["example.com/inner.Helper"] != "exact" {
		t.Errorf("cross-module call UseInner -> inner.Helper = %v, want exact", rootCalls)
	}
	concrete := callsOf(rfg, "example.com/outer.UseConcrete")
	if concrete["example.com/inner.Worker.Do"] != "exact" {
		t.Errorf("cross-module receiver call UseConcrete -> inner.Worker.Do = %v, want exact", concrete)
	}
}

// A repository with no root go.mod but several module directories must load
// each of those modules, with its own module path as the FQN prefix.
func TestLoadExactRootlessMultiModule(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, rootlessModulesFixture)

	r, err := LoadExact("integrationroot", root)
	if err != nil {
		t.Fatalf("LoadExact: %v", err)
	}
	if r == nil {
		t.Fatal("LoadExact did not load the nested module directories")
	}
	for _, rel := range []string{"services/a/a.go", "services/b/b.go"} {
		if !r.HasFile(rel) {
			t.Errorf("HasFile(%q) = false; the nested module was not type-checked", rel)
		}
	}

	a := ExtractFileExact("integrationroot", root, filepath.Join(root, "services", "a", "a.go"), rootlessModulesFixture["services/a/a.go"], r)
	if calls := callsOf(a, "example.com/a.Run"); calls["example.com/a.Doer.Do"] != "exact" {
		t.Errorf("interface dispatch a.Run -> a.Doer = %v, want exact", calls)
	}
	b := ExtractFileExact("integrationroot", root, filepath.Join(root, "services", "b", "b.go"), rootlessModulesFixture["services/b/b.go"], r)
	if calls := callsOf(b, "example.com/b.Execute"); calls["example.com/b.Runner.Run"] != "exact" {
		t.Errorf("interface dispatch b.Execute -> b.Runner = %v, want exact", calls)
	}
	for _, fqn := range declaredFQNs(a) {
		if !strings.HasPrefix(fqn, "example.com/a.") {
			t.Errorf("symbol of services/a not prefixed with its module path: %s", fqn)
		}
	}
	for _, fqn := range declaredFQNs(b) {
		if !strings.HasPrefix(fqn, "example.com/b.") {
			t.Errorf("symbol of services/b not prefixed with its module path: %s", fqn)
		}
	}
}

// A go.work workspace must load every module it links, including imports from
// one workspace module to another.
func TestLoadExactGoWorkWorkspace(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, workspaceModulesFixture)

	r, err := LoadExact("example.com/wsa", root)
	if err != nil {
		t.Fatalf("LoadExact: %v", err)
	}
	if r == nil {
		t.Fatal("LoadExact did not load the go.work workspace modules")
	}
	for _, rel := range []string{"app.go", "wb/wb.go"} {
		if !r.HasFile(rel) {
			t.Errorf("HasFile(%q) = false; the workspace module was not type-checked", rel)
		}
	}
	fg := ExtractFileExact("example.com/wsa", root, filepath.Join(root, "app.go"), workspaceModulesFixture["app.go"], r)
	if calls := callsOf(fg, "example.com/wsa.Use"); calls["example.com/wsb.Buf.Read"] != "exact" {
		t.Errorf("cross-module method call = %v, want example.com/wsb.Buf.Read exact", calls)
	}
	if calls := callsOf(fg, "example.com/wsa.Via"); calls["example.com/wsb.Reader.Read"] != "exact" {
		t.Errorf("workspace interface call = %v, want example.com/wsb.Reader.Read exact", calls)
	}
}

// A partial (incremental) load of a nested module's directory must type-check
// that module and prefix its files with its own module path.
func TestLoadExactDirsNestedSubmodule(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, nestedModulesFixture)

	r, err := LoadExactDirs("example.com/outer", root, []string{"sub"})
	if err != nil {
		t.Fatalf("LoadExactDirs: %v", err)
	}
	if r == nil {
		t.Fatal("LoadExactDirs did not load the nested module's directory")
	}
	if !r.HasFile("sub/inner.go") {
		t.Fatal("sub/inner.go was not type-checked")
	}
	fg := ExtractFileExact("example.com/outer", root, filepath.Join(root, "sub", "inner.go"), nestedModulesFixture["sub/inner.go"], r)
	if calls := callsOf(fg, "example.com/inner.Run"); calls["example.com/inner.Doer.Do"] != "exact" {
		t.Errorf("partial load of a nested module: Run -> Doer = %v, want exact", calls)
	}
	for _, fqn := range declaredFQNs(fg) {
		if strings.HasPrefix(fqn, "example.com/outer/sub.") {
			t.Errorf("partial load used the root module prefix: %s", fqn)
		}
	}
}

// A nested module that vendors its dependencies must load from vendor/ (with
// -mod=vendor rooted at that module), because its dependency is absent from
// the module cache.
func TestLoadExactNestedVendoredModule(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, map[string]string{
		"sub/go.mod":                        "module example.com/vend\n\ngo 1.21\n\nrequire example.com/dep v1.0.0\n",
		"sub/app.go":                        "package vend\n\nimport \"example.com/dep\"\n\n// Use reads the vendored constant.\nfunc Use() int { return dep.Value }\n",
		"sub/vendor/example.com/dep/dep.go": "package dep\n\n// Value is vendored.\nconst Value = 7\n",
		"sub/vendor/modules.txt":            "# example.com/dep v1.0.0\n## explicit; go 1.21\nexample.com/dep\n",
	})
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "mod"))
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))

	r, err := LoadExact("fallback", root)
	if err != nil {
		t.Fatalf("LoadExact of a nested vendored module: %v", err)
	}
	if r == nil || !r.HasFile("sub/app.go") {
		t.Fatalf("nested vendored module not type-checked (resolver %v): the per-module -mod=vendor choice was not applied", r)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "go.mod")); err != nil {
		t.Fatalf("go.mod was moved: %v", err)
	}
}

// workspaceWithStandaloneFixture is a go.work that links ./a and ./b, plus a
// standalone module ./tools/c that the workspace does NOT list. A go command
// run inside ./tools/c with GOWORK pointing at the root workspace fails with
// "directory prefix . does not contain modules listed in go.work", so the
// workspace must not be pinned to a module its use list does not name.
var workspaceWithStandaloneFixture = map[string]string{
	"go.work":        "go 1.21\n\nuse (\n\t./a\n\t./b\n)\n",
	"a/go.mod":       "module example.com/a\n\ngo 1.21\n",
	"b/go.mod":       "module example.com/b\n\ngo 1.21\n",
	"tools/c/go.mod": "module example.com/c\n\ngo 1.21\n",
	"a/a.go": `package a

import "example.com/b"

// Use calls a function of the other workspace module.
func Use() int { return b.Value() }

// Via dispatches through an interface of the other workspace module.
func Via(r b.Doer) int { return r.Do() }
`,
	"b/b.go": `package b

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function.
func (Worker) Do() int { return helper() }

func helper() int { return 2 }

// Value is imported by the other workspace module.
func Value() int { return 1 }
`,
	"tools/c/c.go": `package c

// Runner is implemented by Job by method set.
type Runner interface{ Run() int }

// Job implements Runner.
type Job struct{}

// Run calls a package-level function.
func (Job) Run() int { return step() }

func step() int { return 3 }

// Execute dispatches through the interface.
func Execute(r Runner) int { return r.Run() }
`,
}

// A standalone submodule that its enclosing go.work does not list must load in
// module mode (GOWORK=off), while the listed workspace modules keep loading in
// workspace mode: exact CALLS/IMPLEMENTS for all of them, with their own module
// path as the FQN prefix.
func TestLoadExactGoWorkExcludesUnlistedSubmodule(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, workspaceWithStandaloneFixture)

	mods, err := DiscoverModules(root)
	if err != nil {
		t.Fatalf("DiscoverModules: %v", err)
	}
	cMod, ok := mods.ModuleFor("tools/c/c.go")
	if !ok || cMod.Path != "example.com/c" {
		t.Fatalf("module of tools/c/c.go = %+v, %v; want module example.com/c", cMod, ok)
	}
	if cMod.Work != "" {
		t.Errorf("standalone module tools/c was pinned to the workspace %s it is not listed in", cMod.Work)
	}
	for _, rel := range []string{"a/a.go", "b/b.go"} {
		m, ok := mods.ModuleFor(rel)
		if !ok || m.Work == "" {
			t.Errorf("workspace module %s has no go.work (%+v, %v)", rel, m, ok)
		}
	}

	r, err := LoadExactModules(mods, "fallback")
	if err != nil {
		t.Fatalf("LoadExactModules: %v", err)
	}
	if r == nil {
		t.Fatal("LoadExactModules loaded no module")
	}
	for _, rel := range []string{"a/a.go", "b/b.go", "tools/c/c.go"} {
		if !r.HasFile(rel) {
			t.Errorf("HasFile(%q) = false; that module was not type-checked", rel)
		}
	}

	a := ExtractFileExactInModules(mods, "fallback", root, filepath.Join(root, "a", "a.go"), workspaceWithStandaloneFixture["a/a.go"], r)
	if calls := callsOf(a, "example.com/a.Use"); calls["example.com/b.Value"] != "exact" {
		t.Errorf("workspace cross-module call a.Use -> b.Value = %v, want exact", calls)
	}
	if calls := callsOf(a, "example.com/a.Via"); calls["example.com/b.Doer.Do"] != "exact" {
		t.Errorf("workspace interface call a.Via -> b.Doer.Do = %v, want exact", calls)
	}

	b := ExtractFileExactInModules(mods, "fallback", root, filepath.Join(root, "b", "b.go"), workspaceWithStandaloneFixture["b/b.go"], r)
	if calls := callsOf(b, "example.com/b.Worker.Do"); calls["example.com/b.helper"] != "exact" {
		t.Errorf("workspace receiver call b.Worker.Do -> helper = %v, want exact", calls)
	}
	wantImpl := "example.com/b.Worker -> example.com/b.Doer exact"
	if got := implKeys(b.Implements); !containsString(got, wantImpl) {
		t.Errorf("missing method-set IMPLEMENTS %q in %v", wantImpl, got)
	}

	c := ExtractFileExactInModules(mods, "fallback", root, filepath.Join(root, "tools", "c", "c.go"), workspaceWithStandaloneFixture["tools/c/c.go"], r)
	if calls := callsOf(c, "example.com/c.Execute"); calls["example.com/c.Runner.Run"] != "exact" {
		t.Errorf("standalone module interface call c.Execute -> c.Runner.Run = %v, want exact", calls)
	}
	if calls := callsOf(c, "example.com/c.Job.Run"); calls["example.com/c.step"] != "exact" {
		t.Errorf("standalone module receiver call c.Job.Run -> c.step = %v, want exact", calls)
	}
	wantCImpl := "example.com/c.Job -> example.com/c.Runner exact"
	if got := implKeys(c.Implements); !containsString(got, wantCImpl) {
		t.Errorf("missing method-set IMPLEMENTS %q in %v", wantCImpl, got)
	}
	for _, fqn := range declaredFQNs(c) {
		if !strings.HasPrefix(fqn, "example.com/c.") {
			t.Errorf("symbol of the standalone module not prefixed with its module path: %s", fqn)
		}
	}
}

// An empty, comment-only or malformed go.work lists no module: it must not be
// pinned to any module, and every module must still load in module mode.
func TestLoadExactIgnoredEmptyGoWork(t *testing.T) {
	goFiles := map[string]string{
		"a/go.mod": "module example.com/solo\n\ngo 1.21\n",
		"a/solo.go": `package solo

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function.
func (Worker) Do() int { return helper() }

func helper() int { return 1 }

// Run dispatches through the interface.
func Run(d Doer) int { return d.Do() }
`,
	}
	cases := map[string]string{
		"empty":          "",
		"comment-only":   "// a workspace that links nothing yet\n",
		"no use":         "go 1.21\n",
		"unclosed block": "go 1.21\n\nuse (\n\t./a\n",
		"malformed path": "go 1.21\n\nuse \"/unterminated\n",
	}
	for name, work := range cases {
		t.Run(name, func(t *testing.T) {
			needGo(t)
			isolateGoHome(t)
			files := make(map[string]string, len(goFiles)+1)
			for k, v := range goFiles {
				files[k] = v
			}
			files["go.work"] = work
			root := writeTree(t, files)

			mods, err := DiscoverModules(root)
			if err != nil {
				t.Fatalf("DiscoverModules: %v", err)
			}
			m, ok := mods.ModuleFor("a/solo.go")
			if !ok || m.Path != "example.com/solo" {
				t.Fatalf("module of a/solo.go = %+v, %v; want example.com/solo", m, ok)
			}
			if m.Work != "" {
				t.Fatalf("%s go.work was pinned to module a; it lists no use directive", name)
			}
			r, err := LoadExactModules(mods, "fallback")
			if err != nil {
				t.Fatalf("LoadExactModules with a %s go.work: %v", name, err)
			}
			if r == nil || !r.HasFile("a/solo.go") {
				t.Fatalf("module a was not type-checked with a %s go.work (resolver %v)", name, r)
			}
			fg := ExtractFileExactInModules(mods, "fallback", root, filepath.Join(root, "a", "solo.go"), goFiles["a/solo.go"], r)
			if calls := callsOf(fg, "example.com/solo.Run"); calls["example.com/solo.Doer.Do"] != "exact" {
				t.Errorf("interface dispatch solo.Run -> solo.Doer.Do = %v, want exact", calls)
			}
			if calls := callsOf(fg, "example.com/solo.Worker.Do"); calls["example.com/solo.helper"] != "exact" {
				t.Errorf("receiver call solo.Worker.Do -> helper = %v, want exact", calls)
			}
			wantImpl := "example.com/solo.Worker -> example.com/solo.Doer exact"
			if got := implKeys(fg.Implements); !containsString(got, wantImpl) {
				t.Errorf("missing method-set IMPLEMENTS %q in %v", wantImpl, got)
			}
		})
	}
}

// workspaceOutsideModuleFixture keeps the go.work in a subdirectory and lists
// the sibling module a with "use ../a". Go honours a workspace for every module
// its use list names, whether or not the workspace directory is an ancestor of
// the module: GOWORK=<root>/sub/go.work resolves example.com/b from root/a.
var workspaceOutsideModuleFixture = map[string]string{
	"sub/go.work": "go 1.21\n\nuse (\n\t../a\n\t./b\n)\n",
	"a/go.mod":    "module example.com/a\n\ngo 1.21\n",
	"a/a.go": `package a

import "example.com/b"

// Use calls a function of the other workspace module.
func Use() int { return b.Value() }
`,
	"sub/b/go.mod": "module example.com/b\n\ngo 1.21\n",
	"sub/b/b.go": `package b

// Value is imported by the other workspace module.
func Value() int { return 7 }
`,
}

// A go.work that sits in a subdirectory and lists a module outside it
// ("use ../a") must govern that module: otherwise the module runs with
// GOWORK=off and its workspace-only import collapses to the syntactic resolver.
func TestLoadExactGoWorkOutsideModuleDir(t *testing.T) {
	needGo(t)
	isolateGoHome(t)
	root := writeTree(t, workspaceOutsideModuleFixture)
	wantWork := filepath.Join(root, "sub", "go.work")

	mods, err := DiscoverModules(root)
	if err != nil {
		t.Fatalf("DiscoverModules: %v", err)
	}
	for _, rel := range []string{"a/a.go", "sub/b/b.go"} {
		m, ok := mods.ModuleFor(rel)
		if !ok {
			t.Fatalf("no module for %s", rel)
		}
		if m.Work != wantWork {
			t.Errorf("%s.Work = %q, want %q (the workspace lists that module)", rel, m.Work, wantWork)
		}
	}

	r, err := LoadExactModules(mods, "fallback")
	if err != nil {
		t.Fatalf("LoadExactModules: %v", err)
	}
	if r == nil {
		t.Fatal("LoadExactModules loaded no module")
	}
	for _, rel := range []string{"a/a.go", "sub/b/b.go"} {
		if !r.HasFile(rel) {
			t.Errorf("HasFile(%q) = false; the module was not type-checked", rel)
		}
	}
	fg := ExtractFileExactInModules(mods, "fallback", root, filepath.Join(root, "a", "a.go"), workspaceOutsideModuleFixture["a/a.go"], r)
	if calls := callsOf(fg, "example.com/a.Use"); calls["example.com/b.Value"] != "exact" {
		t.Errorf("workspace call a.Use -> b.Value = %v, want exact (GOWORK=%s)", calls, wantWork)
	}
}

// nearestWorkspace ranks the workspaces that list a module: a workspace whose
// directory contains the module wins (the deepest one), and when none does, the
// workspace nearest the module in the tree wins. A workspace that does not list
// the module never governs it.
func TestNearestWorkspaceRanking(t *testing.T) {
	root := t.TempDir()
	modA := filepath.Join(root, "a")
	modB := filepath.Join(root, "sub", "b")
	work := func(rel string, uses ...string) workspace {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		u := make(map[string]bool, len(uses))
		for _, p := range uses {
			u[moduleDirKey(filepath.Join(dir, filepath.FromSlash(p)))] = true
		}
		return workspace{file: filepath.Join(dir, "go.work"), dir: dir, uses: u}
	}
	at := func(rel ...string) string {
		return filepath.Join(append([]string{root}, rel...)...)
	}
	cases := []struct {
		name       string
		modDir     string
		workspaces []workspace
		want       string
	}{
		{
			name:       "workspace outside the module directory",
			modDir:     modA,
			workspaces: []workspace{work("sub", "../a", "./b")},
			want:       at("sub", "go.work"),
		},
		{
			name:       "deepest containing workspace wins",
			modDir:     modB,
			workspaces: []workspace{work(".", "./a", "./sub/b"), work("sub", "./b", "../a")},
			want:       at("sub", "go.work"),
		},
		{
			name:       "containing workspace beats a listing outsider",
			modDir:     modA,
			workspaces: []workspace{work(".", "./a", "./sub/b"), work("sub", "./b", "../a")},
			want:       at("go.work"),
		},
		{
			name:       "nearest listing workspace wins when none contains the module",
			modDir:     at("services", "a"),
			workspaces: []workspace{work("tools/w", "../services/a"), work("services/w", "../a")},
			want:       at("services", "w", "go.work"),
		},
		{
			name:       "deeper listing workspace wins when the shared prefix ties",
			modDir:     modA,
			workspaces: []workspace{work("build", "../../a"), work("build/nested", "../../a")},
			want:       at("build", "nested", "go.work"),
		},
		{
			name:       "unlisted module has no workspace",
			modDir:     modA,
			workspaces: []workspace{work("sub", "./b")},
			want:       "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nearestWorkspace(tc.modDir, tc.workspaces); got != tc.want {
				t.Errorf("nearestWorkspace(%s) = %q, want %q", tc.modDir, got, tc.want)
			}
		})
	}
}

// A go.work whose use path ends in an escaped backslash ("use \"./a\\\\\"") is
// accepted by the Go tool, so the module must be pinned to that workspace.
func TestDiscoverModulesEscapedTrailingBackslash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash is a path separator on Windows")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "a\\")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.work", "go 1.21\n\nuse \"./a\\\\\"\n")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/esc\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mods, err := DiscoverModules(root)
	if err != nil {
		t.Fatalf("DiscoverModules: %v", err)
	}
	for _, m := range mods.Modules() {
		if m.Path != "example.com/esc" {
			continue
		}
		if want := filepath.Join(root, "go.work"); m.Work != want {
			t.Errorf("module in ./a\\ has Work = %q, want %q", m.Work, want)
		}
		return
	}
	t.Fatal("module in ./a\\ was not discovered")
}

// GoPathIgnored must exclude the files the Go tool never loads: those under
// testdata, vendor, a hidden or a "_"-prefixed directory, and those whose own
// base name starts with "_" or ".".
func TestGoPathIgnored(t *testing.T) {
	cases := map[string]bool{
		"":                    false,
		"main.go":             false,
		"pkg/foo.go":          false,
		"pkg/foo_test.go":     false,
		"./pkg/foo.go":        false,
		"a/../pkg/foo.go":     false,
		"pkg":                 false,
		"_fixture.go":         true,
		".fixture.go":         true,
		"pkg/_gen.go":         true,
		"pkg/.hidden.go":      true,
		"testdata/fixture.go": true,
		"vendor/x/y.go":       true,
		"_build/gen.go":       true,
		".cache/hidden.go":    true,
	}
	for rel, want := range cases {
		if got := GoPathIgnored(rel); got != want {
			t.Errorf("GoPathIgnored(%q) = %v, want %v", rel, got, want)
		}
	}
}

// workspaceUses parses the use directives of a go.work file: single-line,
// block and quoted forms, with // comments ignored, and yields an empty set for
// an empty, comment-only or malformed file. The grammar is the Go tool's own:
// a raw (backtick) string in use, an unterminated string and an inline block
// are rejected by "go work edit -json" too, so the whole file lists nothing.
func TestWorkspaceUses(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string // paths relative to the go.work directory
	}{
		{"single line", "go 1.21\n\nuse ./a\n", []string{"a"}},
		{"block", "go 1.21\n\nuse (\n\t.\n\t./a\n\t./b/c\n)\n", []string{".", "a", "b/c"}},
		{"quoted", "go 1.21\n\nuse (\n\t\"./a\"\n\t\"./b\"\n)\n", []string{"a", "b"}},
		{"comments", "// use ./nope\nuse ./a // trailing\n//\n", []string{"a"}},
		{"comment before block", "use ( // the modules\n\t./a\n)\n", []string{"a"}},
		{"trailing slash", "use ./a/\n", []string{"a"}},
		{"empty", "", nil},
		{"comment only", "// nothing here\n", nil},
		{"no use", "go 1.21\n\ntoolchain go1.22.0\n", nil},
		{"unclosed block", "use (\n\t./a\n", nil},
		{"unterminated quote", "use \"/unterminated\n", nil},
		{"inline block", "use ( ./a )\n", nil},
		{"two paths", "use ./a ./b\n", nil},
		// Go's go.work grammar has no raw strings: "go work edit -json"
		// fails with "unquoted string cannot contain quote", so the file
		// lists nothing.
		{"raw string rejected", "go 1.21\n\nuse `./a`\n", nil},
		{"raw string in block rejected", "go 1.21\n\nuse (\n\t`./a`\n)\n", nil},
		// A backslash is an ordinary character of an unquoted token (the
		// real tool accepts "use ./a\b"), while it escapes a quote inside an
		// interpreted string.
		{"unquoted backslash", "use ./a\\b\n", []string{"a\\b"}},
		{"escaped trailing backslash", "use \"./a\\\\\"\n", []string{"a\\"}},
		{"escaped quote", "use \"./a\\\"b\"\n", []string{"a\"b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "go.work")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			got := workspaceUses(path)
			if len(got) != len(tc.want) {
				t.Fatalf("workspaceUses(%q) = %v, want %v", tc.body, got, tc.want)
			}
			for _, rel := range tc.want {
				key := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(rel)))
				if !got[key] {
					t.Errorf("workspaceUses(%q) lacks %q: %v", tc.body, key, got)
				}
			}
		})
	}
	if got := workspaceUses(filepath.Join(t.TempDir(), "missing", "go.work")); len(got) != 0 {
		t.Errorf("workspaceUses of a missing file = %v, want empty", got)
	}
}

// containsString reports whether ss holds want.
func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestCommonDirPrefix_ElementBoundary(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"/root/app", "/root/app-backend", 5},
		{"/root/app-backend", "/root/app", 5},
		{"/root/app", "/root/app", 9},
		{"/root/app", "/root/app/sub", 9},
		{"/", "/app", 1},
		{"", "/app", 0},
	}
	for _, tc := range cases {
		if got := commonDirPrefix(tc.a, tc.b); got != tc.want {
			t.Errorf("commonDirPrefix(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
