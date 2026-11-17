package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func newFailureTestManager(t *testing.T) *Manager {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewManager(store)
}

// Two error texts that differ only in a path, line number, hex address or
// timestamp must normalize to the same signature, so they deduplicate.
func TestNormalizeErrorSignature(t *testing.T) {
	cases := []struct{ a, b string }{
		{
			"panic: nil pointer at /home/u/proj/pkg/memory/block.go:42:7",
			"panic: nil pointer at /tmp/other/pkg/memory/block.go:99:3",
		},
		{"error at 0x7f2a1b3c4d5e: failed", "error at 0xdeadbeef: failed"},
		{"timeout at 2026-11-16 12:34:56", "timeout at 2026-11-17 01:02:03"},
		{"Error: open block.go:12: no such file", "Error: open block.go:88: no such file"},
	}
	for _, tc := range cases {
		a, b := NormalizeErrorSignature(tc.a), NormalizeErrorSignature(tc.b)
		if a != b {
			t.Errorf("signatures differ:\n  %q → %q\n  %q → %q", tc.a, a, tc.b, b)
		}
	}
}

// Recording the same failure twice — differing only in a path/line number —
// must leave one block with Confirms 2.
func TestRecordFailureDedupsBySignature(t *testing.T) {
	mgr := newFailureTestManager(t)

	f1 := Failure{Command: "go test ./pkg/memory", Error: "panic: nil pointer at /home/u/proj/pkg/memory/block.go:42:7", Fix: "nil check"}
	f2 := Failure{Command: "go test ./pkg/memory", Error: "panic: nil pointer at /tmp/other/pkg/memory/block.go:99:3", Fix: "nil check"}

	b1, err := mgr.RecordFailure(f1)
	if err != nil {
		t.Fatal(err)
	}
	if b1.Confirms != 1 {
		t.Fatalf("first record Confirms = %d, want 1", b1.Confirms)
	}

	b2, err := mgr.RecordFailure(f2)
	if err != nil {
		t.Fatal(err)
	}
	if b2.Confirms != 2 {
		t.Fatalf("second record Confirms = %d, want 2", b2.Confirms)
	}
	if b1.ID != b2.ID {
		t.Fatalf("dedup should reuse block %s, got %s", b1.ID, b2.ID)
	}

	failures, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure block, got %d", len(failures))
	}
}

