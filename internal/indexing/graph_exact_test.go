package indexing

import (
	"path/filepath"
	"testing"
)

// exactFixture is a Go module exercising the cases the syntactic resolver gets
// wrong or cannot resolve (WP-G3): interface dispatch, a promoted (embedded)
// method, aliased and dot imports, a shadowed local function, a method value, a
// generic call, a stdlib callee, and a type implementing an interface by method
// set only (no embedding).
func exactFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/exact\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "iface.go"), `package exact

// Doer does things.
type Doer interface {
	Do() string
}

// Worker implements Doer by method set only (no embedding).
type Worker struct{}

// Do implements Doer.
func (Worker) Do() string { return "worked" }

// RunDoer dispatches through the interface.
func RunDoer(d Doer) string { return d.Do() }
`)
	writeFile(t, filepath.Join(dir, "embed.go"), `package exact

// Base has the promoted method.
type Base struct{}

// Hello is promoted into Wrapper.
func (Base) Hello() string { return "hi" }

// Wrapper embeds Base.
type Wrapper struct{ Base }

// UseWrapper calls the promoted method.
func UseWrapper(w Wrapper) string { return w.Hello() }
`)
	writeFile(t, filepath.Join(dir, "shadow.go"), `package exact

// Target is the package-level function.
func Target() int { return 42 }

// Shadow shadows Target with a local func value.
func Shadow() int {
	Target := func() int { return 1 }
	return Target()
}
`)
	writeFile(t, filepath.Join(dir, "methodvalue.go"), `package exact

// TakeValue captures a method value.
func TakeValue(w Worker) func() string {
	f := w.Do
	return f
}
`)
	writeFile(t, filepath.Join(dir, "generic.go"), `package exact

// Identity is generic.
func Identity[T any](x T) T { return x }

// UseGeneric calls the generic function.
func UseGeneric() int { return Identity[int](5) }
`)
	writeFile(t, filepath.Join(dir, "dotimport.go"), `package exact

import . "example.com/exact/sub"

// UseDot calls a dot-imported function.
func UseDot() int { return Helper() }
`)
	writeFile(t, filepath.Join(dir, "alias.go"), `package exact

import renamed "example.com/exact/sub"

// UseAlias calls an aliased import.
func UseAlias() int { return renamed.Helper() }
`)
	writeFile(t, filepath.Join(dir, "external.go"), `package exact

import "fmt"

// UseExternal calls a stdlib function.
func UseExternal() string { return fmt.Sprintf("%d", 1) }
`)
	writeFile(t, filepath.Join(dir, "sub", "sub.go"), `package sub

// Helper is imported by the alias and dot-import callers.
func Helper() int { return 7 }
`)
	return dir
}

// callEdge returns the confidence and dst_external of a CALLS edge, or fails.
func callEdge(t *testing.T, env *testEnv, src, dst string) (confidence string, external int) {
	t.Helper()
	db := openGraphDB(t, env)
	err := db.QueryRow(`SELECT confidence, dst_external FROM edges WHERE kind = 'CALLS' AND src = ? AND dst = ?`, src, dst).
		Scan(&confidence, &external)
	if err != nil {
		t.Fatalf("CALLS edge %s -> %s: %v", src, dst, err)
	}
	return confidence, external
}

