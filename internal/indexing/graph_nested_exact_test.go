package indexing

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// isolateEnv points HOME, the XDG directories and gleann's config/memory
// locations at the test's temp dir, so a build never reads or writes the
// developer's real ~/.gleann.
func isolateEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(home, ".gleann", "memory"))
}

// nestedBuildFixture is a root module that imports a nested module under sub/.
// Go's "./..." excludes the nested module from the root module's package list.
func nestedBuildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/outer\n\ngo 1.21\n\nrequire example.com/inner v0.0.0\n\nreplace example.com/inner => ./sub\n")
	writeFile(t, filepath.Join(dir, "outer.go"), `package outer

import "example.com/inner"

// UseInner calls a function of the nested module.
func UseInner() int { return inner.Helper() }

// UseConcrete calls a method of a type of the nested module.
func UseConcrete(w inner.Worker) int { return w.Do() }
`)
	writeFile(t, filepath.Join(dir, "sub", "go.mod"), "module example.com/inner\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub", "inner.go"), `package inner

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function.
func (Worker) Do() int { return helper() }

// Helper is exported and imported by the root module.
func Helper() int { return 1 }

func helper() int { return 2 }

// Run dispatches through the interface.
func Run(d Doer) int { return d.Do() }
`)
	return dir
}

// A full build must load a nested submodule too: its calls resolve exactly and
// its symbols carry the nested module path, not the root module path plus the
// nested directory.
func TestFormat2ExactNestedSubmodule(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	docs := nestedBuildFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)

	for _, e := range [][2]string{
		{"example.com/inner.Run", "example.com/inner.Doer"},
		{"example.com/inner.Worker.Do", "example.com/inner.helper"},
		{"example.com/outer.UseInner", "example.com/inner.Helper"},
		{"example.com/outer.UseConcrete", "example.com/inner.Worker.Do"},
	} {
		if conf, ext := callEdge(t, env, e[0], e[1]); conf != "exact" || ext != 0 {
			t.Errorf("CALLS %s -> %s = (%q, external=%d), want (exact, internal)", e[0], e[1], conf, ext)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/inner.Worker' AND dst = 'example.com/inner.Doer' AND confidence = 'exact'`); n != 1 {
		t.Errorf("method-set IMPLEMENTS inner.Worker -> inner.Doer = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn LIKE 'example.com/outer/sub.%'`); n != 0 {
		t.Errorf("%d symbol(s) prefixed with the root module plus the nested directory", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE src LIKE 'example.com/outer/sub.%' OR dst LIKE 'example.com/outer/sub.%'`); n != 0 {
		t.Errorf("%d edge(s) prefixed with the root module plus the nested directory", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/inner.Worker'`); n != 1 {
		t.Errorf("nested symbol example.com/inner.Worker = %d, want 1", n)
	}
	assertNoOrphans(t, db)
}

// A repository with no root go.mod but several module directories must resolve
// each module and prefix its FQNs with that module's real path.
func TestFormat2ExactRootlessMultiModule(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "services", "a", "go.mod"), "module example.com/a\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "services", "a", "a.go"), `package a

type Doer interface{ Do() int }

type Worker struct{}

func (Worker) Do() int { return 1 }

func Run(d Doer) int { return d.Do() }
`)
	writeFile(t, filepath.Join(dir, "services", "b", "go.mod"), "module example.com/b\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "services", "b", "b.go"), `package b

type Runner interface{ Run() int }

type Job struct{}

func (Job) Run() int { return 2 }

func Execute(r Runner) int { return r.Run() }
`)
	buildFormat2(t, env, dir)
	db := openGraphDB(t, env)

	for _, e := range [][2]string{
		{"example.com/a.Run", "example.com/a.Doer"},
		{"example.com/b.Execute", "example.com/b.Runner"},
	} {
		if conf, ext := callEdge(t, env, e[0], e[1]); conf != "exact" || ext != 0 {
			t.Errorf("CALLS %s -> %s = (%q, external=%d), want (exact, internal)", e[0], e[1], conf, ext)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn LIKE ?`, filepath.Base(dir)+".%"); n != 0 {
		t.Errorf("%d symbol(s) prefixed with the index directory name instead of a module path", n)
	}
	assertNoOrphans(t, db)
}

