package indexer

import (
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGoModule(t *testing.T) {
	cases := []struct {
		name  string
		gomod string // empty = no go.mod
		want  string // empty = base name of dir
	}{
		{"plain", "module github.com/acme/widget\n\ngo 1.22\n", "github.com/acme/widget"},
		{"leading comment and spaces", "// header\n\n  module   example.com/x  \n", "example.com/x"},
		{"no module directive falls back", "go 1.22\n", ""},
		{"absent go.mod falls back", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "proj")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.gomod != "" {
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.gomod), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want := tc.want
			if want == "" {
				want = "proj"
			}
			if got := DetectGoModule(dir); got != want {
				t.Errorf("DetectGoModule = %q, want %q", got, want)
			}
		})
	}
}

func TestPackagePrefix(t *testing.T) {
	cases := []struct{ module, rel, want string }{
		{"example.com/m", "internal/a/x.go", "example.com/m/internal/a"},
		{"example.com/m/", "internal/a/x.go", "example.com/m/internal/a"}, // trailing slash trimmed
		{"example.com/m", "main.go", "example.com/m"},
		{"", "main.go", ""},
		{"", "pkg/x.go", "pkg"},
		{"example.com/m", `win\pkg\x.go`, "example.com/m/win/pkg"},
	}
	for _, tc := range cases {
		if got := PackagePrefix(tc.module, tc.rel); got != tc.want {
			t.Errorf("PackagePrefix(%q,%q) = %q, want %q", tc.module, tc.rel, got, tc.want)
		}
	}
}

func TestIsTestSymbol(t *testing.T) {
	cases := []struct {
		file, name string
		want       bool
	}{
		{"pkg/a_test.go", "helper", true},
		{"pkg/a.go", "TestFoo", true},
		{"pkg/a.go", "BenchmarkFoo", true},
		{"pkg/a.go", "FuzzFoo", true},
		{"pkg/a.go", "test_foo", true},
		{"pkg/test_utils.py", "x", true},
		{"pkg/a_test.py", "x", true},
		{"web/a.spec.ts", "x", true},
		{"web/a.test.ts", "x", true},
		{"web/a.spec.js", "x", true},
		{"web/a.test.js", "x", true},
		{"spec/a_spec.rb", "x", true},
		{"src/FooTest.java", "x", true},
		{"a/test/b.go", "x", true},
		{"a/tests/b.go", "x", true},
		{"a/testing/b.go", "x", true},
		{"pkg/a.go", "Foo", false},
		{"pkg/contest.go", "Contest", false},
		{"pkg/latest/a.go", "Run", false},
	}
	for _, tc := range cases {
		if got := isTestSymbol(tc.file, tc.name); got != tc.want {
			t.Errorf("isTestSymbol(%q,%q) = %v, want %v", tc.file, tc.name, got, tc.want)
		}
	}
}

func TestRelPathOf(t *testing.T) {
	root := t.TempDir()
	if got := relPathOf(root, filepath.Join(root, "a", "b.go")); got != "a/b.go" {
		t.Errorf("inside root: %q", got)
	}

	// A symlinked root: the file path uses the real dir, root the link.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(real, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "x", "y.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := relPathOf(link, filepath.Join(real, "x", "y.go")); got != "x/y.go" {
		t.Errorf("symlinked root: %q", got)
	}

	// Outside the root entirely: a "../" relative path is returned rather than
	// a panic or an empty string.
	other := t.TempDir()
	if got := relPathOf(root, filepath.Join(other, "z.go")); got == "" || got == "z.go" {
		t.Errorf("outside root: %q", got)
	}

	// A relative root with an absolute file cannot be related: absPath returned.
	if got := relPathOf("rel/root", filepath.Join(root, "q.go")); got != filepath.ToSlash(filepath.Join(root, "q.go")) {
		t.Errorf("unrelatable: %q", got)
	}
}

func TestGoDeclAnchors(t *testing.T) {
	src := `package p

type T struct{}

func (t *T) M() {}

func (T) V() {}

func F() {}

func F2() {}

var (
	A = 1
	B = 2
)

const C = 3

type (
	I interface{}
	J int
)
`
	a := goDeclAnchors("p.go", src)
	for _, name := range []string{"T", "T.M", "T.V", "F", "F2", "A", "C", "I"} {
		if _, ok := a[name]; !ok {
			t.Errorf("anchor %q missing: %v", name, a)
		}
	}
	if _, ok := a["B"]; ok {
		t.Error("only the first spec names a var group")
	}
	if _, ok := a["J"]; ok {
		t.Error("only the first spec names a type group")
	}
	if m := a["T.M"]; m.line != 5 || m.endLine != 5 || m.col != 1 || m.offset == 0 {
		t.Errorf("T.M anchor = %+v", m)
	}
	if grp := a["A"]; grp.line != 13 || grp.endLine != 16 {
		t.Errorf("var group should span the whole decl: %+v", grp)
	}
	if goDeclAnchors("bad.go", "package p\nfunc (") != nil {
		t.Error("unparsable source must yield no anchors")
	}
}

func parseExpr(t *testing.T, s string) ast.Expr {
	t.Helper()
	e, err := parser.ParseExpr(s)
	if err != nil {
		t.Fatalf("ParseExpr(%q): %v", s, err)
	}
	return e
}

