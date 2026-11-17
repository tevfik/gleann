package walker

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestSniffBytes_ExtensionTable pins the extension → category/language map
// the indexing pipeline routes files by (code chunker vs doc chunker vs skip).
func TestSniffBytes_ExtensionTable(t *testing.T) {
	cases := []struct {
		name string
		cat  FileCategory
		lang string
	}{
		{"a.go", CategoryCode, "go"},
		{"a.pyw", CategoryCode, "python"},
		{"a.mjs", CategoryCode, "javascript"},
		{"a.cts", CategoryCode, "typescript"},
		{"a.tsx", CategoryCode, "react"},
		{"a.rs", CategoryCode, "rust"},
		{"a.h", CategoryCode, "c"},
		{"a.hpp", CategoryCode, "cpp"},
		{"A.java", CategoryCode, "java"},
		{"a.kts", CategoryCode, "kotlin"},
		{"a.cs", CategoryCode, "csharp"},
		{"a.rb", CategoryCode, "ruby"},
		{"a.php", CategoryCode, "php"},
		{"a.zsh", CategoryCode, "bash"},
		{"a.sql", CategoryCode, "sql"},
		{"README.MD", CategoryDoc, "markdown"},
		{"a.adoc", CategoryDoc, "markdown"},
		{"a.yml", CategoryConfig, "yml"},
		{"a.toml", CategoryConfig, "toml"},
		{"a.env", CategoryConfig, "env"},
		{"a.tsv", CategoryData, "tsv"},
		{"a.jsonl", CategoryData, "jsonl"},
		{"a.unknownext", CategoryUnknown, ""},
		{"noext", CategoryUnknown, ""},
		// Well-known file names win over the extension.
		{"Dockerfile.dev", CategoryConfig, "dockerfile"},
		{"Containerfile", CategoryConfig, "dockerfile"},
		{"GNUmakefile", CategoryConfig, "makefile"},
		{"Justfile", CategoryConfig, "makefile"},
		{"CMakeLists.txt", CategoryConfig, "cmake"},
		{"Gemfile", CategoryCode, "ruby"},
		{"Rakefile", CategoryCode, "ruby"},
		{"Procfile", CategoryConfig, "config"},
		{"Vagrantfile", CategoryConfig, "config"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SniffBytes(filepath.Join("dir", c.name), []byte("hello\n"))
			if got.Category != c.cat || got.Language != c.lang {
				t.Fatalf("SniffBytes(%q) = %s/%q, want %s/%q", c.name, got.Category, got.Language, c.cat, c.lang)
			}
			if got.IsBinary || got.IsMinified {
				t.Fatalf("SniffBytes(%q) flagged binary=%v minified=%v", c.name, got.IsBinary, got.IsMinified)
			}
		})
	}
}

// TestSniffBytes_ShebangInterpreters covers every interpreter family and the
// fall-through of an unknown interpreter to the extension map.
func TestSniffBytes_ShebangInterpreters(t *testing.T) {
	cases := []struct {
		shebang string
		lang    string
	}{
		{"#!/usr/bin/env python3", "python"},
		{"#!/bin/zsh", "bash"},
		{"#!/usr/bin/env deno", "javascript"},
		{"#!/usr/bin/ruby", "ruby"},
		{"#!/usr/bin/perl -w", "perl"},
		{"#!/usr/bin/php", "php"},
	}
	for _, c := range cases {
		got := SniffBytes("script", []byte(c.shebang+"\nbody\n"))
		if got.Category != CategoryCode || got.Language != c.lang {
			t.Errorf("%q: got %s/%q, want code/%q", c.shebang, got.Category, got.Language, c.lang)
		}
		if got.Shebang != c.shebang {
			t.Errorf("%q: Shebang = %q", c.shebang, got.Shebang)
		}
	}

	// A shebang without a trailing newline still counts.
	if got := SniffBytes("run", []byte("#!/bin/sh")); got.Language != "bash" {
		t.Errorf("shebang without newline: got %q, want bash", got.Language)
	}
	// An unknown interpreter falls back to the extension.
	if got := SniffBytes("x.sql", []byte("#!/usr/bin/awk -f\n")); got.Language != "sql" {
		t.Errorf("unknown interpreter: got %q, want the extension's sql", got.Language)
	}
}

func TestSniffFile_MissingFile(t *testing.T) {
	res, err := SniffFile(filepath.Join(t.TempDir(), "absent.go"))
	if err == nil {
		t.Fatal("SniffFile of a missing file returned no error")
	}
	if res.Category != CategoryUnknown {
		t.Fatalf("category = %s, want unknown", res.Category)
	}
}

func TestSniffFile_DirectoryReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory fails earlier on windows")
	}
	res, err := SniffFile(t.TempDir())
	if err == nil {
		t.Fatal("SniffFile of a directory returned no error")
	}
	if res.Category != CategoryUnknown {
		t.Fatalf("category = %s, want unknown", res.Category)
	}
}

