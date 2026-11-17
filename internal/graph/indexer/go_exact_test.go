package indexer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

const exactModule = "example.com/app"

// writeTree materialises files (slash paths) under a fresh temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var exactFixture = map[string]string{
	"go.mod": "module " + exactModule + "\n\ngo 1.21\n",
	"app.go": `package app

import (
	"fmt"

	"example.com/app/shape"
)

type Stack[T any] struct{ items []T }

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

func Identity[T any](v T) T { return v }

func Run() string {
	var s shape.Shape = shape.NewSquare(2)
	st := &Stack[int]{}
	st.Push(Identity[int](1))
	f := (shape.NewSquare)
	_ = f
	m := s.Area
	_ = m
	return fmt.Sprint(s.Area()) + helper()
}

func helper() string {
	helper := func() string { return "shadow" }
	return helper()
}

func Builtin() int { return len("abc") }
`,
	"shape/shape.go": `package shape

type Shape interface{ Area() int }

type Empty interface{}

type Square struct{ side int }

func NewSquare(n int) *Square { return &Square{side: n} }

func (s *Square) Area() int { return s.side * s.side }

type Circle struct{}

func (Circle) Area() int { return 3 }

type Plain struct{}
`,
	"broken/broken.go": `package broken

func Bad() int { return undefinedName }

func Caller() { Bad() }
`,
}

func loadExactFixture(t *testing.T) (*ExactResolver, string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	root := writeTree(t, exactFixture)
	r, err := LoadExact(exactModule, root)
	if err != nil {
		t.Fatalf("LoadExact: %v", err)
	}
	if r == nil {
		t.Skip("go/packages could not type-check the fixture in this environment")
	}
	return r, root
}

func callsOf(fg FileGraph, caller string) map[string]string {
	out := map[string]string{}
	for _, c := range fg.Calls {
		if c.CallerFQN == caller {
			out[c.CalleeFQN] = c.Confidence
		}
	}
	return out
}

func TestLoadExactNotAModuleFallsBack(t *testing.T) {
	root := t.TempDir() // no go.mod
	r, err := LoadExact("m", root)
	if r != nil || err != nil {
		t.Fatalf("LoadExact(non-module) = %v, %v; want nil, nil", r, err)
	}
}

func TestExactResolverNilIsSafe(t *testing.T) {
	var r *ExactResolver
	if r.HasFile("a.go") {
		t.Error("nil resolver must not claim files")
	}
	if got := r.ImplementsFor("a.go"); got != nil {
		t.Errorf("nil resolver ImplementsFor = %v", got)
	}
}

