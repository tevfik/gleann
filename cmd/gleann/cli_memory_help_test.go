package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Item 1 (wave v122): every `gleann memory <subcommand>` honours --help/-h as a
// request for usage: exit 0, usage on stdout, and the subcommand never runs —
// it must not store, delete, clear, export or record anything. A literal "--"
// still ends flag parsing, so `memory forget -- --help` searches for the text
// "--help" instead of showing help.
func TestCLIMemorySubcommandHelp(t *testing.T) {
	e := newCLIEnv(t)
	exportDir := filepath.Join(e.Home, "export")

	for _, text := range []string{"hexagonal architecture", "one writer per index", "the flag --help prints usage"} {
		if r := e.run("memory", "remember", text); r.Code != 0 {
			t.Fatalf("seed %q: exit %d\n%s\n%s", text, r.Code, r.Out, r.Err)
		}
	}
	blocks := bMemList(t, e)
	if len(blocks) != 3 {
		t.Fatalf("seeded %d blocks, want 3", len(blocks))
	}
	victim := blocks[0].ID

	for _, args := range [][]string{
		{"memory", "remember", "must not be stored", "--help"},
		{"memory", "remember", "-h"},
		{"memory", "forget", "--help"},
		{"memory", "forget", "-h"},
		{"memory", "forget", victim, "--help"},
		{"memory", "forget", "--help", victim},
		{"memory", "list", "--help"},
		{"memory", "list", "-h"},
		{"memory", "search", "hexagonal", "--help"},
		{"memory", "search", "-h"},
		{"memory", "clear", "--help"},
		{"memory", "clear", "--tier", "long", "-h"},
		{"memory", "add", "long", "--help"},
		{"memory", "add", "long", "must not be stored", "-h"},
		{"memory", "stats", "--help"},
		{"memory", "stats", "-h"},
		{"memory", "summarize", "--last", "--help"},
		{"memory", "prune", "--age", "1d", "-h"},
		{"memory", "compact", "--min-validity", "0.9", "--help"},
		{"memory", "context", "--help"},
		{"memory", "maintain", "--help"},
		{"memory", "maintain", "-h"},
		{"memory", "reflect", "--dry-run", "--help"},
		{"memory", "export", "--dir", exportDir, "-h"},
		{"memory", "import", "--dir", exportDir, "--help"},
		{"memory", "import-graph", "missing.json", "-h"},
		{"memory", "failure", "--help"},
		{"memory", "failure", "add", "--command", "go test -race", "--error", "boom", "--fix", "retry", "--help"},
		{"memory", "failure", "list", "-h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := e.run(args...)
			if r.Code != 0 {
				t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", r.Code, r.Out, r.Err)
			}
			comb := r.combined()
			if !strings.Contains(comb, "Usage:") {
				t.Errorf("no usage text on --help:\nstdout: %s\nstderr: %s", r.Out, r.Err)
			}
			if r.Err != "" {
				t.Errorf("usage went to stderr instead of stdout: %s", r.Err)
			}
			for _, marker := range []string{"Forgot", "✅", "Deleted", "Removed", "Recorded", "Promoted", "error:"} {
				if strings.Contains(comb, marker) {
					t.Errorf("subcommand ran on --help (found %q):\n%s", marker, comb)
				}
			}
		})
	}

	// Nothing was written or destroyed: still the same 3 blocks, no exported
	// files, no recorded failure.
	if got := bMemList(t, e); len(got) != 3 {
		t.Errorf("after the --help sweep want 3 blocks, got %d: %v", len(got), got)
	}
	if entries, err := os.ReadDir(exportDir); err == nil {
		t.Errorf("memory export --help wrote %d file(s) into %s", len(entries), exportDir)
	}
	if r := e.run("memory", "failure", "list"); strings.Contains(r.combined(), "go test -race") {
		t.Errorf("memory failure add --help recorded a failure:\n%s", r.combined())
	}

	// "--" still ends flag parsing: the text after it is the query, so the
	// block seeded with "--help" in its content is the one that gets deleted.
	r := e.run("memory", "forget", "--", "--help")
	wantExit(t, r, 0)
	mustContain(t, "forget -- --help", r.Out, `Forgot 1 memory block(s) matching "--help"`)
	if got := bMemList(t, e); len(got) != 2 {
		t.Errorf("after forget -- --help want 2 blocks, got %d", len(got))
	}
}

// Item 2 (wave v122) at the CLI level: list and search show 8-character short
// IDs, so `memory forget <short-prefix>` must address the block.
func TestCLIMemoryForgetIDPrefix(t *testing.T) {
	e := newCLIEnv(t)
	for _, text := range []string{"hexagonal architecture", "one writer per index"} {
		if r := e.run("memory", "remember", text); r.Code != 0 {
			t.Fatalf("seed %q: exit %d\n%s\n%s", text, r.Code, r.Out, r.Err)
		}
	}
	blocks := bMemList(t, e)
	if len(blocks) != 2 {
		t.Fatalf("seeded %d blocks, want 2", len(blocks))
	}
	prefix := blocks[0].ID[:8]

	r := e.run("memory", "forget", prefix)
	wantExit(t, r, 0)
	mustContain(t, "forget by short ID", r.Out, "Forgot 1 memory block(s)")

	left := bMemList(t, e)
	if len(left) != 1 {
		t.Fatalf("after prefix forget want 1 block, got %d", len(left))
	}
	if left[0].ID == blocks[0].ID {
		t.Errorf("forget %q deleted the wrong block", prefix)
	}
}
