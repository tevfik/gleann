//go:build treesitter

package mcp

import (
	"testing"
)

func TestTruncPath_Short(t *testing.T) {
	result := truncPath("src/main.go", 50)
	if result != "src/main.go" {
		t.Errorf("expected unchanged, got: %s", result)
	}
}

func TestTruncPath_Long(t *testing.T) {
	path := "/very/long/path/to/some/deeply/nested/file/in/the/project/source/code/main.go"
	result := truncPath(path, 30)
	if len(result) > 30 {
		t.Errorf("expected max 30 chars, got %d: %s", len(result), result)
	}
	if result[len(result)-3:] != "..." {
		t.Error("expected ... suffix")
	}
}

func TestTruncPath_Exact(t *testing.T) {
	result := truncPath("12345", 5)
	if result != "12345" {
		t.Errorf("expected exact match: %s", result)
	}
}
