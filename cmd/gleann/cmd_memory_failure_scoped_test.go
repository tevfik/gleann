package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

func setupFailureMem(t *testing.T) {
	t.Helper()
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(t.TempDir(), "mem"))
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
}

// runMem runs a memory subcommand with stdout/stderr captured and exitFunc
// stubbed, so a usage error surfaces as output rather than killing the test.
func runMem(t *testing.T, args ...string) string {
	t.Helper()
	return captureHelp(t, func() { cmdMemory(args) })
}

// --scope isolates failures across repos; --scope all lists every scope.
func TestCmdMemoryFailureScopedAddList(t *testing.T) {
	setupFailureMem(t)

	runMem(t, "failure", "add", "--command", "go test ./pkg/a", "--error", "boom a", "--fix", "fix a", "--scope", "repo-a")
	runMem(t, "failure", "add", "--command", "go test ./pkg/b", "--error", "boom b", "--fix", "fix b", "--scope", "repo-b")

	out := runMem(t, "failure", "list", "--scope", "repo-a")
	if !strings.Contains(out, "boom a") || strings.Contains(out, "boom b") {
		t.Fatalf("repo-a list = %q", out)
	}
	out = runMem(t, "failure", "list", "--scope", "repo-b")
	if !strings.Contains(out, "boom b") || strings.Contains(out, "boom a") {
		t.Fatalf("repo-b list = %q", out)
	}
	for _, all := range []string{"all", "*"} {
		out = runMem(t, "failure", "list", "--scope", all)
		if !strings.Contains(out, "Recorded failures (2)") {
			t.Fatalf("--scope %s list = %q, want 2", all, out)
		}
	}
}

// Without --scope the CLI defaults to the detected repository scope.
func TestCmdMemoryFailureDefaultScope(t *testing.T) {
	setupFailureMem(t)
	runMem(t, "failure", "add", "--command", "go test", "--error", "boom", "--fix", "retry")

	mgr, err := memory.DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	fs, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	want := memory.DetectRepoScope()
	if len(fs) != 1 || fs[0].Scope != want {
		t.Fatalf("default scope = %v, want %q", fs, want)
	}

	// Default list sees it; a foreign scope does not.
	out := runMem(t, "failure", "list")
	if !strings.Contains(out, "boom") {
		t.Fatalf("default list should show the failure, got %q", out)
	}
	out = runMem(t, "failure", "list", "--scope", "foreign-repo")
	if !strings.Contains(out, "No failures recorded") {
		t.Fatalf("foreign scope should be empty, got %q", out)
	}
}

// memory context --symbol surfaces a failure with paths empty, and --scope
// isolates it. --command keeps working.
func TestCmdMemoryContextSymbolScoped(t *testing.T) {
	setupFailureMem(t)
	runMem(t, "failure", "add",
		"--command", "go build ./pkg/a",
		"--error", "undefined: Thing",
		"--fix", "import thing",
		"--symbol", "github.com/org/repo-a/pkg.Thing",
		"--scope", "repo-a")

	// symbols only, paths empty, matching scope.
	out := runMem(t, "context", "--symbol", "Thing", "--scope", "repo-a")
	if !strings.Contains(out, "<failure_memory>") || !strings.Contains(out, "import thing") {
		t.Fatalf("symbol context missing failure:\n%s", out)
	}
	// Other scope does not see it.
	out = runMem(t, "context", "--symbol", "Thing", "--scope", "repo-b")
	if strings.Contains(out, "import thing") {
		t.Fatalf("repo-b context leaked repo-a failure:\n%s", out)
	}
	// --command still surfaces it in scope.
	out = runMem(t, "context", "--command", "go build", "--scope", "repo-a")
	if !strings.Contains(out, "import thing") {
		t.Fatalf("command context missing failure:\n%s", out)
	}
	// No task → no failure section.
	out = runMem(t, "context", "--scope", "repo-a")
	if strings.Contains(out, "<failure_memory>") {
		t.Fatalf("plain context should have no failure section:\n%s", out)
	}
}

// The new flags are registered, so rejectUnknownFlags accepts them.
func TestCmdMemoryFailureFlagsRegistered(t *testing.T) {
	setupFailureMem(t)
	out := runMem(t, "failure", "add", "--command", "go test", "--error", "boom", "--fix", "x", "--scope", "repo-a", "--symbol", "Foo")
	if !strings.Contains(out, "Recorded failure") {
		t.Fatalf("failure add with --scope/--symbol should succeed, got %q", out)
	}
	out = runMem(t, "context", "--symbol", "Foo", "--scope", "repo-a")
	if strings.Contains(out, "unknown flag") {
		t.Fatalf("context --symbol/--scope rejected: %q", out)
	}
}

// ── v2 repair, finding 4: failure add --scope all records a global block ─────