// A go.work workspace must load the modules it links, including a call from
// one workspace module into another.
func TestFormat2ExactGoWorkWorkspace(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.work"), "go 1.21\n\nuse (\n\t.\n\t./wb\n)\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/wsa\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "app.go"), `package wsa

import "example.com/wsb"

// Use calls a method of another workspace module's type.
func Use(b wsb.Buf) int { return b.Read() }

// Via dispatches through an interface of another workspace module.
func Via(r wsb.Reader) int { return r.Read() }
`)
	writeFile(t, filepath.Join(dir, "wb", "go.mod"), "module example.com/wsb\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "wb", "wb.go"), `package wsb

type Reader interface{ Read() int }

type Buf struct{}

func (Buf) Read() int { return 0 }
`)
	buildFormat2(t, env, dir)
	db := openGraphDB(t, env)

	if conf, ext := callEdge(t, env, "example.com/wsa.Use", "example.com/wsb.Buf.Read"); conf != "exact" || ext != 0 {
		t.Errorf("workspace cross-module call = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	if conf, ext := callEdge(t, env, "example.com/wsa.Via", "example.com/wsb.Reader"); conf != "exact" || ext != 0 {
		t.Errorf("workspace interface call = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/wsb.Buf' AND dst = 'example.com/wsb.Reader' AND confidence = 'exact'`); n != 1 {
		t.Errorf("workspace method-set IMPLEMENTS wsb.Buf -> wsb.Reader = %d, want 1", n)
	}
	assertNoOrphans(t, db)
}

