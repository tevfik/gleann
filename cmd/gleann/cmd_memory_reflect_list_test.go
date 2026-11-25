package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

// isolateMemoryDirs points every home, XDG and GLEANN lookup at t.TempDir() so
// `memory reflect --list` can never read or write the real ~/.gleann.
func isolateMemoryDirs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	memDir := filepath.Join(root, "memory")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg-data"))
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("GLEANN_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("GLEANN_MEMORY_DIR", memDir)
	t.Setenv("GLEANN_INDEX_DIR", filepath.Join(root, "indices"))
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	return memDir
}

// seedReflectionRuns writes change sets straight to the store: listing them
// must not need an LLM or a conversation store.
func seedReflectionRuns(t *testing.T, memDir string, runs ...*memory.ChangeSet) {
	t.Helper()
	s, err := memory.OpenStore(filepath.Join(memDir, "memory.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	for _, cs := range runs {
		if err := s.SaveChangeSet(cs); err != nil {
			t.Fatalf("SaveChangeSet %s: %v", cs.RunID, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCmdMemoryReflectListEmptyAndPopulated(t *testing.T) {
	memDir := isolateMemoryDirs(t)

	// 1. Nothing recorded yet.
	out := captureHelp(t, func() { cmdMemory([]string{"reflect", "--list"}) })
	if !strings.Contains(out, "No reflection runs recorded.") {
		t.Errorf("empty --list output = %q, want the empty-state line", out)
	}
	if strings.Contains(out, "run-") {
		t.Errorf("empty --list output mentions a run: %q", out)
	}

	// 2. Three runs, saved oldest-last so the ordering has to come from the store.
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	seedReflectionRuns(t, memDir,
		&memory.ChangeSet{RunID: "run-old", Timestamp: base, BlocksAdded: []string{"a1"}},
		&memory.ChangeSet{RunID: "run-new", Timestamp: base.Add(2 * time.Hour),
			BlocksAdded:      []string{"a2", "a3"},
			BlocksSuperseded: []string{"b1"},
			BlocksConfirmed:  []memory.BlockConfirmation{{ID: "c1", PriorConfirms: 1}}},
		&memory.ChangeSet{RunID: "run-mid", Timestamp: base.Add(time.Hour)},
	)

	out = captureHelp(t, func() { cmdMemory([]string{"reflect", "--list"}) })
	for _, want := range []string{"run-new", "run-mid", "run-old", "added 2, superseded 1, confirmed 1", "no changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("--list output missing %q:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "run-new"), strings.Index(out, "run-old"); i < 0 || j < 0 || i > j {
		t.Errorf("--list is not newest first:\n%s", out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("--list does not report the run count:\n%s", out)
	}

	// 3. --json lists the same runs in the same order.
	out = captureHelp(t, func() { cmdMemory([]string{"reflect", "--list", "--json"}) })
	var runs []*memory.ChangeSet
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &runs); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, out)
	}
	if len(runs) != 3 {
		t.Fatalf("--json listed %d runs, want 3", len(runs))
	}
	for i, want := range []string{"run-new", "run-mid", "run-old"} {
		if runs[i].RunID != want {
			t.Errorf("--json run %d = %q, want %q", i, runs[i].RunID, want)
		}
	}

	// 4. --limit caps the list and keeps the newest runs.
	out = captureHelp(t, func() { cmdMemory([]string{"reflect", "--list", "--limit", "2"}) })
	if !strings.Contains(out, "run-new") || !strings.Contains(out, "run-mid") {
		t.Errorf("--limit 2 lost the newest runs:\n%s", out)
	}
	if strings.Contains(out, "run-old") {
		t.Errorf("--limit 2 still listed the oldest run:\n%s", out)
	}

	out = captureHelp(t, func() { cmdMemory([]string{"reflect", "--list", "--limit", "1", "--json"}) })
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &runs); err != nil {
		t.Fatalf("--limit 1 --json output is not valid JSON: %v\n%s", err, out)
	}
	if len(runs) != 1 || runs[0].RunID != "run-new" {
		t.Errorf("--limit 1 --json = %+v, want only run-new", runs)
	}
}

func TestCmdMemoryReflectListEmptyJSON(t *testing.T) {
	isolateMemoryDirs(t)
	out := captureHelp(t, func() { cmdMemory([]string{"reflect", "--list", "--json"}) })
	var runs []*memory.ChangeSet
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &runs); err != nil {
		t.Fatalf("empty --list --json output is not valid JSON: %v\n%s", err, out)
	}
	if len(runs) != 0 {
		t.Errorf("empty --list --json listed %d runs, want 0", len(runs))
	}
}

func TestCmdMemoryReflectListFlagErrors(t *testing.T) {
	isolateMemoryDirs(t)

	if got := runExit(t, func() { cmdMemory([]string{"reflect", "--list", "--dry-run"}) }); got != 1 {
		t.Errorf("reflect --list --dry-run: exit status %d, want 1", got)
	}
	if got := runExit(t, func() { cmdMemory([]string{"reflect", "--list", "--revert", "run-1"}) }); got != 1 {
		t.Errorf("reflect --list --revert: exit status %d, want 1", got)
	}
	if got := runExit(t, func() { cmdMemory([]string{"reflect", "--list", "--limit", "many"}) }); got != 1 {
		t.Errorf("reflect --list --limit many: exit status %d, want 1", got)
	}
	// --list and --limit are real flags now; a bogus one is still rejected.
	if got := runExit(t, func() { cmdMemory([]string{"reflect", "--list", "--limit", "2", bogusFlag}) }); got != 2 {
		t.Errorf("reflect --list --limit 2 --bogus: exit status %d, want 2", got)
	}
}
