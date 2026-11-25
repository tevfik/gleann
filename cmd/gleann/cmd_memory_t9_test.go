package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

// bOnlyContents returns the block contents of a `memory list --json` run.
func bOnlyContents(t *testing.T, blocks []memory.Block) []string {
	t.Helper()
	var out []string
	for _, b := range blocks {
		out = append(out, b.Content)
	}
	return out
}

func bHasContent(want []string, s string) bool {
	for _, c := range want {
		if c == s {
			return true
		}
	}
	return false
}

// T9: `memory list` is scope-aware. The default is what `memory context`
// injects — global plus the detected repository — and --scope all shows every
// scope, --scope <scope> shows one. --tier and --json keep working.
func TestCLIMemoryListScopeDefault(t *testing.T) {
	e := newCLIEnv(t)
	detected := memory.DetectRepoScope()
	if detected == "" || detected == "repo" {
		t.Fatalf("unusable detected scope %q", detected)
	}

	wantExit(t, e.run("memory", "remember", "zz here repo note", "--scope", "repo"), 0)
	wantExit(t, e.run("memory", "remember", "zz global note"), 0)
	wantExit(t, e.run("memory", "remember", "zz sibling repo note", "--scope", "github.com/org/sibling"), 0)

	// Default: global + this repo, never a sibling repository's memory.
	got := bOnlyContents(t, bMemList(t, e))
	if !bHasContent(got, "zz here repo note") || !bHasContent(got, "zz global note") {
		t.Errorf("default scope list = %v, want the repo and global notes", got)
	}
	if bHasContent(got, "zz sibling repo note") {
		t.Errorf("default scope list leaked a sibling scope: %v", got)
	}

	// --scope all lists every scope, including the sibling's block.
	all := bOnlyContents(t, bMemList(t, e, "--scope", "all"))
	for _, want := range []string{"zz here repo note", "zz global note", "zz sibling repo note"} {
		if !bHasContent(all, want) {
			t.Errorf("--scope all = %v, missing %q", all, want)
		}
	}

	// An explicit scope shows only that scope plus global.
	one := bOnlyContents(t, bMemList(t, e, "--scope", "github.com/org/sibling"))
	if !bHasContent(one, "zz sibling repo note") || bHasContent(one, "zz here repo note") {
		t.Errorf("--scope sibling = %v, want only the sibling and global notes", one)
	}

	// --tier still filters, and the text listing names the scope in use.
	long := bMemList(t, e, "--tier", "long", "--scope", "all")
	if len(long) != 3 {
		t.Errorf("--tier long --scope all = %d blocks, want 3", len(long))
	}
	r := e.run("memory", "list")
	wantExit(t, r, 0)
	mustContain(t, "list scope line", r.Out, "Scope "+detected, "[scope: global]")
	mustNotContain(t, "list scope filter", r.Out, "zz sibling repo note")
}

// T9: export --scope writes only that scope, and a re-export prunes the files
// of blocks forgotten since, so a later import cannot resurrect them.
func TestCLIMemoryExportScopeAndPrune(t *testing.T) {
	e := newCLIEnv(t)
	dir := filepath.Join(t.TempDir(), "backup")

	wantExit(t, e.run("memory", "remember", "zz alpha fact", "--scope", "github.com/org/alpha"), 0)
	wantExit(t, e.run("memory", "remember", "zz beta fact", "--scope", "github.com/org/beta"), 0)

	r := e.run("memory", "export", "--dir", dir, "--scope", "github.com/org/alpha")
	wantExit(t, r, 0)
	mustContain(t, "scoped export", r.Out, "Exported 1 memory block(s)", "scope: github.com/org/alpha")

	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(names) != 1 {
		t.Fatalf("scoped export files = %v (err %v), want one block file", names, err)
	}
	data, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "zz alpha fact") || strings.Contains(string(data), "zz beta fact") {
		t.Errorf("scoped export file leaked or missed a block:\n%s", data)
	}

	// Import into an empty store: only the scoped block arrives.
	wantExit(t, e.run("memory", "clear"), 0)
	wantExit(t, e.run("memory", "import", "--dir", dir), 0)
	got := bOnlyContents(t, bMemList(t, e, "--scope", "all"))
	if !bHasContent(got, "zz alpha fact") || bHasContent(got, "zz beta fact") {
		t.Errorf("after scoped import = %v, want only the alpha block", got)
	}

	// Forget the block, re-export the same directory: its file is pruned even
	// though a foreign file shares the folder. (The foreign file is not a
	// "*.md" block file: `memory import` has always rejected markdown it cannot
	// parse, and that stays so.)
	own := filepath.Join(dir, "my-notes.txt")
	if err := os.WriteFile(own, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantExit(t, e.run("memory", "forget", "zz alpha fact"), 0)
	r = e.run("memory", "export", "--dir", dir, "--scope", "github.com/org/alpha")
	wantExit(t, r, 0)
	mustContain(t, "re-export prune", r.Out, "Exported 0 memory block(s)", "pruned 1 stale file")
	if _, err := os.Stat(names[0]); !os.IsNotExist(err) {
		t.Errorf("stale export file survived: %v", err)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("the user's own file was removed: %v", err)
	}

	// An import of that directory into an empty store resurrects nothing.
	wantExit(t, e.run("memory", "clear"), 0)
	wantExit(t, e.run("memory", "import", "--dir", dir), 0)
	if left := bOnlyContents(t, bMemList(t, e, "--scope", "all")); len(left) != 0 {
		t.Errorf("forgotten block came back through export/import: %v", left)
	}
}

