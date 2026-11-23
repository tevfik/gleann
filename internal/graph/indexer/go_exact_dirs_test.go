package indexer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

func needGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
}

func sortedCalls(fg FileGraph) []gtypes.EdgeCalls {
	out := append([]gtypes.EdgeCalls(nil), fg.Calls...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].CallerFQN != out[j].CallerFQN {
			return out[i].CallerFQN < out[j].CallerFQN
		}
		return out[i].CalleeFQN < out[j].CalleeFQN
	})
	return out
}

func implKeys(edges []gtypes.EdgeImplements) []string {
	var out []string
	for _, e := range edges {
		out = append(out, e.ImplFQN+" -> "+e.IfaceFQN+" "+e.Confidence)
	}
	sort.Strings(out)
	return out
}

// A partial load type-checks only the listed directory, and the files it
// covers get the CALLS edges the full load gives them, although the packages
// they import come from export data instead of source.
func TestLoadExactDirsMatchesFullLoadForItsFiles(t *testing.T) {
	full, root := loadExactFixture(t)
	part, err := LoadExactDirs(exactModule, root, []string{"."})
	if err != nil || part == nil {
		t.Fatalf("LoadExactDirs(.) = %v, %v", part, err)
	}
	if !part.Partial() || full.Partial() {
		t.Errorf("Partial(): dirs load %v, full load %v; want true, false", part.Partial(), full.Partial())
	}
	if !part.HasFile("app.go") || part.HasFile("shape/shape.go") {
		t.Fatalf("the load of . covers app.go=%v shape/shape.go=%v; want true, false", part.HasFile("app.go"), part.HasFile("shape/shape.go"))
	}
	abs := filepath.Join(root, "app.go")
	want := sortedCalls(ExtractFileExact(exactModule, root, abs, exactFixture["app.go"], full))
	got := sortedCalls(ExtractFileExact(exactModule, root, abs, exactFixture["app.go"], part))
	if len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("CALLS of app.go differ\npartial load: %+v\nfull load:    %+v", got, want)
	}
	// A file the load does not cover falls back to the syntactic resolver.
	fg := ExtractFileExact(exactModule, root, filepath.Join(root, "shape", "shape.go"), exactFixture["shape/shape.go"], part)
	for _, c := range fg.Calls {
		if c.Confidence == "exact" {
			t.Errorf("exact edge in a file the load does not cover: %+v", c)
		}
	}
}

// Packages with errors are skipped and counted; when none is left the load
// reports why.
func TestLoadExactDirsSkipsAndReportsBrokenPackages(t *testing.T) {
	needGo(t)
	root := writeTree(t, exactFixture)

	r, err := LoadExactDirs(exactModule, root, []string{"shape", "broken"})
	if err != nil || r == nil {
		t.Fatalf("LoadExactDirs(shape, broken) = %v, %v", r, err)
	}
	if !r.HasFile("shape/shape.go") || r.HasFile("broken/broken.go") {
		t.Errorf("covers shape=%v broken=%v; want true, false", r.HasFile("shape/shape.go"), r.HasFile("broken/broken.go"))
	}
	if n, first := r.Skipped(); n != 1 || !strings.Contains(first, "undefinedName") {
		t.Errorf("Skipped() = %d, %q; want 1 and the type error", n, first)
	}

	r, err = LoadExactDirs(exactModule, root, []string{"broken"})
	if r != nil || !errors.Is(err, errNothingTypeChecked) || !strings.Contains(err.Error(), "undefinedName") {
		t.Errorf("LoadExactDirs(broken) = %v, %v; want nil and errNothingTypeChecked naming the error", r, err)
	}
}

// Nothing to load is not an error: no module, no directory, or only
// directories that are not a package of any discovered module.
func TestLoadExactDirsNothingToLoad(t *testing.T) {
	needGo(t)
	if r, err := LoadExactDirs("m", t.TempDir(), []string{"."}); r != nil || err != nil {
		t.Errorf("outside a module: %v, %v; want nil, nil", r, err)
	}
	files := map[string]string{
		"nested/go.mod":   "module example.com/nested\n\ngo 1.21\n",
		"nested/n.go":     "package nested\n\nfunc N() {}\n",
		"testdata/t.go":   "package testdata\n\nfunc T() {}\n",
		"docs/readme.txt": "no Go here\n",
		"onlytest/x_test.go": `package onlytest

import "testing"

func TestX(t *testing.T) {}
`,
	}
	for rel, content := range exactFixture {
		files[rel] = content
	}
	root := writeTree(t, files)
	for _, dirs := range [][]string{nil, {"docs"}, {"onlytest"}, {"missing"}, {"docs", "testdata", "onlytest", "missing"}} {
		if r, err := LoadExactDirs(exactModule, root, dirs); r != nil || err != nil {
			t.Errorf("LoadExactDirs(%v) = %v, %v; want nil, nil", dirs, r, err)
		}
	}
	// A nested module directory is loaded by its own go.mod (3.3-nested),
	// with the nested module's path as prefix, not the root module's.
	r, err := LoadExactDirs(exactModule, root, []string{"nested"})
	if err != nil || r == nil || !r.HasFile("nested/n.go") {
		t.Errorf("LoadExactDirs(nested) = %v, %v; want the nested module loaded", r, err)
	}
	// Such a directory next to a package of the root module does not spoil it.
	r, err = LoadExactDirs(exactModule, root, []string{"nested", "docs", "shape", "missing"})
	if err != nil || !r.HasFile("shape/shape.go") || !r.HasFile("nested/n.go") {
		t.Errorf("mixed load = %v, %v; want shape and nested covered", r, err)
	}
	if n, _ := r.Skipped(); n != 0 {
		t.Errorf("Skipped() = %d for directories that type-check or hold no package, want 0", n)
	}
}

