package gleann

import (
	"regexp"
	"strings"
	"testing"
)

func TestNewShellCompressor(t *testing.T) {
	sc := NewShellCompressor()
	if sc == nil {
		t.Fatal("NewShellCompressor returned nil")
	}
	if len(sc.patterns) == 0 {
		t.Fatal("no default patterns")
	}
}

func TestParseCmdLine(t *testing.T) {
	tests := []struct {
		cmd  string
		tool string
		sub  string
	}{
		{"git status", "git", "status"},
		{"git log -n 5 --oneline", "git", "log"},
		{"/usr/bin/git diff", "git", "diff"},
		{"npm install --save-dev foo", "npm", "install"},
		{"go test ./...", "go", "test"},
		{"docker-compose up -d", "docker-compose", "up"},
		{"kubectl get pods -n default", "kubectl", "get"},
		{"cargo build --release", "cargo", "build"},
		{"", "", ""},
		{"ls", "ls", ""},
		{"git --no-pager log", "git", "log"},
	}
	for _, tt := range tests {
		tool, sub := parseCmdLine(tt.cmd)
		if tool != tt.tool || sub != tt.sub {
			t.Errorf("parseCmdLine(%q) = (%q, %q), want (%q, %q)", tt.cmd, tool, sub, tt.tool, tt.sub)
		}
	}
}

func TestCompressGitStatus(t *testing.T) {
	output := `On branch main
Your branch is up to date with 'origin/main'.

Changes not staged for commit:
  modified:   pkg/gleann/searcher.go
  modified:   pkg/gleann/types.go
  modified:   internal/mcp/server.go
  new file:   pkg/gleann/shellcompress.go
  deleted:    old_file.go

Untracked files:
  test_data/
`
	sc := NewShellCompressor()
	result := sc.Compress("git status", output)

	if result.PatternUsed != "git_status" {
		t.Errorf("expected pattern git_status, got %q", result.PatternUsed)
	}
	if result.Ratio <= 0 {
		t.Error("expected positive compression ratio")
	}
	if result.RawBytes <= 0 {
		t.Error("expected positive raw bytes")
	}
}

func TestCompressGoTest(t *testing.T) {
	output := `=== RUN   TestFoo
--- PASS: TestFoo (0.00s)
=== RUN   TestBar
--- PASS: TestBar (0.01s)
=== RUN   TestBaz
--- FAIL: TestBaz (0.02s)
    baz_test.go:15: expected 42, got 0
ok      github.com/example/pkg   0.123s
FAIL    github.com/example/other 0.456s
?       github.com/example/nope  [no test files]
`
	sc := NewShellCompressor()
	result := sc.Compress("go test ./...", output)

	if result.PatternUsed != "go_test" {
		t.Errorf("expected pattern go_test, got %q", result.PatternUsed)
	}
	// Should contain summary
	if !strings.Contains(result.Output, "Summary") {
		t.Error("expected Summary section in compressed go test output")
	}
	// Should contain failures
	if !strings.Contains(result.Output, "FAIL") {
		t.Error("expected FAIL in compressed go test output")
	}
}

func TestCompressNpmInstall(t *testing.T) {
	output := `npm warn deprecated @types/q@1.5.1: This is a stub types package
npm warn deprecated har-validator@5.1.5: this package is broken
npm http fetch GET 200 https://registry.npmjs.org/lodash 340ms
npm http fetch GET 200 https://registry.npmjs.org/chalk 234ms
added 542 packages, and audited 543 packages in 12s
24 packages are looking for funding
found 0 vulnerabilities
`
	sc := NewShellCompressor()
	result := sc.Compress("npm install", output)

	if result.PatternUsed != "npm_install" {
		t.Errorf("expected pattern npm_install, got %q", result.PatternUsed)
	}
	// Should strip npm warn and npm http lines
	if strings.Contains(result.Output, "npm http") {
		t.Error("should strip npm http lines")
	}
}

func TestCompressDockerBuild(t *testing.T) {
	output := `Sending build context to Docker daemon  2.048kB
#1 [internal] load build definition from Dockerfile
#2 sha256:abc123 pulling fs layer
#3 sha256:def456 pulling fs layer
#4 extracting sha256:abc123
#5 DONE 0.1s
Step 1/5 : FROM golang:1.21
Successfully built abc123
Successfully tagged myapp:latest
`
	sc := NewShellCompressor()
	result := sc.Compress("docker build -t myapp .", output)

	if result.PatternUsed != "docker_build" {
		t.Errorf("expected pattern docker_build, got %q", result.PatternUsed)
	}
}

func TestCompressGitDiff(t *testing.T) {
	lines := make([]string, 150)
	for i := range lines {
		lines[i] = "+added line " + string(rune('a'+i%26))
	}
	longOutput := strings.Join(lines, "\n")

	sc := NewShellCompressor()
	result := sc.Compress("git diff", longOutput)

	if result.PatternUsed != "git_diff" {
		t.Errorf("expected pattern git_diff, got %q", result.PatternUsed)
	}
	// Should be truncated (MaxLines=100)
	resultLines := strings.Split(result.Output, "\n")
	if len(resultLines) > 120 { // some overhead from tail+omitted message
		t.Errorf("expected truncation, got %d lines", len(resultLines))
	}
}

