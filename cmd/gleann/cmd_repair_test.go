package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
)

// runRepair runs cmdRepair with exitFunc and the standard streams captured.
func runRepair(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	origExit, origOut, origErr := exitFunc, os.Stdout, os.Stderr
	defer func() { exitFunc, os.Stdout, os.Stderr = origExit, origOut, origErr }()

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	exitFunc = func(code int) {
		exitCode = code
		panic(fmt.Sprintf("exit_%d", code))
	}
	func() {
		defer func() {
			if r := recover(); r != nil && !strings.HasPrefix(fmt.Sprint(r), "exit_") {
				panic(r)
			}
		}()
		cmdRepair(args)
	}()
	wOut.Close()
	wErr.Close()
	o, _ := io.ReadAll(rOut)
	e, _ := io.ReadAll(rErr)
	return string(o), string(e), exitCode
}

func TestRepairHelp(t *testing.T) {
	out, _, code := runRepair(t, "--help")
	if code != 0 || !strings.Contains(out, "Usage: gleann index repair") || !strings.Contains(out, "--check") {
		t.Errorf("help output unexpected (exit %d): %q", code, out)
	}
}

func TestRepairNeedsANameOrAll(t *testing.T) {
	_, errOut, code := runRepair(t)
	if code != 1 || !strings.Contains(errOut, "usage:") {
		t.Errorf("want exit 1 with usage, got %d: %q", code, errOut)
	}
}

func TestRepairAllOnAnEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	out, _, code := runRepair(t, "--all", "--check", "--index-dir", dir)
	if code != 0 || !strings.Contains(out, "No indexes found") {
		t.Errorf("want a clean 'No indexes found', got exit %d: %q", code, out)
	}
}

func TestRepairUnknownIndexFails(t *testing.T) {
	_, errOut, code := runRepair(t, "does-not-exist", "--index-dir", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "does-not-exist") {
		t.Errorf("want exit 1 naming the index, got %d: %q", code, errOut)
	}
}

// captureStderr runs fn and returns what it wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestRepairProgress(t *testing.T) {
	if repairProgress("none", "x") != nil {
		t.Error("--progress none must disable the callback")
	}

	out := captureStderr(t, func() {
		f := repairProgress("plain", "docs")
		f(indexing.Progress{Phase: "vector_index", ItemsDone: 1, TotalItems: 4}) // first update is shown
		f(indexing.Progress{Phase: "vector_index", ItemsDone: 2, TotalItems: 4}) // rate-limited
		f(indexing.Progress{Phase: "vector_index", ItemsDone: 4, TotalItems: 4}) // the final one always is
		f(indexing.Progress{Phase: "vector_index", ItemsDone: 0, TotalItems: 0}) // nothing to report
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "docs: embedding 1/4 texts (25%)") || !strings.Contains(lines[1], "4/4 texts (100%)") {
		t.Errorf("plain progress lines: %q", lines)
	}

	out = captureStderr(t, func() {
		repairProgress("json", "docs")(indexing.Progress{Phase: "vector_index", ItemsDone: 4, TotalItems: 4})
	})
	if !strings.Contains(out, `"index":"docs"`) || !strings.Contains(out, `"pct":100.0`) {
		t.Errorf("json progress: %q", out)
	}
}
