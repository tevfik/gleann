package mcp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/tevfik/gleann/pkg/memory"
)

// initScopeRepoForMCP creates a git repository with the given origin remote.
func initScopeRepoForMCP(t *testing.T, remote string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL="+os.DevNull,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if remote != "" {
		runGit("remote", "add", "origin", remote)
	}
	return dir
}

// TestMemoryToolsUseSharedRepoScope proves that the MCP surfaces keep using
// the same repository scope detection after it moved into pkg/memory: a
// memory_remember without a scope lands under the normalized git remote, and
// memory_context without a scope shows it while hiding a sibling repository.
func TestMemoryToolsUseSharedRepoScope(t *testing.T) {
	repo := initScopeRepoForMCP(t, "git@github.com:org/repo-a.git")
	t.Chdir(repo)

	const (
		markerA      = "mcp repo-a scope marker"
		markerB      = "mcp repo-b scope marker"
		markerGlobal = "mcp global scope marker"
	)

	s := testMCPServer()
	defer s.Close()

	res, err := s.handleMemoryRemember(context.Background(), makeCallToolReq(map[string]any{
		"content": markerA,
	}))
	if err != nil || res.IsError {
		t.Fatalf("memory_remember failed: %v %+v", err, res)
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	// The store is shared by the package's tests; an unlimited budget keeps the
	// marker blocks from being dropped by other tests' accumulated memories.
	oldBudget := mgr.MaxContextTokens
	mgr.MaxContextTokens = 0
	t.Cleanup(func() { mgr.MaxContextTokens = oldBudget })
	if _, err := mgr.AddScopedNote("github.com/org/repo-b", memory.TierLong, "rule", markerB); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddNote(memory.TierLong, "rule", markerGlobal); err != nil {
		t.Fatal(err)
	}

	// The stored block must sit exactly under the shared, normalized scope.
	blocks, err := mgr.ListScoped("github.com/org/repo-a", memory.TierLong)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range blocks {
		if strings.Contains(b.Content, markerA) {
			found = true
			if b.Scope != "github.com/org/repo-a" {
				t.Errorf("memory_remember stored scope %q, want shared scope %q", b.Scope, "github.com/org/repo-a")
			}
		}
	}
	if !found {
		t.Fatalf("memory_remember did not store the block under the current repository scope")
	}

	ctxRes, err := s.handleMemoryContext(context.Background(), makeCallToolReq(map[string]any{}))
	if err != nil || ctxRes.IsError {
		t.Fatalf("memory_context failed: %v %+v", err, ctxRes)
	}
	text := ctxRes.Content[0].(mcpsdk.TextContent).Text
	for _, want := range []string{markerA, markerGlobal} {
		if !strings.Contains(text, want) {
			t.Errorf("memory_context is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, markerB) {
		t.Errorf("memory_context leaked a sibling repository's block:\n%s", text)
	}
}
