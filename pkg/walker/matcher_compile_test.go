package walker

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uncompiled is p as a test or an older caller would build it: only the
// fields read from the ignore file, so matchScopedPattern takes the general
// filepath.Match path.
func uncompiled(p scopedPattern) scopedPattern {
	return scopedPattern{scopeDir: p.scopeDir, raw: p.raw, negate: p.negate, dirOnly: p.dirOnly, glob: p.glob}
}

// TestCompiledPatternMatchesLikeFilepathMatch pins the literal / suffix /
// prefix shortcuts of a compiled pattern to the filepath.Match evaluation they
// replace: the two must agree on every pattern and path, or a walk would index
// a different set of files.
func TestCompiledPatternMatchesLikeFilepathMatch(t *testing.T) {
	globs := []string{
		"node_modules", "*.log", "coverage*", "*", "", "a", ".DS_Store", "*~", "*.passages.jsonl",
		"LOCAL_*.md", "?", "a?c", "[ab]c", "*.[oa]", "a*b", "**", "*.*", "x*", "*x", "*.tar.gz",
		`a\*`, `*\.go`, `back\slash`, "c:x", "*:x", "c:*",
		"/bin", "/gleann", "build/out", "e2e/results/*.html", "**/.gleann", "a/**/b", "src/*", "*/tmp",
		"deps/lib/libopenblas*", "/", "a/", "ünï", "*ö", "ü*",
	}
	segs := []string{
		"node_modules", "a", "b", "c", "x", "ab", "abc", "ac", "bc", "a.log", ".log", "log", "x.o", "x.a",
		"coverage", "coverage.out", "cov", ".DS_Store", "file~", "~", "t.passages.jsonl", "LOCAL_x.md",
		"LOCAL_.md", "local_x.md", "a*", "a*b", `a\*`, "x.go", "back", "slash", `back\slash`, "c:x", "c:", ":x",
		"bin", "gleann", "build", "out", "e2e", "results", "r.html", ".gleann", "src", "tmp", "deps", "lib",
		"libopenblas.so", "x.tar.gz", "tar.gz", "ünï", "ö", "xö", "üx", "*",
	}
	scopes := []string{"", "sub", "sub/dir", "a", "build", "node_modules"}

	rng := rand.New(rand.NewSource(7))
	check := func(scope, glob, path string, isDir bool) {
		p := scopedPattern{scopeDir: scope, glob: glob}
		want := matchScopedPattern(p, path, isDir)
		p.compile()
		if got := matchScopedPattern(p, path, isDir); got != want {
			t.Fatalf("glob %q in scope %q on %q (dir=%v): compiled (kind %d, literal %q) = %v, filepath.Match evaluation = %v",
				glob, scope, path, isDir, p.nameKind, p.nameLit, got, want)
		}
	}
	for _, scope := range scopes {
		for _, glob := range globs {
			for _, seg := range segs {
				check(scope, glob, seg, false)
				check(scope, glob, seg, true)
				if scope != "" {
					check(scope, glob, scope+"/"+seg, false)
				}
			}
			check(scope, glob, scope, true)
			for n := 0; n < 300; n++ {
				parts := make([]string, 1+rng.Intn(5))
				for i := range parts {
					parts[i] = segs[rng.Intn(len(segs))]
				}
				path := strings.Join(parts, "/")
				if scope != "" && rng.Intn(2) == 0 {
					path = scope + "/" + path
				}
				check(scope, glob, path, rng.Intn(2) == 0)
			}
		}
	}
}

// TestAddPatternCompiles checks that the patterns a Matcher loads carry their
// derived form, and what that form is for the common shapes.
func TestAddPatternCompiles(t *testing.T) {
	m := &Matcher{loadedDirs: map[string]bool{}, submodules: map[string]bool{}}
	for _, line := range []string{"node_modules/", "*.log", "coverage*", "!keep.log", "/bin/", "a?c", "docs/*.md"} {
		m.addPattern("sub", line)
	}
	want := []struct {
		kind nameKind
		lit  string
	}{
		{nameLiteral, "node_modules"}, {nameSuffix, ".log"}, {namePrefix, "coverage"}, {nameLiteral, "keep.log"},
		{nameGlob, ""}, {nameGlob, ""}, {nameGlob, ""},
	}
	if len(m.patterns) != len(want) {
		t.Fatalf("loaded %d patterns, want %d", len(m.patterns), len(want))
	}
	for i, p := range m.patterns {
		if p.nameKind != want[i].kind || p.nameLit != want[i].lit || p.scopePrefix != "sub/" {
			t.Errorf("pattern %q: kind %d literal %q scope prefix %q, want kind %d literal %q scope prefix %q",
				p.raw, p.nameKind, p.nameLit, p.scopePrefix, want[i].kind, want[i].lit, "sub/")
		}
	}
}