func TestV2CLIAddScopeAllGlobal(t *testing.T) {
	setupFailureMem(t)
	for i, scope := range []string{"all", "*", "ALL"} {
		runMem(t, "failure", "add", "--command", "go build",
			"--error", fmt.Sprintf("zz cli global probe %d", i), "--fix", "retry", "--scope", scope)
	}
	mgr, err := memory.DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	mgr.Close()
	if len(fs) != 3 {
		t.Fatalf("ListFailures = %d blocks, want 3", len(fs))
	}
	for _, b := range fs {
		if b.Scope != "" {
			t.Fatalf("--scope %q stored scope %q, want global \"\"", b.Metadata[memory.FailureMetaCommand], b.Scope)
		}
	}
	// The default (repo-scoped) list sees global failures; so does --scope all.
	out := runMem(t, "failure", "list")
	if !strings.Contains(out, "Recorded failures (3)") {
		t.Fatalf("default list should see the 3 global failures, got %q", out)
	}
	out = runMem(t, "failure", "list", "--scope", "all")
	if !strings.Contains(out, "Recorded failures (3)") {
		t.Fatalf("--scope all list = %q, want 3", out)
	}
}

// The usage text must state the sentinel semantics: --scope all = global on
// add, all scopes on list/context.
func TestV2CLIUsageScopeAllSemantics(t *testing.T) {
	setupFailureMem(t)
	out := runMem(t) // no subcommand → usage
	if !strings.Contains(out, "--scope all = global") {
		t.Fatalf("usage should document '--scope all = global' on add, got:\n%s", out)
	}
}

// ── v2 repair, finding 2: CLI context/list with slash-spelled scopes ─────────

func TestV2CLIContextScopeSlash(t *testing.T) {
	setupFailureMem(t)
	runMem(t, "failure", "add",
		"--command", "go test ./pkg/x",
		"--error", "zz slash probe",
		"--fix", "retry",
		"--path", "pkg/main.go",
		"--scope", "repo-a")

	for _, scope := range []string{"repo-a", "repo-a/", "/repo-a"} {
		out := runMem(t, "context", "--scope", scope, "--path", "main.go")
		if !strings.Contains(out, "<failure_memory>") || !strings.Contains(out, "retry") {
			t.Fatalf("context --scope %q --path main.go lost the basename match:\n%s", scope, out)
		}
		out = runMem(t, "failure", "list", "--scope", scope)
		if !strings.Contains(out, "zz slash probe") {
			t.Fatalf("failure list --scope %q = %q, want the repo-a failure", scope, out)
		}
	}
}

// ── v3 repair, finding 1: remember --scope stores a normalized block scope ───

func TestV3CLIRememberScope(t *testing.T) {
	setupFailureMem(t)

	out := runMem(t, "remember", "zz v3 cli padded fact", "--scope", "repo-a/")
	if strings.Contains(out, "unknown flag") {
		t.Fatalf("remember rejected --scope:\n%s", out)
	}
	if !strings.Contains(out, "Remembered") {
		t.Fatalf("remember --scope failed:\n%s", out)
	}

	mgr, err := memory.DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.ListScoped("repo-a", memory.TierLong)
	if err != nil || len(got) != 1 || got[0].Scope != "repo-a" || got[0].Content != "zz v3 cli padded fact" {
		mgr.Close()
		t.Fatalf("ListScoped(repo-a) = %v (err %v), want the normalized remember block", got, err)
	}
	mgr.Close()

	// --scope all stores a global block, never a literal "all" scope.
	out = runMem(t, "remember", "zz v3 cli global fact", "--scope", "all")
	if !strings.Contains(out, "Remembered") {
		t.Fatalf("remember --scope all failed:\n%s", out)
	}
	mgr, err = memory.DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	got, err = mgr.ListScoped("repo-b", memory.TierLong)
	if err != nil || len(got) != 1 || got[0].Scope != "" || got[0].Content != "zz v3 cli global fact" {
		t.Fatalf("repo-b view = %v (err %v), want only the global remember block", got, err)
	}

	// Without --scope, remember stays global (existing behaviour).
	out = runMem(t, "remember", "zz v3 cli unscoped fact")
	if !strings.Contains(out, "Remembered") {
		t.Fatalf("plain remember failed:\n%s", out)
	}
	got, err = mgr.ListScoped("repo-a", memory.TierLong)
	if err != nil || len(got) != 3 { // repo-a fact + 2 globals
		t.Fatalf("repo-a view after plain remember = %v (err %v), want 3 blocks", got, err)
	}

	// Usage documents the flag.
	out = runMem(t)
	if !strings.Contains(out, "remember <text> [--tag <tags>] [--label <label>] [--scope <scope>]") {
		t.Fatalf("usage does not document remember --scope:\n%s", out)
	}
}