// T9: import --dry-run previews without writing, and a hand-edited export file
// is reported instead of being silently skipped.
func TestCLIMemoryImportDryRunAndHandEdit(t *testing.T) {
	e := newCLIEnv(t)
	dir := filepath.Join(t.TempDir(), "backup")

	wantExit(t, e.run("memory", "remember", "zz cli dry note"), 0)
	wantExit(t, e.run("memory", "export", "--dir", dir), 0)
	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(names) != 1 {
		t.Fatalf("export files = %v (err %v), want one", names, err)
	}

	// Preview into an empty store: the file is counted, nothing is written.
	wantExit(t, e.run("memory", "clear"), 0)
	r := e.run("memory", "import", "--dir", dir, "--dry-run")
	wantExit(t, r, 0)
	mustContain(t, "dry run", r.Out, "Dry run", "1 would import", "nothing written")
	if left := bMemList(t, e, "--scope", "all"); len(left) != 0 {
		t.Errorf("--dry-run wrote %d blocks, want 0", len(left))
	}

	// The real run imports it.
	r = e.run("memory", "import", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "real import", r.Out, "1 imported")
	if left := bMemList(t, e, "--scope", "all"); len(left) != 1 {
		t.Fatalf("after import = %d blocks, want 1", len(left))
	}

	// Hand-edit the exported file, leaving updated_at where it was: the import
	// keeps the stored text and says so, naming the file.
	edited := strings.Replace(readFile(t, names[0]), "zz cli dry note", "zz hand edited text", 1)
	if err := os.WriteFile(names[0], []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("memory", "import", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "hand edit report", r.combined(), "edited by hand", filepath.Base(names[0]))
	got := bOnlyContents(t, bMemList(t, e, "--scope", "all"))
	if !bHasContent(got, "zz cli dry note") || bHasContent(got, "zz hand edited text") {
		t.Errorf("stored blocks = %v, want the stored text kept", got)
	}

	// --dry-run reports the same hand-edit without writing.
	r = e.run("memory", "import", "--dir", dir, "--dry-run")
	wantExit(t, r, 0)
	mustContain(t, "dry run hand edit", r.combined(), "edited by hand", filepath.Base(names[0]))
	got = bOnlyContents(t, bMemList(t, e, "--scope", "all"))
	if bHasContent(got, "zz hand edited text") {
		t.Errorf("--dry-run applied the hand edit: %v", got)
	}
}

// The unknown-flag guard covers the new flags.
func TestCLIMemoryT9FlagAcceptance(t *testing.T) {
	e := newCLIEnv(t)
	dir := t.TempDir()
	wantExit(t, e.run("memory", "list", "--scope", "all"), 0)
	wantExit(t, e.run("memory", "export", "--dir", dir, "--scope", "all", "--include-short", "--include-expired"), 0)
	wantExit(t, e.run("memory", "import", "--dir", dir, "--dry-run"), 0)
	wantExit(t, e.run("memory", "list", "--bogus"), 2)
	wantExit(t, e.run("memory", "export", "--dir", dir, "--bogus"), 2)
	wantExit(t, e.run("memory", "import", "--dir", dir, "--bogus"), 2)
}
