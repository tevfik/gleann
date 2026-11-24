package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// CLI tests of --quiet on the build/rebuild surfaces. `index sync --quiet`
// already discards the pipeline's stdout and defaults the progress surface to
// none (an explicit --progress wins); `index build`, `index rebuild`, the
// dual-model build path and `graph build` must behave the same: no progress
// lines, no informational stdout, errors still on stderr, exit codes unchanged.

// progressLines are the pacer's plain-surface markers on stderr.
func progressLines() []string {
	return []string{"gleann-bar", "gleann-progress", "gleann-done"}
}

func TestCLIBuildQuiet(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	cases := []struct {
		name       string
		flags      []string
		wantStdout bool   // informational stdout expected
		surface    string // expected progress surface: "", "plain" or "json"
	}{
		{"default", nil, true, "plain"},
		{"explicit-plain", []string{"--progress", "plain"}, true, "plain"},
		{"explicit-none", []string{"--progress", "none"}, true, ""},
		{"quiet", []string{"--quiet"}, false, ""},
		{"quiet-none", []string{"--quiet", "--progress", "none"}, false, ""},
		// An explicit --progress wins over --quiet, as on index sync.
		{"quiet-plain", []string{"--quiet", "--progress", "plain"}, false, "plain"},
		{"quiet-json", []string{"--quiet", "--progress", "json"}, false, "json"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("idx%d", i)
			args := append([]string{"index", "build", name, "--docs", e.Docs, "--no-report", "--no-agents"}, tc.flags...)
			r := e.run(args...)
			wantExit(t, r, 0)
			if tc.wantStdout && r.Out == "" {
				t.Error("expected informational stdout without --quiet, got none")
			}
			if !tc.wantStdout && r.Out != "" {
				t.Errorf("--quiet build printed stdout:\n%s", r.Out)
			}
			switch tc.surface {
			case "":
				mustNotContain(t, "stderr", r.Err, progressLines()...)
			case "plain":
				mustContain(t, "stderr", r.Err, "gleann-progress", "gleann-done")
			case "json":
				mustContain(t, "stderr", r.Err, `"phase":`, `"done":true`)
				mustNotContain(t, "stderr", r.Err, progressLines()...)
			}
			// The build itself must still happen.
			meta, err := gleann.GetIndexMeta(e.IndexDir, name)
			if err != nil || meta.NumPassages == 0 {
				t.Errorf("build produced no index: %+v %v", meta, err)
			}
		})
	}
}

