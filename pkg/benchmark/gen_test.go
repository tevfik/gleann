package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("a/a.go", `package a

// ComputeChecksum calculates a rolling checksum over the given byte slice quickly.
func ComputeChecksum(b []byte) uint32 { return 0 }

// Close releases resources.
func Close() {}
`)
	w("b/b.go", `package b

// Close releases other resources for the second package entirely.
func Close() {}

// Widget describes a thing that renders pixels onto a canvas surface.
type Widget struct{}
`)
	w("a/a_test.go", `package a

// TestOnly is a documented test helper that should never be sampled here.
func TestOnly() {}
`)
	return dir
}

func TestGenerateGoTasks_Doc(t *testing.T) {
	tasks, err := GenerateGoTasks(writeTree(t), GenOptions{Count: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2 (ComputeChecksum, Widget): %+v", len(tasks), tasks)
	}
	for _, task := range tasks {
		if strings.Contains(task.Query, task.Description) {
			t.Errorf("query %q still contains the identifier %q", task.Query, task.Description)
		}
		if len(task.GoldFiles) != 1 || strings.HasSuffix(task.GoldFiles[0], "_test.go") {
			t.Errorf("bad gold %v", task.GoldFiles)
		}
	}
}

func TestGenerateGoTasks_SymbolSkipsAmbiguousNames(t *testing.T) {
	tasks, err := GenerateGoTasks(writeTree(t), GenOptions{Kind: "symbol", Count: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Query == "Close" {
			t.Fatalf("ambiguous symbol Close must be dropped: %+v", task)
		}
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d, want 2", len(tasks))
	}
}

func TestGenerateGoTasks_Deterministic(t *testing.T) {
	dir := writeTree(t)
	a, _ := GenerateGoTasks(dir, GenOptions{})
	b, _ := GenerateGoTasks(dir, GenOptions{})
	if len(a) != len(b) || a[0].ID != b[0].ID || a[0].Query != b[0].Query {
		t.Fatal("generation is not deterministic")
	}
}

func TestGenerateGoTasks_BadKind(t *testing.T) {
	if _, err := GenerateGoTasks(t.TempDir(), GenOptions{Kind: "x"}); err == nil {
		t.Fatal("want error")
	}
}