// A different command is a different failure even when the error text is the
// same.
func TestRecordFailureDifferentCommandsDoNotDedup(t *testing.T) {
	mgr := newFailureTestManager(t)

	if _, err := mgr.RecordFailure(Failure{Command: "go test", Error: "boom", Fix: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RecordFailure(Failure{Command: "go build", Error: "boom", Fix: "x"}); err != nil {
		t.Fatal(err)
	}

	failures, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 2 {
		t.Fatalf("expected 2 failure blocks, got %d", len(failures))
	}
}

// Dedup unions paths and symbols so the second recording's provenance is not
// lost.
func TestRecordFailureDedupUnionsProvenance(t *testing.T) {
	mgr := newFailureTestManager(t)

	if _, err := mgr.RecordFailure(Failure{Command: "go test", Error: "boom", Fix: "x", Paths: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	b, err := mgr.RecordFailure(Failure{Command: "go test", Error: "boom", Fix: "x", Paths: []string{"b.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Paths) != 2 {
		t.Fatalf("expected 2 unioned paths, got %v", b.Paths)
	}
}

// FailuresFor matches on command (normalized, prefix in either direction).
func TestFailuresForByCommand(t *testing.T) {
	mgr := newFailureTestManager(t)
	must := func(f Failure) {
		t.Helper()
		if _, err := mgr.RecordFailure(f); err != nil {
			t.Fatal(err)
		}
	}
	must(Failure{Command: "go test ./pkg/memory", Error: "boom", Fix: "x"})
	must(Failure{Command: "go build ./cmd/gleann", Error: "bam", Fix: "y"})

	got, err := mgr.FailuresFor("go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Metadata[FailureMetaCommand] != "go test ./pkg/memory" {
		t.Fatalf("FailuresFor(go test) = %v, want the go test failure", got)
	}

	// A more specific command also matches its stored prefix.
	got, err = mgr.FailuresFor("go test ./pkg/memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("FailuresFor(full command) = %d, want 1", len(got))
	}

	// No match.
	got, err = mgr.FailuresFor("go vet", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("FailuresFor(go vet) = %d, want 0", len(got))
	}
}

// FailuresFor matches on path overlap (cleaned path and base name).
func TestFailuresForByPath(t *testing.T) {
	mgr := newFailureTestManager(t)
	must := func(f Failure) {
		t.Helper()
		if _, err := mgr.RecordFailure(f); err != nil {
			t.Fatal(err)
		}
	}
	must(Failure{Command: "go test", Error: "boom", Fix: "x", Paths: []string{"pkg/memory/block.go"}})
	must(Failure{Command: "go test", Error: "bam", Fix: "y", Paths: []string{"cmd/gleann/main.go"}})

	got, err := mgr.FailuresFor("", []string{"pkg/memory/block.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Paths[0] != "pkg/memory/block.go" {
		t.Fatalf("FailuresFor(path) = %v, want the block.go failure", got)
	}

	// Base-name match.
	got, err = mgr.FailuresFor("", []string{"block.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("FailuresFor(base name) = %d, want 1", len(got))
	}

	// No overlap.
	got, err = mgr.FailuresFor("", []string{"other.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("FailuresFor(no overlap) = %d, want 0", len(got))
	}
}

// Failures surface in their own section when a task names a command or file,
// and they are absent from the plain tier context.
func TestBuildTaskContextSurfacesFailures(t *testing.T) {
	mgr := newFailureTestManager(t)

	if _, err := mgr.RecordFailure(Failure{
		Command: "go test ./pkg/memory",
		Error:   "panic: nil pointer at /x/y/block.go:42",
		Fix:     "nil check",
		Paths:   []string{"pkg/memory/block.go"},
	}); err != nil {
		t.Fatal(err)
	}

	cw, err := mgr.BuildTaskContext("", "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 1 {
		t.Fatalf("expected 1 surfaced failure, got %d", len(cw.Failures))
	}
	rendered := cw.Render()
	if !strings.Contains(rendered, "<failure_memory>") {
		t.Errorf("missing <failure_memory> section:\n%s", rendered)
	}
	if !strings.Contains(rendered, "go test ./pkg/memory") {
		t.Errorf("missing failure command:\n%s", rendered)
	}
	if !strings.Contains(rendered, "nil check") {
		t.Errorf("missing failure fix:\n%s", rendered)
	}

	// Without a task the failure is not surfaced, and does not appear in the
	// regular long-term tier either.
	cw, err = mgr.BuildTaskContext("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 0 {
		t.Fatalf("expected 0 failures without a task, got %d", len(cw.Failures))
	}
	if len(cw.LongTerm) != 0 {
		t.Fatalf("failure leaked into LongTerm: %v", cw.LongTerm)
	}
}

// Failures count against the injection budget and rank like long-term blocks:
// under the cap they render, over a tiny cap they are dropped.
func TestFailuresCountAgainstBudget(t *testing.T) {
	mgr := newFailureTestManager(t)
	if _, err := mgr.RecordFailure(Failure{
		Command: "go test ./pkg/memory",
		Error:   "panic: nil pointer at /x/y/block.go:42",
		Fix:     "nil check",
	}); err != nil {
		t.Fatal(err)
	}

	mgr.MaxContextTokens = 100000
	cw, err := mgr.BuildTaskContext("", "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 1 {
		t.Fatalf("expected 1 failure under the cap, got %d", len(cw.Failures))
	}
	if !strings.Contains(cw.Render(), "<failure_memory>") {
		t.Errorf("failure should render under the cap:\n%s", cw.Render())
	}

	mgr.MaxContextTokens = 1
	cw, err = mgr.BuildTaskContext("", "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cw.Render() != "" {
		t.Fatalf("over-budget render should be empty, got %q", cw.Render())
	}
	if cw.Budget == nil || cw.Budget.DroppedCount != 1 {
		t.Fatalf("Budget = %+v, want 1 dropped failure", cw.Budget)
	}
}
