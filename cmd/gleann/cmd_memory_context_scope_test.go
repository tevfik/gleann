package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `gleann memory context` without --scope shows the detected repository's
// memory plus global blocks, like MCP memory_context; the SessionStart hook of
// `gleann install claude` runs it in every project. Since v1.22 a plain
// directory detects as "dir:<name>-<path-hash>" (finding H7), so the scoped
// write uses the "repo" alias of the detected scope instead of a bare name.
func TestMemoryContextDefaultsToRepoScope(t *testing.T) {
	e := newCLIEnv(t)
	root := t.TempDir()
	projX := filepath.Join(root, "projX")
	other := filepath.Join(root, "unrelated")
	for _, d := range []string{projX, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(projX)
	wantExit(t, e.run("memory", "remember", "SECRET-of-projX", "--scope", "repo"), 0)
	wantExit(t, e.run("memory", "remember", "GLOBAL-fact"), 0)

	t.Chdir(other)
	r := e.run("memory", "context")
	wantExit(t, r, 0)
	mustNotContain(t, "unrelated dir", r.Out, "SECRET-of-projX")
	mustContain(t, "unrelated dir keeps global", r.Out, "GLOBAL-fact")

	t.Chdir(projX)
	r = e.run("memory", "context")
	wantExit(t, r, 0)
	mustContain(t, "inside projX", r.Out, "SECRET-of-projX", "GLOBAL-fact")

	t.Chdir(other)
	r = e.run("memory", "context", "--scope", "all")
	wantExit(t, r, 0)
	mustContain(t, "--scope all", r.Out, "SECRET-of-projX", "GLOBAL-fact")
}