// A load that fails as a whole is an error the caller reports.
func TestLoadExactDirsReportsFailedLoad(t *testing.T) {
	needGo(t)
	root := writeTree(t, map[string]string{
		"go.mod": "module " + exactModule + "\n\ngo 1.999.0\n",
		"app.go": "package app\n\nfunc A() {}\n",
	})
	if r, err := LoadExactDirs(exactModule, root, []string{"."}); r != nil || err == nil {
		t.Fatalf("LoadExactDirs(go 1.999.0) = %v, %v; want nil and an error", r, err)
	}
}

// The load is bounded: past the bound it is an error that says so, and the
// sync falls back to the syntactic resolver.
func TestLoadExactDirsIsBounded(t *testing.T) {
	needGo(t)
	root := writeTree(t, exactFixture)
	r, err := loadExactDirs(exactModule, root, []string{"."}, time.Nanosecond)
	if r != nil || err == nil || !strings.Contains(err.Error(), "no result within 1ns") {
		t.Fatalf("loadExactDirs with a 1ns bound = %v, %v; want nil and an error naming the bound", r, err)
	}
}

func TestDirPatterns(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/b", "testdata/x", "vendor/y", "_gen", ".hidden", "c/testdata", "c d"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := dirPatterns(root, []string{"a/b", ".", "", "a/b/", "a", "testdata/x", "vendor/y", "_gen", ".hidden", "c/testdata", "missing", "file", "../up", "c d"})
	want := []string{".", "./a", "./a/b", "./c d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dirPatterns = %q, want %q", got, want)
	}
}

// Method-set IMPLEMENTS edges of a partial load: the interfaces of the listed
// packages and the exported interfaces of their direct imports are judged,
// anything else is left to the caller.
func TestLoadExactDirsImplementsAndJudgedInterfaces(t *testing.T) {
	needGo(t)
	const mod = "example.com/impl"
	root := writeTree(t, map[string]string{
		"go.mod": "module " + mod + "\n\ngo 1.21\n",
		"low/low.go": `package low

type Reader interface{ Read() int }

type hidden interface{ Read() int }

var _ hidden

type File struct{}

func (File) Read() int { return 1 }
`,
		"mid/mid.go": `package mid

import "` + mod + `/low"

type Closer interface{ Close() }

type Buf struct{}

func (Buf) Read() int { return 2 }
func (Buf) Close()    {}

func Use(r low.Reader) int { return r.Read() }
`,
		"top/top.go": `package top

import "` + mod + `/mid"

type Both interface {
	Read() int
	Close()
}

func Use(b mid.Buf) { b.Close() }
`,
	})
	full, err := LoadExact(mod, root)
	if err != nil || full == nil {
		t.Fatalf("LoadExact: %v, %v", full, err)
	}
	part, err := LoadExactDirs(mod, root, []string{"mid"})
	if err != nil || part == nil {
		t.Fatalf("LoadExactDirs(mid): %v, %v", part, err)
	}

	// Full load: Buf implements the interface of its own package, of the
	// package it imports (exported and unexported) and of its importer.
	wantFull := []string{
		mod + "/mid.Buf -> " + mod + "/low.Reader exact",
		mod + "/mid.Buf -> " + mod + "/low.hidden exact",
		mod + "/mid.Buf -> " + mod + "/mid.Closer exact",
		mod + "/mid.Buf -> " + mod + "/top.Both exact",
	}
	if got := implKeys(full.ImplementsFor("mid/mid.go")); !reflect.DeepEqual(got, wantFull) {
		t.Fatalf("full load IMPLEMENTS of mid.go = %q, want %q", got, wantFull)
	}
	// Partial load of mid: its own interface and the exported one of low.
	wantPart := []string{
		mod + "/mid.Buf -> " + mod + "/low.Reader exact",
		mod + "/mid.Buf -> " + mod + "/mid.Closer exact",
	}
	if got := implKeys(part.ImplementsFor("mid/mid.go")); !reflect.DeepEqual(got, wantPart) {
		t.Errorf("partial load IMPLEMENTS of mid.go = %q, want %q", got, wantPart)
	}
	for fqn, want := range map[string]bool{
		mod + "/mid.Closer": true,  // listed package
		mod + "/mid.Gone":   true,  // listed package: an interface that is not there was judged too
		mod + "/low.Reader": true,  // direct import, exported
		mod + "/low.hidden": false, // direct import, unexported: not in export data for sure
		mod + "/top.Both":   false, // importer: not loaded
		"nodot":             false,
	} {
		if got := part.JudgedInterface(fqn); got != want {
			t.Errorf("partial JudgedInterface(%s) = %v, want %v", fqn, got, want)
		}
		if !full.JudgedInterface(fqn) {
			t.Errorf("full JudgedInterface(%s) = false; a full load judges every interface", fqn)
		}
	}
	var nilResolver *ExactResolver
	if nilResolver.Partial() || nilResolver.JudgedInterface(mod+"/mid.Closer") {
		t.Error("a nil resolver is neither partial nor a judge")
	}
	if n, first := nilResolver.Skipped(); n != 0 || first != "" {
		t.Errorf("nil Skipped() = %d, %q", n, first)
	}
}