// An incremental sync that re-extracts a file of a nested submodule must load
// that module and resolve the file exactly, with the nested module path.
func TestSyncNestedSubmoduleExact(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	docs := nestedBuildFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if conf, ext := callEdge(t, env, "example.com/inner.Run", "example.com/inner.Doer"); conf != "exact" || ext != 0 {
		t.Fatalf("the full build is not exact: nothing to compare the sync with (%q, %d)", conf, ext)
	}

	edited := `package inner

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function.
func (Worker) Do() int { return helper() }

// Helper is exported and imported by the root module.
func Helper() int { return 1 }

func helper() int { return 2 }

// Run dispatches through the interface.
func Run(d Doer) int { return d.Do() }

// Extra is added by the edit and dispatches through a second interface.
type Extra interface{ Do() int }

// Double calls through a receiver added by the edit.
func (w Worker) Double() int { return w.Do() }

// RunExtra dispatches through Extra.
func RunExtra(e Extra) int { return e.Do() }
`
	writeFile(t, filepath.Join(docs, "sub", "inner.go"), edited)
	syncGraph(t, env)

	for _, e := range [][2]string{
		{"example.com/inner.RunExtra", "example.com/inner.Extra"},
		{"example.com/inner.Worker.Double", "example.com/inner.Worker.Do"},
	} {
		if conf, ext := callEdge(t, env, e[0], e[1]); conf != "exact" || ext != 0 {
			t.Errorf("synced CALLS %s -> %s = (%q, external=%d), want (exact, internal)", e[0], e[1], conf, ext)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/inner.Worker' AND dst = 'example.com/inner.Extra' AND confidence = 'exact'`); n != 1 {
		t.Errorf("synced IMPLEMENTS inner.Worker -> inner.Extra = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn LIKE 'example.com/outer/sub.%'`); n != 0 {
		t.Errorf("%d symbol(s) prefixed with the root module plus the nested directory after sync", n)
	}

	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "nested submodule sync", openGraphDB(t, env), openGraphDB(t, full))
	assertNoOrphans(t, openGraphDB(t, env))
}

// A tree whose Go files have no go.mod/go.work anywhere must build
// syntactically, and the build must warn on stderr (never on stdout, where
// JSON/MCP callers read).
func TestFormat2NoModuleGoFilesWarnOnStderr(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package plain\n\nfunc One() int { return 1 }\n\nfunc Two() int { return One() }\n")

	var stdout, stderr bytes.Buffer
	o := env.options(nil)
	o.Stdout, o.Stderr = &stdout, &stderr
	if _, err := Build(context.Background(), BuildOptions{Options: o, Name: "g", DocsDir: dir, Graph: true}); err != nil {
		t.Fatalf("Build: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no Go module") {
		t.Errorf("a Go tree without a module did not warn on stderr: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "no Go module") {
		t.Errorf("the warning leaked to stdout: %q", stdout.String())
	}
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = ? AND dst = ?`, filepath.Base(dir)+".Two", filepath.Base(dir)+".One"); n != 1 {
		t.Errorf("syntactic call Two -> One = %d after the warning, want 1 (the build must continue)", n)
	}
}

// A repository with no Go files at all must not print the Go-module warning.
func TestFormat2NonGoTreeNoModuleWarning(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "# Docs\n\nSome prose.\n")
	writeFile(t, filepath.Join(dir, "script.py"), "def run():\n    return 1\n")

	var stdout, stderr bytes.Buffer
	o := env.options(nil)
	o.Stdout, o.Stderr = &stdout, &stderr
	if _, err := Build(context.Background(), BuildOptions{Options: o, Name: "g", DocsDir: dir, Graph: true}); err != nil {
		t.Fatalf("Build: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "no Go module") {
		t.Errorf("a tree with no Go files warned about Go modules: %q", stderr.String())
	}
}

// The real module path of a nested module prefixes its symbols even when exact
// resolution is disabled: FQN prefixing must not depend on the go/types load.
func TestFormat2NestedSubmoduleFQNPrefixWithoutExact(t *testing.T) {
	isolateEnv(t)
	t.Setenv("GLEANN_GRAPH_EXACT", "0")
	env := newEnv(t)
	docs := nestedBuildFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)

	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/inner.Worker'`); n != 1 {
		t.Errorf("nested symbol example.com/inner.Worker = %d, want 1 (real module path, exact disabled)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn LIKE 'example.com/outer/sub.%'`); n != 0 {
		t.Errorf("%d symbol(s) prefixed with the root module plus the nested directory", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE confidence = 'exact'`); n != 0 {
		t.Errorf("%d exact edge(s) with GLEANN_GRAPH_EXACT=0", n)
	}
}

// A go.work that lists ./a and ./b, plus a standalone module ./tools/c the
// workspace does not list: both the workspace modules and the standalone
// module must resolve exactly, each with its own module path as FQN prefix.
func TestFormat2ExactGoWorkWithStandaloneSubmodule(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.work"), "go 1.21\n\nuse (\n\t./a\n\t./b\n)\n")
	writeFile(t, filepath.Join(dir, "a", "go.mod"), "module example.com/a\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a", "a.go"), `package a

import "example.com/b"

// Use calls a function of the other workspace module.
func Use() int { return b.Value() }

// Via dispatches through an interface of the other workspace module.
func Via(r b.Doer) int { return r.Do() }
`)
	writeFile(t, filepath.Join(dir, "b", "go.mod"), "module example.com/b\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "b", "b.go"), `package b

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function.
func (Worker) Do() int { return helper() }

func helper() int { return 2 }

// Value is imported by the other workspace module.
func Value() int { return 1 }
`)
	writeFile(t, filepath.Join(dir, "tools", "c", "go.mod"), "module example.com/c\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "tools", "c", "c.go"), `package c

// Runner is implemented by Job by method set.
type Runner interface{ Run() int }

// Job implements Runner.
type Job struct{}

// Run calls a package-level function.
func (Job) Run() int { return step() }

func step() int { return 3 }

// Execute dispatches through the interface.
func Execute(r Runner) int { return r.Run() }
`)
	buildFormat2(t, env, dir)
	db := openGraphDB(t, env)

	for _, e := range [][2]string{
		{"example.com/a.Use", "example.com/b.Value"},
		{"example.com/a.Via", "example.com/b.Doer"},
		{"example.com/b.Worker.Do", "example.com/b.helper"},
		{"example.com/c.Execute", "example.com/c.Runner"},
		{"example.com/c.Job.Run", "example.com/c.step"},
	} {
		if conf, ext := callEdge(t, env, e[0], e[1]); conf != "exact" || ext != 0 {
			t.Errorf("CALLS %s -> %s = (%q, external=%d), want (exact, internal)", e[0], e[1], conf, ext)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/b.Worker' AND dst = 'example.com/b.Doer' AND confidence = 'exact'`); n != 1 {
		t.Errorf("workspace method-set IMPLEMENTS b.Worker -> b.Doer = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/c.Job' AND dst = 'example.com/c.Runner' AND confidence = 'exact'`); n != 1 {
		t.Errorf("standalone module method-set IMPLEMENTS c.Job -> c.Runner = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn LIKE 'example.com/%.%tools/%' OR fqn LIKE 'example.com/%.%tools.%'`); n != 0 {
		t.Errorf("%d symbol(s) prefixed with the workspace module path plus the standalone directory", n)
	}
	assertNoOrphans(t, db)
}

// An empty or comment-only go.work lists no module: the module under the tree
// must still load in module mode and resolve exactly.
func TestFormat2EmptyGoWorkStillExact(t *testing.T) {
	for name, work := range map[string]string{
		"empty":        "",
		"comment-only": "// this workspace links nothing yet\n",
	} {
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)
			env := newEnv(t)
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.work"), work)
			writeFile(t, filepath.Join(dir, "a", "go.mod"), "module example.com/solo\n\ngo 1.21\n")
			writeFile(t, filepath.Join(dir, "a", "solo.go"), `package solo

// Doer is implemented by Worker by method set.
type Doer interface{ Do() int }

// Worker implements Doer.
type Worker struct{}

// Do calls a package-level function.
func (Worker) Do() int { return helper() }

func helper() int { return 1 }

// Run dispatches through the interface.
func Run(d Doer) int { return d.Do() }
`)
			buildFormat2(t, env, dir)
			db := openGraphDB(t, env)
			for _, e := range [][2]string{
				{"example.com/solo.Run", "example.com/solo.Doer"},
				{"example.com/solo.Worker.Do", "example.com/solo.helper"},
			} {
				if conf, ext := callEdge(t, env, e[0], e[1]); conf != "exact" || ext != 0 {
					t.Errorf("CALLS %s -> %s = (%q, external=%d), want (exact, internal)", e[0], e[1], conf, ext)
				}
			}
			if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/solo.Worker' AND dst = 'example.com/solo.Doer' AND confidence = 'exact'`); n != 1 {
				t.Errorf("method-set IMPLEMENTS solo.Worker -> solo.Doer = %d, want 1", n)
			}
			assertNoOrphans(t, db)
		})
	}
}

// A non-Go repository whose only .go files sit in directories the Go tool
// ignores (testdata, vendor, _prefixed, hidden) must build and sync without the
// moduleless-Go-tree warning: those files belong to no Go package of the tree.
func TestFormat2NonGoTreeWithIgnoredGoFixturesNoWarn(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.py"), "def run():\n    return 1\n")
	writeFile(t, filepath.Join(dir, "testdata", "fixture.go"), "package fixture\n\n// One is a fixture.\nfunc One() int { return 1 }\n")
	writeFile(t, filepath.Join(dir, "vendor", "sample.go"), "package sample\n\n// Two is vendored.\nfunc Two() int { return 2 }\n")
	writeFile(t, filepath.Join(dir, "_build", "gen.go"), "package build\n\n// Three is generated.\nfunc Three() int { return 3 }\n")
	writeFile(t, filepath.Join(dir, ".cache", "hidden.go"), "package cache\n\n// Four is hidden.\nfunc Four() int { return 4 }\n")

	var stdout, stderr bytes.Buffer
	o := env.options(nil)
	o.Stdout, o.Stderr = &stdout, &stderr
	if _, err := Build(context.Background(), BuildOptions{Options: o, Name: "g", DocsDir: dir, Graph: true}); err != nil {
		t.Fatalf("Build: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "Go module") || strings.Contains(stderr.String(), "Go file(s)") {
		t.Errorf("a non-Go tree with ignored .go fixtures warned on build: %q", stderr.String())
	}

	// The sync must keep quiet too: a changed fixture file is not a Go package.
	writeFile(t, filepath.Join(dir, "testdata", "fixture.go"), "package fixture\n\n// One is an edited fixture.\nfunc One() int { return 10 }\n")
	writeFile(t, filepath.Join(dir, "_build", "gen.go"), "package build\n\n// Three is regenerated.\nfunc Three() int { return 30 }\n")
	writeFile(t, filepath.Join(dir, "app.py"), "def run():\n    return 2\n")
	stdout.Reset()
	stderr.Reset()
	so := env.options(nil)
	so.Stdout, so.Stderr = &stdout, &stderr
	if _, err := Sync(context.Background(), SyncOptions{Options: so, Name: "g"}); err != nil {
		t.Fatalf("Sync: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "Go module") || strings.Contains(stderr.String(), "Go file(s)") {
		t.Errorf("a non-Go tree with ignored .go fixtures warned on sync: %q", stderr.String())
	}
}

// `.go` files whose own base name starts with "_" or "." are no package of the
// Go tool either ("go list ./..." sees zero Go source in a directory holding
// only those), so a non-Go repository containing them must build and sync
// without any warning on either stream.
func TestFormat2NonGoTreeWithUnderscoreGoFilesNoWarn(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.py"), "def run():\n    return 1\n")
	writeFile(t, filepath.Join(dir, "_fixture.go"), "package fixture\n\n// One is a fixture.\nfunc One() int { return 1 }\n")
	writeFile(t, filepath.Join(dir, ".fixture.go"), "package fixture\n\n// Hidden is hidden.\nfunc Hidden() int { return 2 }\n")
	writeFile(t, filepath.Join(dir, "pkg", "_gen.go"), "package gen\n\n// Three is generated.\nfunc Three() int { return 3 }\n")

	warned := func(s string) bool {
		return strings.Contains(s, "warning") || strings.Contains(s, "Go module") || strings.Contains(s, "Go file(s)")
	}

	var stdout, stderr bytes.Buffer
	o := env.options(nil)
	o.Stdout, o.Stderr = &stdout, &stderr
	if _, err := Build(context.Background(), BuildOptions{Options: o, Name: "g", DocsDir: dir, Graph: true}); err != nil {
		t.Fatalf("Build: %v\n%s", err, stderr.String())
	}
	if s := stdout.String(); warned(s) {
		t.Errorf("a non-Go tree with _/. Go files warned on build (stdout): %q", s)
	}
	if s := stderr.String(); warned(s) {
		t.Errorf("a non-Go tree with _/. Go files warned on build (stderr): %q", s)
	}

	writeFile(t, filepath.Join(dir, "_fixture.go"), "package fixture\n\n// One is an edited fixture.\nfunc One() int { return 10 }\n")
	writeFile(t, filepath.Join(dir, "pkg", "_gen.go"), "package gen\n\n// Three is regenerated.\nfunc Three() int { return 30 }\n")
	writeFile(t, filepath.Join(dir, "app.py"), "def run():\n    return 2\n")
	stdout.Reset()
	stderr.Reset()
	so := env.options(nil)
	so.Stdout, so.Stderr = &stdout, &stderr
	if _, err := Sync(context.Background(), SyncOptions{Options: so, Name: "g"}); err != nil {
		t.Fatalf("Sync: %v\n%s", err, stderr.String())
	}
	if s := stdout.String(); warned(s) {
		t.Errorf("a non-Go tree with _/. Go files warned on sync (stdout): %q", s)
	}
	if s := stderr.String(); warned(s) {
		t.Errorf("a non-Go tree with _/. Go files warned on sync (stderr): %q", s)
	}
}

// A go.work in a subdirectory that lists a sibling module with "use ../a" must
// govern that module: exact CALLS across the workspace, not a syntactic
// collapse to GOWORK=off.
func TestFormat2ExactGoWorkOutsideModuleDir(t *testing.T) {
	isolateEnv(t)
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "go.work"), "go 1.21\n\nuse (\n\t../a\n\t./b\n)\n")
	writeFile(t, filepath.Join(dir, "a", "go.mod"), "module example.com/a\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a", "a.go"), `package a

import "example.com/b"

// Use calls a function of the other workspace module.
func Use() int { return b.Value() }
`)
	writeFile(t, filepath.Join(dir, "sub", "b", "go.mod"), "module example.com/b\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub", "b", "b.go"), `package b

// Value is imported by the other workspace module.
func Value() int { return 7 }
`)
	buildFormat2(t, env, dir)
	if conf, ext := callEdge(t, env, "example.com/a.Use", "example.com/b.Value"); conf != "exact" || ext != 0 {
		t.Errorf("CALLS a.Use -> b.Value = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	assertNoOrphans(t, openGraphDB(t, env))
}
