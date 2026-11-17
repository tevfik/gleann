package memory

import (
	"strings"
	"testing"
)

// BuildTaskContext keeps the blocks of the task's scope (and the global
// ones), and attaches only the failures that match the task's command or
// paths.
func TestBuildTaskContextScopeAndFailures(t *testing.T) {
	m := NewManager(openTestStore(t))
	if _, err := m.AddScopedNote("proj-a", TierLong, "a", "alpha convention"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddScopedNote("proj-b", TierLong, "b", "beta convention"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddNote(TierLong, "g", "global convention"); err != nil {
		t.Fatal(err)
	}
	// A failure with no error text is stored as "<command> failed".
	if _, err := m.RecordFailure(Failure{Command: "go test ./internal/store"}); err != nil {
		t.Fatal(err)
	}
	// A failure without a command is found by its paths only.
	if _, err := m.RecordFailure(Failure{Error: "lock held", Paths: []string{"internal/store/lock.go"}}); err != nil {
		t.Fatal(err)
	}

	cw, err := m.BuildTaskContext("proj-a", "go test ./internal/store", nil)
	if err != nil {
		t.Fatal(err)
	}
	var long []string
	for _, b := range cw.LongTerm {
		if b.Label != FailureLabel {
			long = append(long, b.Content)
		}
	}
	joined := strings.Join(long, "|")
	if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "global") || strings.Contains(joined, "beta") {
		t.Fatalf("long-term blocks for proj-a = %v", long)
	}
	if len(cw.Failures) != 1 || cw.Failures[0].Content != "go test ./internal/store failed" {
		t.Fatalf("failures for the command = %+v", cw.Failures)
	}

	cw, err = m.BuildTaskContext("", "", []string{"lock.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 1 || cw.Failures[0].Content != "lock held" {
		t.Fatalf("failures for the path = %+v", cw.Failures)
	}

	cw, err = m.BuildTaskContext("", "", nil)
	if err != nil || len(cw.Failures) != 0 {
		t.Fatalf("a task without command or paths gets no failures: %+v, %v", cw.Failures, err)
	}
}
