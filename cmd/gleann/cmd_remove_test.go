package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createTestIndex writes a minimal index directory with metadata so
// gleann.ListIndexes sees it.
func createTestIndex(t *testing.T, indexDir, name string) {
	t.Helper()
	dir := filepath.Join(indexDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// indexExists reports whether the index directory is still present.
func indexExists(indexDir, name string) bool {
	_, err := os.Stat(filepath.Join(indexDir, name))
	return err == nil
}

// The value of a value-taking flag must never be treated as an index name:
// "index remove foo --model bar" removes only foo, not the index bar.
func TestCmdRemoveSkipsFlagValues(t *testing.T) {
	indexDir := t.TempDir()
	createTestIndex(t, indexDir, "foo")
	createTestIndex(t, indexDir, "bar")

	cmdRemove([]string{"foo", "--model", "bar", "--index-dir", indexDir})

	if indexExists(indexDir, "foo") {
		t.Error("index foo should have been removed")
	}
	if !indexExists(indexDir, "bar") {
		t.Error("index bar was the value of --model and must not be removed")
	}
}

// The same holds for the "--flag=value" spelling, and names after "--" are
// still removed.
func TestCmdRemoveFlagEqualsAndDoubleDash(t *testing.T) {
	indexDir := t.TempDir()
	createTestIndex(t, indexDir, "foo")
	createTestIndex(t, indexDir, "bar")

	cmdRemove([]string{"foo", "--model=bar", "--index-dir=" + indexDir})

	if indexExists(indexDir, "foo") {
		t.Error("index foo should have been removed")
	}
	if !indexExists(indexDir, "bar") {
		t.Error("index bar was the value of --model=bar and must not be removed")
	}

	createTestIndex(t, indexDir, "foo")
	cmdRemove([]string{"--index-dir", indexDir, "--", "foo"})

	if indexExists(indexDir, "foo") {
		t.Error("index foo after -- should have been removed")
	}
	if !indexExists(indexDir, "bar") {
		t.Error("index bar must not be removed")
	}
}

func runRemove(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
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
		cmdRemove(args)
	}()
	wOut.Close()
	wErr.Close()
	o, _ := io.ReadAll(rOut)
	e, _ := io.ReadAll(rErr)
	return string(o), string(e), exitCode
}

func TestCmdRemoveUnmatchedLiteralNameDoesNotDeleteDirectory(t *testing.T) {
	indexDir := t.TempDir()
	createTestIndex(t, indexDir, "valid")
	userDir := filepath.Join(indexDir, "src")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, _ := runRemove(t, "src", "--index-dir", indexDir)

	if !strings.Contains(stderr, "not a gleann index") {
		t.Errorf("stderr = %q, want 'not a gleann index'", stderr)
	}
	if !indexExists(indexDir, "src") {
		t.Fatal("non-index directory src was deleted by cmdRemove!")
	}
	if _, err := os.Stat(filepath.Join(userDir, "main.go")); err != nil {
		t.Fatalf("src/main.go was lost: %v", err)
	}
}