func TestCLIBuildQuietDualModel(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	// Control: the dual-model banner goes to stdout.
	r := e.run("index", "build", "loud", "--docs", e.Docs, "--code-model", "nomic-embed-text", "--doc-model", "bge-m3", "--no-report", "--no-agents", "--yes")
	wantExit(t, r, 0)
	mustContain(t, "dual banner", r.Out, "Dual-model mode active", "Dual-index build complete")

	r = e.run("index", "build", "quiet", "--docs", e.Docs, "--code-model", "nomic-embed-text", "--doc-model", "bge-m3", "--no-report", "--no-agents", "--yes", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet dual build printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet dual stderr", r.Err, progressLines()...)
	for _, name := range []string{"quiet-code", "quiet-docs"} {
		meta, err := gleann.GetIndexMeta(e.IndexDir, name)
		if err != nil || meta.NumPassages == 0 {
			t.Errorf("dual pass %s produced no index: %+v %v", name, meta, err)
		}
	}
}

func TestCLIRebuildQuiet(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	// Rebuilding a missing index: the remove/rebuild notices are silenced.
	r := e.run("index", "rebuild", "fresh", "--docs", e.Docs, "--no-report", "--no-agents", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet rebuild of a missing index printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet rebuild stderr", r.Err, progressLines()...)

	// Rebuilding an existing index stays silent too.
	e.write("docs/guide.md", "# Guide\n\nA changed sentence about pineapples.\n")
	r = e.run("index", "rebuild", "fresh", "--docs", e.Docs, "--no-report", "--no-agents", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet rebuild printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet rebuild stderr", r.Err, progressLines()...)
	meta, err := gleann.GetIndexMeta(e.IndexDir, "fresh")
	if err != nil || meta.NumPassages == 0 {
		t.Errorf("rebuild produced no index: %+v %v", meta, err)
	}
}

func TestCLIGraphBuildQuiet(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("g", "--graph")

	// Control: a graph build reports its status on stdout.
	r := e.run("graph", "build", "g")
	wantExit(t, r, 0)
	mustContain(t, "graph build stdout", r.Out, "Graph Index")

	r = e.run("graph", "build", "g", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet graph build printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet graph build stderr", r.Err, progressLines()...)
}

func TestCLIBuildQuietErrorsStillReported(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	// An empty source directory: the error stays on stderr, exit code 1.
	empty := filepath.Join(e.Home, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	r := e.run("index", "build", "demo", "--docs", empty, "--no-report", "--no-agents", "--quiet")
	wantExit(t, r, 1)
	mustContain(t, "no documents", r.Err, "error: no documents found")
	if r.Out != "" {
		t.Errorf("--quiet failed build printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "failed build stderr", r.Err, progressLines()...)

	// A failing embedding backend is reported with its cause.
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	r = e.run("index", "build", "demo", "--docs", e.Docs, "--no-report", "--no-agents", "--quiet")
	wantExit(t, r, 1)
	mustContain(t, "embedding failure", r.Err, "error:", "500")
	if r.Out != "" {
		t.Errorf("--quiet failed build printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "failed build stderr", r.Err, progressLines()...)
}

func TestCLIBuildQuietKeepsDataOutput(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	// --dry-run prints the estimate: that is the command's data output, not
	// status, so --quiet keeps it (like search --quiet keeps the results).
	r := e.run("index", "build", "demo", "--docs", e.Docs, "--dry-run", "--quiet")
	wantExit(t, r, 0)
	mustContain(t, "dry-run estimate", r.Out, "Pre-flight Job Estimate")

	// The decision gate's JSON payload (exit 4) is data output as well.
	r = e.run("index", "build", "demo", "--docs", e.Docs, "--max-minutes", "0.0000001", "--quiet")
	wantExit(t, r, 4)
	mustContain(t, "gate payload", r.Out, "estimated_time_exceeds_threshold")

	// A cancelled build exits 0 without the informational notice.
	r = e.run("index", "build", "demo", "--docs", e.Docs, "--approve", "cancel", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet cancelled build printed stdout:\n%s", r.Out)
	}
	if _, err := gleann.GetIndexMeta(e.IndexDir, "demo"); err == nil {
		t.Error("a cancelled build must not create the index")
	}
}

// TestCLIBuildQuietAgents pins the --agents status lines of
// appendOrCreateFile under --quiet: the "(already configured …)" notice is
// informational stdout and must be silenced, while the file itself is left
// untouched. Without --quiet the notice keeps its old stdout behaviour
// (shared with `gleann install`, which must not change).
func TestCLIBuildQuietAgents(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	agents := e.write("AGENTS.md", "# Project notes\n\n## gleann: Code Intelligence\n\nExisting section.\n")
	before, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}

	// Control: without --quiet the sentinel notice stays on stdout.
	r := e.run("index", "build", "loud", "--docs", e.Docs, "--no-report", "--agents")
	wantExit(t, r, 0)
	mustContain(t, "user-edited section (loud)", r.Out, "does not look generated")

	// Under --quiet the same run must leave stdout empty.
	r = e.run("index", "build", "quiet", "--docs", e.Docs, "--no-report", "--agents", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet --agents build printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet agents stderr", r.Err, progressLines()...)
	// The section is the user's own text (no gleann markers, no generated
	// text): the warning that it was left alone survives on stderr.
	mustContain(t, "quiet agents stderr warning", r.Err, "does not look generated")

	// The existing file must not be modified.
	after, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("--quiet --agents build modified an already-configured AGENTS.md")
	}
}

// TestCLIBuildQuietAgentsCreate covers the write path: a fresh AGENTS.md is
// still created under --quiet, only the status lines are silenced.
func TestCLIBuildQuietAgentsCreate(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	r := e.run("index", "build", "ag", "--docs", e.Docs, "--no-report", "--agents", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet --agents build printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet agents create stderr", r.Err, progressLines()...)
	data, err := os.ReadFile(filepath.Join(e.Docs, "AGENTS.md"))
	if err != nil {
		t.Fatalf("--quiet --agents build did not create AGENTS.md: %v", err)
	}
	mustContain(t, "created AGENTS.md", string(data), "gleann: Code Intelligence")
}

// TestCLIBuildQuietAgentsSymlink pins the symlink-skip line: it is a warning,
// so under --quiet it moves to stderr instead of being dropped. Without
// --quiet it keeps its old stdout behaviour.
func TestCLIBuildQuietAgentsSymlink(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	target := filepath.Join(e.Home, "agents-target.md")
	if err := os.WriteFile(target, []byte("# Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(e.Docs, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	followSymlinkFlag = false
	defer func() { followSymlinkFlag = false }()

	// Control: without --quiet the skip warning stays on stdout.
	r := e.run("index", "build", "loud", "--docs", e.Docs, "--no-report", "--agents")
	wantExit(t, r, 0)
	mustContain(t, "symlink skip (loud)", r.Out, "Skipped", "--follow-symlink")

	// Under --quiet the warning moves to stderr; stdout stays empty.
	r = e.run("index", "build", "quiet", "--docs", e.Docs, "--no-report", "--agents", "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet --agents symlink build printed stdout:\n%s", r.Out)
	}
	mustContain(t, "symlink skip (quiet)", r.Err, "Skipped", "--follow-symlink")
	mustNotContain(t, "quiet symlink stderr", r.Err, progressLines()...)

	// The symlink target must not have been modified.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Target\n" {
		t.Error("--quiet --agents build modified a symlinked AGENTS.md target")
	}
}

// TestCLISyncQuietParity is a guard: sync --quiet already behaves this way
// before the build fix; it pins the behaviour the build surfaces must match.
func TestCLISyncQuietParity(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("snc")

	e.write("docs/guide.md", "# Guide\n\nA changed sentence about pineapples.\n")
	r := e.run("index", "sync", "snc", "--docs", e.Docs, "--quiet")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet sync printed stdout:\n%s", r.Out)
	}
	mustNotContain(t, "quiet sync stderr", r.Err, progressLines()...)

	// An explicit --progress wins over --quiet on sync too.
	e.write("docs/guide.md", "# Guide\n\nAnother changed sentence about mangoes.\n")
	r = e.run("index", "sync", "snc", "--docs", e.Docs, "--quiet", "--progress", "plain")
	wantExit(t, r, 0)
	if r.Out != "" {
		t.Errorf("--quiet sync with --progress printed stdout:\n%s", r.Out)
	}
}
