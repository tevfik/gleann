package roles

import (
	"os"
	"path/filepath"
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
	path := filepath.Join(dir, "role.txt")
	if err := os.WriteFile(path, []byte("role from file"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadMessage("file://" + path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "role from file" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestLoadMessageFileMissing(t *testing.T) {
	_, err := LoadMessage("file:///nonexistent/path.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRegistryResolveWithFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(path, []byte("From file."), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(map[string][]string{
		"hybrid": {"Inline prompt.", "file://" + path},
	})

	msgs, err := reg.Resolve("hybrid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 || msgs[0] != "Inline prompt." || msgs[1] != "From file." {
		t.Errorf("unexpected resolved messages: %v", msgs)
	}
}