func TestLoadExactSkipsPackagesWithTypeErrors(t *testing.T) {
	r, _ := loadExactFixture(t)
	for rel, want := range map[string]bool{
		"app.go":            true,
		"shape/shape.go":    true,
		"broken/broken.go":  false, // type error: stays syntactic
		"does/not/exist.go": false,
	} {
		if got := r.HasFile(rel); got != want {
			t.Errorf("HasFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestExactResolverImplementsFromMethodSets(t *testing.T) {
	r, _ := loadExactFixture(t)
	got := map[string]string{}
	for _, e := range r.ImplementsFor("shape/shape.go") {
		got[e.ImplFQN+"→"+e.IfaceFQN] = e.Confidence
	}
	pre := exactModule + "/shape."
	// *Square (pointer receiver) and Circle (value receiver) both implement Shape.
	for _, c := range []string{"Square", "Circle"} {
		if got[pre+c+"→"+pre+"Shape"] != "exact" {
			t.Errorf("missing exact edge %s -> Shape in %v", c, got)
		}
	}
	for k := range got {
		if strings.Contains(k, "Plain") {
			t.Errorf("Plain has no methods but got %s", k)
		}
		if strings.HasSuffix(k, "Empty") {
			t.Errorf("empty interface must be skipped: %s", k)
		}
	}
	if len(r.ImplementsFor("app.go")) != 0 {
		t.Errorf("app.go declares no implementors: %v", r.ImplementsFor("app.go"))
	}
}

func TestExtractFileExactResolvesTypedCalls(t *testing.T) {
	r, root := loadExactFixture(t)
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "app.go"), exactFixture["app.go"], r)

	run := callsOf(fg, exactModule+".Run")
	want := []string{
		exactModule + "/shape.NewSquare",
		exactModule + "/shape.Shape.Area", // interface dispatch, call and method value
		exactModule + ".Stack.Push",       // generic receiver keeps origin name
		exactModule + ".Identity",         // generic instantiation F[int](...)
		exactModule + ".helper",           // same package
		"fmt.Sprint",                      // external: import path prefix
	}
	for _, w := range want {
		if run[w] != "exact" {
			t.Errorf("Run -> %s missing or not exact; calls = %v", w, run)
		}
	}
	// len() is a builtin: no edge.
	if c := callsOf(fg, exactModule+".Builtin"); len(c) != 0 {
		t.Errorf("builtin call produced edges: %v", c)
	}
	// helper() calls its shadowing local closure, a *types.Var rather than the
	// package-level function: no edge may be produced.
	if c := callsOf(fg, exactModule+".helper"); len(c) != 0 {
		t.Errorf("shadowed local call resolved to a function: %v", c)
	}
	var runSym *gtypes.SymbolNode
	for i := range fg.Symbols {
		if fg.Symbols[i].FQN == exactModule+".Run" {
			runSym = &fg.Symbols[i]
		}
	}
	if runSym == nil || runSym.File != "app.go" || runSym.Line == 0 || runSym.StartCol == 0 || runSym.ByteOffset == 0 {
		t.Errorf("Run symbol not anchored: %+v", runSym)
	}
}

func TestExtractFileExactMergesMethodSetImplements(t *testing.T) {
	r, root := loadExactFixture(t)
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "shape", "shape.go"), exactFixture["shape/shape.go"], r)
	found := false
	for _, e := range fg.Implements {
		if e.ImplFQN == exactModule+"/shape.Square" && e.IfaceFQN == exactModule+"/shape.Shape" {
			found = e.Confidence == "exact"
		}
	}
	if !found {
		t.Errorf("Square IMPLEMENTS Shape (exact) not in %+v", fg.Implements)
	}
}

func TestExtractFileExactFallsBackForUntypedFiles(t *testing.T) {
	r, root := loadExactFixture(t)
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "broken", "broken.go"), exactFixture["broken/broken.go"], r)
	calls := callsOf(fg, exactModule+"/broken.Caller")
	if calls[exactModule+"/broken.Bad"] != "extracted" {
		t.Errorf("file with type errors must use the syntactic resolver; calls = %v", calls)
	}
	// A nil resolver is equivalent to ExtractFile.
	a := ExtractFileExact(exactModule, root, filepath.Join(root, "app.go"), exactFixture["app.go"], nil)
	b := ExtractFile(exactModule, root, filepath.Join(root, "app.go"), exactFixture["app.go"])
	if len(a.Calls) != len(b.Calls) || len(a.Symbols) != len(b.Symbols) {
		t.Errorf("nil resolver differs from ExtractFile: %d/%d calls, %d/%d symbols", len(a.Calls), len(b.Calls), len(a.Symbols), len(b.Symbols))
	}
	for _, c := range a.Calls {
		if c.Confidence != "extracted" {
			t.Errorf("syntactic edge labelled %q", c.Confidence)
		}
	}
}

func TestDedupeImplementsPrefersExact(t *testing.T) {
	in := []gtypes.EdgeImplements{
		{ImplFQN: "p.A", IfaceFQN: "p.I", Confidence: "extracted"},
		{ImplFQN: "p.B", IfaceFQN: "p.I", Confidence: "exact"},
		{ImplFQN: "p.A", IfaceFQN: "p.I", Confidence: "exact"},
		{ImplFQN: "p.B", IfaceFQN: "p.I", Confidence: "extracted"},
	}
	out := dedupeImplements(in)
	if len(out) != 2 {
		t.Fatalf("len = %d: %+v", len(out), out)
	}
	for _, e := range out {
		if e.Confidence != "exact" {
			t.Errorf("%s kept weaker label %q", e.ImplFQN, e.Confidence)
		}
	}
	if out[0].ImplFQN != "p.A" || out[1].ImplFQN != "p.B" {
		t.Errorf("first-seen order not preserved: %+v", out)
	}
}