func TestParseGitmodules(t *testing.T) {
	dir := t.TempDir()
	if got, err := ParseGitmodules(filepath.Join(dir, ".gitmodules")); err != nil || got != nil {
		t.Fatalf("missing file: got %v, %v; want nil, nil", got, err)
	}

	p := filepath.Join(dir, ".gitmodules")
	writeFile(t, p, strings.Join([]string{
		`[submodule "a"]`,
		`	path = libs/a`,
		`	url = https://example.invalid/a.git`,
		`# path = commented`,
		`; path = alsocommented`,
		`[submodule "b"]`,
		`	path = "third_party/b/"`,
		`	path = .`,
		`	pathological`,
	}, "\n"))
	got, err := ParseGitmodules(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"libs/a", "third_party/b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("submodules = %v, want %v", got, want)
	}

	// A directory in place of the file is a read error, not "absent".
	if runtime.GOOS != "windows" {
		bad := filepath.Join(dir, "dir")
		if err := os.Mkdir(bad, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseGitmodules(bad); err == nil {
			t.Fatal("ParseGitmodules of a directory returned no error")
		}
	}
}

func TestWalk_Symlinks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.go"), "package x\n")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sub", "inner.go"), "package y\n")
	links := map[string]string{
		"link.go":     filepath.Join(root, "real.go"),
		"dangling.go": filepath.Join(root, "missing.go"),
		"dirlink":     filepath.Join(root, "sub"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	collect := func(follow bool) []string {
		files, err := CollectFiles(root, Options{FollowSymlinks: follow}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var rel []string
		for _, f := range files {
			r, _ := filepath.Rel(root, f)
			rel = append(rel, filepath.ToSlash(r))
		}
		sort.Strings(rel)
		return rel
	}

	if got := strings.Join(collect(false), ","); got != "real.go,sub/inner.go" {
		t.Errorf("without FollowSymlinks: %s", got)
	}
	// Following keeps the file link and still skips dangling and directory links.
	if got := strings.Join(collect(true), ","); got != "link.go,real.go,sub/inner.go" {
		t.Errorf("with FollowSymlinks: %s", got)
	}
}

func TestWalk_ErrorsReachCallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	var seen error
	err := Walk(missing, Options{}, func(path string, d fs.DirEntry, err error) error {
		seen = err
		return err
	})
	if seen == nil || err == nil {
		t.Fatalf("walking a missing root: callback err %v, Walk err %v; want both set", seen, err)
	}
	if _, cerr := CollectFiles(missing, Options{}, nil); cerr == nil {
		t.Fatal("CollectFiles of a missing root returned no error")
	}

	// A callback error stops the walk and comes back from Walk.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "x")
	writeFile(t, filepath.Join(root, "b.go"), "x")
	stop := errors.New("stop")
	calls := 0
	err = Walk(root, Options{}, func(string, fs.DirEntry, error) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("Walk = %v after %d calls, want stop after 1", err, calls)
	}
}

func TestWalk_DefaultIgnoresAndExtraPatterns(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"keep.go",
		"node_modules/pkg/index.js",
		".hidden/secret.go",
		".env",
		"gen/out.pb.go",
		"docs/notes.md",
	} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, "x")
	}
	files, err := CollectFiles(root, Options{ExtraIgnores: []string{"gen/", "", "# comment"}},
		func(path string, _ fs.DirEntry) bool { return !strings.HasSuffix(path, ".md") })
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "keep.go" {
		t.Fatalf("files = %v, want only keep.go", files)
	}
}

func TestMatcher_ScopedIgnoreFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	// .gitignore in pkg ignores *.log; .gleannignore in the same dir re-includes keep.log.
	writeFile(t, filepath.Join(root, "pkg", ".gitignore"), "*.log\ntmp/\n")
	writeFile(t, filepath.Join(root, "pkg", ".gleannignore"), "!keep.log\n")
	m := NewMatcher(root, false, nil)

	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"pkg/a.log", false, true},
		{"pkg/deep/b.log", false, true},
		{"pkg/keep.log", false, false},
		{"top.log", false, false}, // outside the pkg scope
		{"pkg/tmp", true, true},
		{"pkg/tmp", false, false}, // dir-only pattern does not hit a file
		{".", true, false},
		{"node_modules", true, true},
	}
	for _, c := range cases {
		if got := m.ShouldIgnore(c.rel, c.isDir); got != c.want {
			t.Errorf("ShouldIgnore(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
	// Loading the same directory twice is a no-op (patterns are not duplicated).
	n := len(m.patterns)
	m.LoadDirIgnores(filepath.Join(root, "pkg"))
	if len(m.patterns) != n {
		t.Fatalf("reloading pkg ignores added %d patterns", len(m.patterns)-n)
	}
}