func TestGenericCompress(t *testing.T) {
	input := "line1\n\n\n\n\nline2\n\x1b[31mred\x1b[0m\n\n\n\nline3"
	result := genericCompress(input)

	if strings.Contains(result, "\x1b") {
		t.Error("should strip ANSI codes")
	}
	if strings.Contains(result, "\n\n\n") {
		t.Error("should collapse blank lines")
	}
}

func TestTruncateOutput(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	input := strings.Join(lines, "\n")

	result := truncateOutput(input, 5, 2)
	if !strings.Contains(result, "omitted") {
		t.Error("expected omitted message")
	}
	if !strings.Contains(result, "i") || !strings.Contains(result, "j") {
		t.Error("expected tail lines i and j")
	}
}

func TestCollapseLines(t *testing.T) {
	input := "header\nfoo 1\nfoo 2\nfoo 3\ntrailer"
	re := strings.NewReplacer() // dummy
	_ = re

	result := collapseLines(input, compileRegex(`^foo `), "%d items")
	if !strings.Contains(result, "3 items") {
		t.Errorf("expected '3 items' collapse, got:\n%s", result)
	}
}

func compileRegex(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

func TestCompressUnknownTool(t *testing.T) {
	sc := NewShellCompressor()
	result := sc.Compress("someweirdtool run", "output\n\n\n\nmore")

	if result.PatternUsed != "" {
		t.Errorf("expected no pattern for unknown tool, got %q", result.PatternUsed)
	}
	// Should still do generic compression
	if strings.Contains(result.Output, "\n\n\n") {
		t.Error("generic compress should collapse blanks")
	}
}

func TestAddPattern(t *testing.T) {
	sc := NewShellCompressor()
	before := len(sc.patterns)
	sc.AddPattern(ShellPattern{
		Name: "custom_tool", Tool: "mytool", SubCommand: "run",
		MaxLines: 5,
	})
	if len(sc.patterns) != before+1 {
		t.Error("AddPattern should add one pattern")
	}

	result := sc.Compress("mytool run", "line1\nline2\nline3\nline4\nline5\nline6\nline7")
	if result.PatternUsed != "custom_tool" {
		t.Errorf("expected custom_tool pattern, got %q", result.PatternUsed)
	}
}

func TestCompressCargoTest(t *testing.T) {
	output := `   Compiling mylib v0.1.0
   Compiling myapp v0.1.0
    Finished test [unoptimized + debuginfo] target(s) in 5.32s
     Running unittests src/main.rs

running 3 tests
test tests::test_add ... ok
test tests::test_sub ... FAILED
test tests::test_mul ... ok

test result: FAILED. 2 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out

`
	sc := NewShellCompressor()
	result := sc.Compress("cargo test", output)
	if result.PatternUsed != "cargo_test" {
		t.Errorf("expected cargo_test, got %q", result.PatternUsed)
	}
	if !strings.Contains(result.Output, "FAILED") {
		t.Error("should preserve FAILED lines")
	}
}

func TestCompressGitBlame(t *testing.T) {
	output := `abcdef12 (John 2024-01-01 10:00:00 +0000 1) func main() {
12345678 (Jane 2024-01-02 11:00:00 +0000 2)     fmt.Println("hello")
deadbeef (John 2024-01-03 12:00:00 +0000 3) }
`
	sc := NewShellCompressor()
	result := sc.Compress("git blame main.go", output)
	if result.PatternUsed != "git_blame" {
		t.Errorf("expected git_blame, got %q", result.PatternUsed)
	}
}

func TestCompressStatsFields(t *testing.T) {
	sc := NewShellCompressor()
	result := sc.Compress("ls -la", "file1\nfile2\nfile3")
	if result.RawTokens <= 0 {
		t.Error("expected positive raw tokens")
	}
	if result.CompTokens <= 0 {
		t.Error("expected positive compressed tokens")
	}
}

func TestCompressPipInstall(t *testing.T) {
	output := `Collecting requests
  Downloading requests-2.31.0.tar.gz (110 kB)
Using cached urllib3-2.1.0-py3-none-any.whl
Installing collected packages: urllib3, requests
Successfully installed requests-2.31.0 urllib3-2.1.0
`
	sc := NewShellCompressor()
	result := sc.Compress("pip install requests", output)
	if result.PatternUsed != "pip_install" {
		t.Errorf("expected pip_install, got %q", result.PatternUsed)
	}
}

func TestCompressKubectlApply(t *testing.T) {
	output := `deployment.apps/web configured
service/web unchanged
configmap/web-config created
secret/web-tls configured
`
	sc := NewShellCompressor()
	result := sc.Compress("kubectl apply -f .", output)
	if result.PatternUsed != "kubectl_apply" {
		t.Errorf("expected kubectl_apply, got %q", result.PatternUsed)
	}
	if !strings.Contains(result.Output, "resources applied") {
		t.Error("should collapse applied resources")
	}
}

func TestDefaultPatternCount(t *testing.T) {
	patterns := defaultShellPatterns()
	// We defined 95+ patterns covering git(16), go(6), npm/yarn/pnpm(8), cargo(3),
	// docker(7), python/pip(5), make(2), kubectl(4), terraform(3), misc(20+), ...
	if len(patterns) < 70 {
		t.Errorf("expected at least 70 patterns, got %d", len(patterns))
	}
}
