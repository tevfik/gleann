package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRegistryBuiltinRoles(t *testing.T) {
	reg := DefaultRegistry()
	expected := []string{"default", "explain", "reviewer", "shell", "summarize"}

	names := reg.List()
	if len(names) != len(expected) {
		t.Fatalf("expected %d roles, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("names[%d] = %q, want %q", i, names[i], name)
		}
	}
}

func TestRegistryGet(t *testing.T) {
	reg := NewRegistry(map[string][]string{
		"test": {"You are a test role."},
	})

	msgs, err := reg.Get("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "You are a test role." {
		t.Errorf("unexpected messages: %v", msgs)
	}

	_, err = reg.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent role")
	}
}

func TestRegistrySystemPrompt(t *testing.T) {
	reg := NewRegistry(map[string][]string{
		"multi": {"Line one.", "Line two."},
	})

	prompt, err := reg.SystemPrompt("multi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "Line one.\n\nLine two." {
		t.Errorf("unexpected prompt: %q", prompt)
	}
}

func TestRegistryHasAndAdd(t *testing.T) {
	reg := NewRegistry(nil)
	if reg.Has("custom") {
		t.Error("should not have 'custom' role")
	}

	reg.Add("custom", []string{"Custom prompt."})
	if !reg.Has("custom") {
		t.Error("should have 'custom' role after Add")
	}
}

func TestLoadMessagePlainText(t *testing.T) {
	content, err := LoadMessage("plain text content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "plain text content" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestLoadMessageFilePrefix(t *testing.T) {
	dir := t.TempDir()
	oldRolesDir := RolesDir
	RolesDir = dir
	defer func() { RolesDir = oldRolesDir }()

	filename := "role.txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("role from file"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadMessage("file://" + filename)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "role from file" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestLoadMessageFileMissing(t *testing.T) {
	dir := t.TempDir()
	oldRolesDir := RolesDir
	RolesDir = dir
	defer func() { RolesDir = oldRolesDir }()

	_, err := LoadMessage("file://nonexistent.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRegistryResolveWithFile(t *testing.T) {
	dir := t.TempDir()
	oldRolesDir := RolesDir
	RolesDir = dir
	defer func() { RolesDir = oldRolesDir }()

	filename := "prompt.txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("From file."), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(map[string][]string{
		"hybrid": {"Inline prompt.", "file://" + filename},
	})

	msgs, err := reg.Resolve("hybrid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 || msgs[0] != "Inline prompt." || msgs[1] != "From file." {
		t.Errorf("unexpected resolved messages: %v", msgs)
	}
}

func TestLoadMessagePathTraversalSecurity(t *testing.T) {
	rolesDir := t.TempDir()
	secretDir := t.TempDir()

	oldRolesDir := RolesDir
	RolesDir = rolesDir
	defer func() { RolesDir = oldRolesDir }()

	secretFile := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0600); err != nil {
		t.Fatal(err)
	}

	// Try to access the secret file via path traversal
	// We need to construct a relative path from rolesDir to secretFile
	relPath, err := filepath.Rel(rolesDir, secretFile)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadMessage("file://" + relPath)
	if err == nil {
		t.Error("expected security error for path traversal, but got none")
	} else if !strings.Contains(err.Error(), "outside allowed roles directory") {
		t.Errorf("expected security error message, got: %v", err)
	}

	// Try with direct absolute path (if it's not under rolesDir)
	_, err = LoadMessage("file://" + secretFile)
	if err == nil {
		t.Error("expected security error for absolute path traversal, but got none")
	} else if !strings.Contains(err.Error(), "outside allowed roles directory") {
		t.Errorf("expected security error message, got: %v", err)
	}
}