func TestReceiverAndTypeNameHelpers(t *testing.T) {
	cases := []struct {
		expr                       string
		anchorRecv, exprType, tNam string
	}{
		{"T", "T", "T", "T"},
		{"*T", "T", "T", "T"},
		{"**T", "T", "T", "T"},
		{"pkg.T", "pkg.T", "", "T"},
		{"*pkg.T", "pkg.T", "", "T"},
		{"Stack[int]", "", "Stack", ""},
		{"[]int", "", "", ""},
	}
	for _, tc := range cases {
		e := parseExpr(t, tc.expr)
		if got := anchorRecvName(e); got != tc.anchorRecv {
			t.Errorf("anchorRecvName(%s) = %q, want %q", tc.expr, got, tc.anchorRecv)
		}
		if got := exprTypeName(e); got != tc.exprType {
			t.Errorf("exprTypeName(%s) = %q, want %q", tc.expr, got, tc.exprType)
		}
		if got := typeExprName(e); got != tc.tNam {
			t.Errorf("typeExprName(%s) = %q, want %q", tc.expr, got, tc.tNam)
		}
	}
}

func TestResolveCallFQN(t *testing.T) {
	imports := map[string]string{"forge": "example.com/forge", "str": "strings"}
	local := map[string]string{"Helper": "m/p.Helper", "Agent.Run": "m/p.Agent.Run"}
	cases := []struct {
		call, want string
	}{
		{"Helper()", "m/p.Helper"},
		{"unknown()", "m/p.unknown"}, // same-package function without a chunk
		{"forge.New()", "example.com/forge.New"},
		{"str.ToUpper()", "strings.ToUpper"}, // aliased import
		{"Agent.Run()", "m/p.Agent.Run"},     // local type method expression
		{"a.Run()", ""},                      // unknown receiver variable
		{"a.b.Run()", ""},                    // chained selector
		{"Helper[int]()", "m/p.Helper"},      // generic instantiation
		{"(func())(nil)()", ""},              // non-ident, non-selector callee
	}
	for _, tc := range cases {
		call := parseExpr(t, tc.call).(*ast.CallExpr)
		if got := resolveCallFQN(call.Fun, imports, local, "m/p"); got != tc.want {
			t.Errorf("resolveCallFQN(%s) = %q, want %q", tc.call, got, tc.want)
		}
	}
}

func TestResolveTypeRefFQN(t *testing.T) {
	imports := map[string]string{"io": "io"}
	local := map[string]string{"Local": "m/p.Local"}
	cases := []struct{ typ, want string }{
		{"Local", "m/p.Local"},
		{"Other", "m/p.Other"},
		{"string", ""},
		{"error", ""},
		{"any", ""},
		{"io.Reader", "io.Reader"},
		{"unknownpkg.T", ""},
		{"*Local", "m/p.Local"},
		{"[]Local", "m/p.Local"},
		{"map[string]*Other", "m/p.Other"},
		{"interface{ M() }", ""},
		{"func(int) string", ""},
		{"chan Local", ""}, // unsupported composite
		{"a.b.C", ""},
	}
	for _, tc := range cases {
		e, err := parser.ParseExpr(tc.typ)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if got := resolveTypeRefFQN(e, imports, local, "m/p"); got != tc.want {
			t.Errorf("resolveTypeRefFQN(%s) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

func TestNodeTypeWeight(t *testing.T) {
	cases := map[string]float64{
		"interface": 0.3, "trait": 0.3, "module": 0.2, "namespace": 0.2,
		"impl": 0.15, "class": 0.1, "function": 0, "": 0, "struct": 0,
	}
	for kind, want := range cases {
		if got := nodeTypeWeight(kind); got != want {
			t.Errorf("nodeTypeWeight(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestCollectGoCallQueriesRejectsUnparsableSource(t *testing.T) {
	_, _, err := collectGoCallQueries("m", "p/x.go", "package p\nfunc (", nil)
	if err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestCollectGoCallQueriesCallerFallbacks(t *testing.T) {
	// Without chunks every caller falls back to pkgPrefix.Name, methods to
	// pkgPrefix.Recv.Name; dot and blank imports register no alias.
	src := `package p

import (
	_ "embed"
	. "strings"
	str "strconv"
)

func A() { str.Itoa(1); B() }

func (r *Recv) M() { A(); Recv.M(nil) }

func Skipped()
`
	nodes, edges, err := collectGoCallQueries("m", "p/x.go", src, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range edges {
		got[e.CallerFQN+"→"+e.CalleeFQN] = true
		if e.Confidence != "extracted" {
			t.Errorf("confidence %q", e.Confidence)
		}
	}
	for _, w := range []string{"m/p.A→strconv.Itoa", "m/p.A→m/p.B", "m/p.Recv.M→m/p.A"} {
		if !got[w] {
			t.Errorf("missing edge %s in %v", w, got)
		}
	}
	if got["m/p.Recv.M→m/p.Recv.M"] {
		t.Error("self call must be dropped")
	}
	if len(nodes) != len(edges) {
		t.Errorf("one stub per edge expected: %d nodes, %d edges", len(nodes), len(edges))
	}
}
