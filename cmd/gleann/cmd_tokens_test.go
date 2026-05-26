package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdTokens(t *testing.T) {
	// Create a temp directory
	tmpDir, err := os.MkdirTemp("", "gleann_tokens_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a temp file with Go content
	content := `package main

import "fmt"

func main() {
	// This is a test comment
	fmt.Println("hello world")
}
`
	tmpFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Run command
	cmdTokens([]string{tmpFile})

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read output
	data, _ := io.ReadAll(r)
	out := string(data)

	// Verify output
	if !strings.Contains(out, "Token Estimation") {
		t.Errorf("expected header in output, got:\n%s", out)
	}
	if !strings.Contains(out, "aggressive") {
		t.Errorf("expected aggressive mode in output, got:\n%s", out)
	}
	if !strings.Contains(out, "signatures") {
		t.Errorf("expected signatures mode in output, got:\n%s", out)
	}
}