// TestShouldIgnoreSameWithAndWithoutCompiledPatterns walks a tree with nested
// ignore files and compares every decision of the Matcher with the decision
// of a Matcher whose patterns were stripped of their derived form.
func TestShouldIgnoreSameWithAndWithoutCompiledPatterns(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.log\n/bin/\ncoverage*\n!keep.log\ntmp/\ndocs/*.html\n**/cache/\nLOCAL_*.md\n")
	write("sub/.gitignore", "*.gen.go\nout\n!important.log\n")
	write("sub/deep/.gleannignore", "secret*\n*.bak\n")
	paths := []string{
		"a.go", "a.log", "keep.log", "bin/x", "bin", "x/bin/y", "coverage.out", "coverage", "tmp/a", "tmp",
		"docs/a.html", "docs/a.md", "docs/sub/a.html", "x/cache/y", "cache", "LOCAL_notes.md", "LOCAL.md",
		"sub/a.gen.go", "sub/a.go", "sub/out", "sub/out/x.go", "sub/important.log", "sub/b.log",
		"sub/deep/secret.txt", "sub/deep/a.bak", "sub/deep/a.go", "sub/deep", "sub", "other/a.gen.go",
		"other/secret.txt", "other/out",
	}
	for _, rel := range paths {
		write(rel+".d/placeholder", "") // make the parent directories exist for nested ignore loading
	}
	fast := NewMatcher(root, false, []string{"extra*", "*.tmp"})
	slow := NewMatcher(root, false, []string{"extra*", "*.tmp"})
	strip := func() {
		for i := range slow.patterns {
			slow.patterns[i] = uncompiled(slow.patterns[i])
		}
	}
	for _, rel := range append(paths, "extra.txt", "a.tmp", "sub/extra", "sub/deep/x.tmp") {
		for _, isDir := range []bool{false, true} {
			strip() // nested ignore files load lazily: strip again after every call
			want := slow.ShouldIgnore(rel, isDir)
			if got := fast.ShouldIgnore(rel, isDir); got != want {
				t.Errorf("ShouldIgnore(%q, dir=%v) = %v with compiled patterns, %v without", rel, isDir, got, want)
			}
		}
	}
	if len(fast.patterns) != len(slow.patterns) || len(fast.patterns) < 14 {
		t.Fatalf("matchers loaded %d and %d patterns", len(fast.patterns), len(slow.patterns))
	}
}

func benchmarkShouldIgnore(b *testing.B, compiled bool) {
	m := &Matcher{loadedDirs: map[string]bool{}, submodules: map[string]bool{}}
	for _, line := range []string{
		"/bin/", "/gleann", "*.exe", "/build/", "/dist/", ".idea/", "*.swp", "*.swo", "*~", ".DS_Store",
		"Thumbs.db", "coverage*.out", "coverage.html", "*.test", "indexes/", "*.index", "*.passages.jsonl",
		"*.meta.json", "/tmp/", "*.log", "**/.gleann/", "LOCAL_*.md", "GRAPH_REPORT.md", "notes/",
		"e2e/results/*.html", "tests/benchmarks/results/", "bench/results/*", "!bench/results/README.md",
	} {
		m.addPattern("", line)
	}
	if !compiled {
		for i := range m.patterns {
			m.patterns[i] = uncompiled(m.patterns[i])
		}
	}
	paths := []string{
		"internal/store/passages.go", "pkg/gleann/builder_format2.go", "cmd/gleann/main.go", "README.md",
		"internal/graph/indexer/extract.go", "docs/plans/003-store.md", "modules/chunking/ast_chunker.go",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			if m.ShouldIgnore(p, false) {
				b.Fatalf("%s ignored", p)
			}
		}
	}
}

func BenchmarkShouldIgnoreCompiled(b *testing.B)   { benchmarkShouldIgnore(b, true) }
func BenchmarkShouldIgnoreUncompiled(b *testing.B) { benchmarkShouldIgnore(b, false) }
