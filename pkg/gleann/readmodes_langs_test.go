package gleann

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func linesOf(n int, line string) string {
	return strings.Repeat(line+"\n", n-1) + line
}

func TestAutoSelectModeTable(t *testing.T) {
	cases := []struct {
		file  string
		lines int
		want  ReadMode
	}{
		{"small.go", 50, ReadModeFull},
		{"mid.go", 200, ReadModeSignatures},
		{"huge.py", 600, ReadModeMap},
		{"cfg.yaml", 150, ReadModeFull},
		{"cfg.json", 250, ReadModeAggressive},
		{"notes.md", 250, ReadModeFull},
		{"notes.txt", 400, ReadModeMap},
		{"data.bin", 250, ReadModeFull},
		{"data.bin", 400, ReadModeMap},
	}
	for _, tc := range cases {
		if got := autoSelectMode(tc.file, linesOf(tc.lines, "x")); got != tc.want {
			t.Errorf("autoSelectMode(%s, %d lines) = %v, want %v", tc.file, tc.lines, got, tc.want)
		}
	}
}

func TestSignaturesAndReferencesPerLanguage(t *testing.T) {
	cases := []struct {
		file    string
		src     string
		sigs    []string // must appear in signature mode
		notSigs []string
		refs    []string // must appear in reference mode
	}{
		{"app.ts", "import { x } from './x'\nexport function run() {\n  return 1\n}\ninterface Opts {}\nconst y = 2\n",
			[]string{"export function run()", "interface Opts"}, []string{"return 1"}, []string{"import { x }", "interface Opts"}},
		{"Main.java", "package app;\nimport java.util.List;\npublic class Main {\n  int x = 1;\n}\n",
			[]string{"public class Main"}, []string{"int x = 1"}, []string{"package app;", "import java.util.List;", "public class Main"}},
		{"lib.c", "#include <stdio.h>\nstruct point { int x; };\nint add(int a, int b) {\n  return a + b;\n}\n",
			[]string{"struct point", "int add(int a, int b)"}, []string{"return a + b"}, []string{"#include <stdio.h>"}},
		{"tool.rb", "require 'json'\nmodule Tools\n  def run\n    puts 1\n  end\nend\n",
			[]string{"module Tools", "def run"}, []string{"puts 1"}, []string{"require 'json'"}},
		{"lib.rs", "use std::io;\npub struct S;\nfn f() {}\n",
			[]string{"pub struct S", "fn f()"}, nil, []string{"use std::io;", "pub struct S;"}},
		{"script.sh", "function deploy\n  echo hi\n",
			[]string{"function deploy"}, []string{"echo hi"}, nil},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		path := filepath.Join(dir, tc.file)
		if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
		sig, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeSignatures})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.sigs {
			if !strings.Contains(sig, want) {
				t.Errorf("%s signatures lack %q:\n%s", tc.file, want, sig)
			}
		}
		for _, not := range tc.notSigs {
			if strings.Contains(sig, not) {
				t.Errorf("%s signatures contain the body line %q", tc.file, not)
			}
		}
		ref, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeReference})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.refs {
			if !strings.Contains(ref, want) {
				t.Errorf("%s references lack %q:\n%s", tc.file, want, ref)
			}
		}
	}
}

func TestLineRangesClampAndSkip(t *testing.T) {
	content := "a\nb\nc\nd"
	got, err := extractLineRanges(content, "0:2, 3:99, 4:2")
	if err != nil {
		t.Fatal(err)
	}
	want := "L1    a\nL2    b\n---\nL3    c\nL4    d\n---" // the empty 4:2 range adds nothing
	if got != want {
		t.Fatalf("ranges = %q, want %q", got, want)
	}
	if got, _ := extractLineRanges(content, ""); got != content {
		t.Fatalf("empty ranges = %q", got)
	}
}

func TestTaskAndEntropyEdgeCases(t *testing.T) {
	// A query of stop words only has no keywords: the content is returned.
	if got := extractTaskRelevant("alpha\nbeta", "the a of"); got != "alpha\nbeta" {
		t.Fatalf("stop-word query = %q", got)
	}
	if got := extractTaskRelevant("alpha\nbeta", "gamma delta"); !strings.HasPrefix(got, "# No lines matched") {
		t.Fatalf("unmatched query = %q", got)
	}
	// More than 100 high-entropy lines are capped at 100.
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("if err != nil && x || y { return fmt.Errorf(\"failed: %w\", wrap(err)) }\n")
	}
	out := extractHighEntropy(b.String())
	if n := strings.Count(out, "\nL"); n != 100 {
		t.Fatalf("%d entropy lines, want the cap of 100", n)
	}
	long := strings.Repeat("value", 40)
	if entropyScore("x := "+long) <= entropyScore("x := y") {
		t.Fatal("a long line must score higher than a short one")
	}
}

func TestReadModeDiffOutsideGitAndUnchanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeDiff})
	if err != nil || !strings.Contains(out, "requires git context") {
		t.Fatalf("outside git = %q, %v", out, err)
	}

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.name=t", "-c", "user.email=t@example.invalid", "add", "plain.txt")
	run("-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "init")
	out, err = ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeDiff})
	if err != nil || !strings.Contains(out, "No changes detected") {
		t.Fatalf("unchanged file = %q, %v", out, err)
	}
}