func TestMergeImplementsPrefersExact(t *testing.T) {
	got := MergeImplements(
		[]gtypes.EdgeImplements{{ImplFQN: "p.T", IfaceFQN: "q.I", Confidence: "extracted"}, {ImplFQN: "p.T", IfaceFQN: "p.J", Confidence: "exact"}},
		[]gtypes.EdgeImplements{{ImplFQN: "p.T", IfaceFQN: "q.I", Confidence: "exact"}, {ImplFQN: "p.U", IfaceFQN: "q.I", Confidence: "exact"}},
	)
	want := []string{"p.T -> p.J exact", "p.T -> q.I exact", "p.U -> q.I exact"}
	if keys := implKeys(got); !reflect.DeepEqual(keys, want) {
		t.Errorf("MergeImplements = %q, want %q", keys, want)
	}
}

// TestExactDirsParityOnGleann is the measurement behind LoadExactDirs on
// gleann's own tree: every package directory is loaded on its own, and each
// file's CALLS edges must equal the ones the full load resolves; the
// method-set IMPLEMENTS edges must be the full load's minus the ones to
// interfaces the partial load reports as not judged. Skipped unless
// GLEANN_GRAPH_PRECISION is set (it runs one go list per package).
func TestExactDirsParityOnGleann(t *testing.T) {
	if testing.Short() || os.Getenv("GLEANN_GRAPH_PRECISION") == "" {
		t.Skip("parity measurement: set GLEANN_GRAPH_PRECISION=1 to run")
	}
	needGo(t)
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	if dir := os.Getenv("GLEANN_GRAPH_PARITY_ROOT"); dir != "" {
		root = dir
	}
	module := DetectGoModule(root)
	full, err := LoadExact(module, root)
	if err != nil || full == nil {
		t.Skipf("full load unavailable: %v, %v", full, err)
	}
	byDir := make(map[string][]string)
	for rel := range full.files {
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
		byDir[dir] = append(byDir[dir], rel)
	}
	var files, calls, implSame, implUnjudged int
	for dir, rels := range byDir {
		part, err := LoadExactDirs(module, root, []string{dir})
		if err != nil || part == nil {
			t.Errorf("LoadExactDirs(%s) = %v, %v", dir, part, err)
			continue
		}
		for _, rel := range rels {
			if !part.HasFile(rel) {
				t.Errorf("%s: type-checked by the full load only", rel)
				continue
			}
			abs := filepath.Join(root, filepath.FromSlash(rel))
			src, err := os.ReadFile(abs)
			if err != nil {
				t.Fatal(err)
			}
			want := sortedCalls(ExtractFileExact(module, root, abs, string(src), full))
			got := sortedCalls(ExtractFileExact(module, root, abs, string(src), part))
			files++
			calls += len(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: CALLS differ (partial %d, full %d)", rel, len(got), len(want))
			}
			partImpl := make(map[string]bool)
			for _, k := range implKeys(part.ImplementsFor(rel)) {
				partImpl[k] = true
			}
			fullImpl := make(map[string]bool)
			for _, e := range full.ImplementsFor(rel) {
				k := e.ImplFQN + " -> " + e.IfaceFQN + " " + e.Confidence
				fullImpl[k] = true
				switch {
				case partImpl[k]:
					implSame++
				case part.JudgedInterface(e.IfaceFQN):
					t.Errorf("%s: %s is judged by the partial load but missing from it", rel, k)
				default:
					implUnjudged++
				}
			}
			for k := range partImpl {
				if !fullImpl[k] {
					t.Errorf("%s: %s only in the partial load", rel, k)
				}
			}
		}
	}
	t.Logf("PARITY %d packages, %d files, %d CALLS edges identical; IMPLEMENTS: %d identical, %d to interfaces the partial load does not judge (carried over by the sync)",
		len(byDir), files, calls, implSame, implUnjudged)
}