// TestFormat2ExactGoResolution asserts the exact go/types edges and their
// "exact" labels. These assertions fail on the syntactic-only resolver: it
// cannot resolve interface dispatch, promoted methods, method values or
// dot-imports, and it wrongly resolves a shadowed local to the package function.
func TestFormat2ExactGoResolution(t *testing.T) {
	env := newEnv(t)
	docs := exactFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)

	const pkg = "example.com/exact"

	// Interface dispatch resolves to the interface method; an interface method
	// is not a declared symbol, so the edge is bound to the declared interface.
	if conf, ext := callEdge(t, env, pkg+".RunDoer", pkg+".Doer"); conf != "exact" || ext != 0 {
		t.Errorf("interface call RunDoer -> Doer = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	// Promoted method resolves to the declaring type (Base), not Wrapper.
	if conf, ext := callEdge(t, env, pkg+".UseWrapper", pkg+".Base.Hello"); conf != "exact" || ext != 0 {
		t.Errorf("promoted call UseWrapper -> Base.Hello = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	// Aliased import.
	if conf, ext := callEdge(t, env, pkg+".UseAlias", pkg+"/sub.Helper"); conf != "exact" || ext != 0 {
		t.Errorf("aliased call UseAlias -> sub.Helper = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	// Dot-import resolves to the imported package, not the local one.
	if conf, ext := callEdge(t, env, pkg+".UseDot", pkg+"/sub.Helper"); conf != "exact" || ext != 0 {
		t.Errorf("dot-import call UseDot -> sub.Helper = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind='CALLS' AND src=? AND dst=?`, pkg+".UseDot", pkg+".Helper"); n != 0 {
		t.Errorf("dot-import mis-resolved to local %s.Helper (%d edges)", pkg, n)
	}
	// Method value resolves to the method.
	if conf, ext := callEdge(t, env, pkg+".TakeValue", pkg+".Worker.Do"); conf != "exact" || ext != 0 {
		t.Errorf("method value TakeValue -> Worker.Do = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	// Generic function call.
	if conf, ext := callEdge(t, env, pkg+".UseGeneric", pkg+".Identity"); conf != "exact" || ext != 0 {
		t.Errorf("generic call UseGeneric -> Identity = (%q, external=%d), want (exact, internal)", conf, ext)
	}
	// Shadowed local function must NOT resolve to the package-level Target.
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind='CALLS' AND src=? AND dst=?`, pkg+".Shadow", pkg+".Target"); n != 0 {
		t.Errorf("shadowed local resolved to package-level Target (%d edges), want 0", n)
	}
	// Standard library callee stays external.
	if conf, ext := callEdge(t, env, pkg+".UseExternal", "fmt.Sprintf"); conf != "exact" || ext != 1 {
		t.Errorf("stdlib call UseExternal -> fmt.Sprintf = (%q, external=%d), want (exact, external)", conf, ext)
	}

	// IMPLEMENTS by method set (Worker has Do, no embedding), labelled exact.
	var implConf string
	if err := db.QueryRow(`SELECT confidence FROM edges WHERE kind='IMPLEMENTS' AND src=? AND dst=?`, pkg+".Worker", pkg+".Doer").Scan(&implConf); err != nil {
		t.Fatalf("method-set IMPLEMENTS Worker -> Doer: %v", err)
	}
	if implConf != "exact" {
		t.Errorf("method-set IMPLEMENTS confidence = %q, want exact", implConf)
	}

	assertNoOrphans(t, db)
}

// TestFormat2ExactFallbackNotAModule checks that a tree without go.mod builds
// with the syntactic resolver and does not abort (no exact edges, no error).
func TestFormat2ExactFallbackNotAModule(t *testing.T) {
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package plain\n\nfunc One() int { return 1 }\n\nfunc Two() int { return One() }\n")
	buildFormat2(t, env, dir)
	db := openGraphDB(t, env)
	// Without go.mod, DetectGoModule falls back to the directory's base name.
	prefix := filepath.Base(dir)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind='CALLS' AND src=? AND dst=?`, prefix+".Two", prefix+".One"); n != 1 {
		t.Fatalf("syntactic fallback edge Two -> One = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE confidence='exact'`); n != 0 {
		t.Fatalf("non-module tree produced %d exact edges, want 0", n)
	}
	assertNoOrphans(t, db)
}

// TestFormat2ExactFallbackTypeErrors checks that a module with type errors
// falls back to the syntactic resolver per package without aborting the build.
func TestFormat2ExactFallbackTypeErrors(t *testing.T) {
	env := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/broken\n\ngo 1.25\n")
	// Undefined symbol: the package does not type-check.
	writeFile(t, filepath.Join(dir, "a.go"), "package broken\n\nfunc One() int { return missing() }\n\nfunc Two() int { return One() }\n")
	buildFormat2(t, env, dir)
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind='CALLS' AND src='example.com/broken.Two' AND dst='example.com/broken.One'`); n != 1 {
		t.Fatalf("syntactic fallback edge Two -> One = %d, want 1", n)
	}
	assertNoOrphans(t, db)
}

// Without a go toolchain on PATH (an end-user machine) the go/packages load
// fails; the full build must still produce the syntactic graph.
func TestFormat2ExactFallbackNoGoToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = 'example.com/fx.main' AND dst = 'example.com/fx.Helper'`); n != 1 {
		t.Fatalf("CALLS main -> Helper = %d without a go toolchain, want 1 (syntactic)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE confidence = 'exact'`); n != 0 {
		t.Errorf("%d exact edge(s) without a go toolchain", n)
	}
}
