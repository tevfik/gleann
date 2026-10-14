package gleann

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{
		"a", "docs", "my-index", "my_index", "v1.2", "Index42", "gleann-plugin-docs",
		"proj_memory_vectors", "0start", strings.Repeat("x", MaxNameLength),
	}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}

	invalid := []string{
		"", ".", "..", "../etc", "..%2F", "a/b", `a\b`, "/abs", "-x", "--help",
		".hidden", "_private", "has space", "semi;colon", "null\x00byte", "tab\t",
		"ünicode", "a:b", strings.Repeat("x", MaxNameLength+1),
	}
	for _, n := range invalid {
		err := ValidateName(n)
		if err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
			continue
		}
		if !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateName(%q) error %v does not wrap ErrInvalidName", n, err)
		}
	}
}

func TestRemoveIndex_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	indexDir := filepath.Join(root, "indexes")
	sibling := filepath.Join(root, "sibling")
	for _, d := range []string{indexDir, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"", ".", "..", "../sibling", "a/../../sibling", `..\sibling`} {
		if err := RemoveIndex(indexDir, name); err == nil {
			t.Errorf("RemoveIndex(%q) = nil, want error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(sibling, "keep.txt")); err != nil {
		t.Fatalf("sibling directory was touched: %v", err)
	}
	if _, err := os.Stat(indexDir); err != nil {
		t.Fatalf("index dir itself was removed: %v", err)
	}
}

func TestIndexMetaHelpers_RejectTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := GetIndexMeta(dir, ".."); err == nil {
		t.Error("GetIndexMeta(..) should fail")
	}
	if err := UpdateIndexMeta(dir, "../x", func(*IndexMeta) {}); err == nil {
		t.Error("UpdateIndexMeta(../x) should fail")
	}
	s := NewSearcher(DefaultConfig(), nil)
	if err := s.Load(t.Context(), "../x"); err == nil || !errors.Is(err, ErrInvalidName) {
		t.Errorf("Load(../x) = %v, want ErrInvalidName", err)
	}
}

func TestUninstallPlugin_RejectsTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	victim := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(filepath.Join(victim, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"..", "../..", "-rf", "", "a/b"} {
		if _, err := UninstallPlugin(name); err == nil {
			t.Errorf("UninstallPlugin(%q) = nil, want error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "plugins")); err != nil {
		t.Fatalf("plugins dir was removed: %v", err)
	}
}

func TestConfigRedacted(t *testing.T) {
	c := DefaultConfig()
	c.OpenAIAPIKey = "sk-live"
	c.SearchConfig.RerankerConfig.APIKey = "jina-key"

	r := c.Redacted()
	if r.OpenAIAPIKey != RedactedSecret || r.SearchConfig.RerankerConfig.APIKey != RedactedSecret {
		t.Fatalf("secrets not redacted: %+v", r)
	}
	if c.OpenAIAPIKey != "sk-live" || c.SearchConfig.RerankerConfig.APIKey != "jina-key" {
		t.Fatal("Redacted mutated the receiver")
	}
	if r.EmbeddingModel != c.EmbeddingModel {
		t.Error("non-secret fields must be preserved")
	}

	// Empty secrets stay empty (so the UI can tell "unset" from "set").
	empty := DefaultConfig().Redacted()
	if empty.OpenAIAPIKey != "" || empty.SearchConfig.RerankerConfig.APIKey != "" {
		t.Error("empty secrets must not be replaced by the sentinel")
	}
}

func TestConfigRestoreRedactedSecrets(t *testing.T) {
	prev := DefaultConfig()
	prev.OpenAIAPIKey = "sk-old"
	prev.SearchConfig.RerankerConfig.APIKey = "rr-old"

	// Round-trip of the redacted value keeps the old secret.
	in := prev.Redacted()
	in.RestoreRedactedSecrets(prev)
	if in.OpenAIAPIKey != "sk-old" || in.SearchConfig.RerankerConfig.APIKey != "rr-old" {
		t.Fatalf("sentinel should restore previous secrets, got %q / %q", in.OpenAIAPIKey, in.SearchConfig.RerankerConfig.APIKey)
	}

	// New values replace; empty clears.
	in = prev.Redacted()
	in.OpenAIAPIKey = "sk-new"
	in.SearchConfig.RerankerConfig.APIKey = ""
	in.RestoreRedactedSecrets(prev)
	if in.OpenAIAPIKey != "sk-new" {
		t.Errorf("new secret should win, got %q", in.OpenAIAPIKey)
	}
	if in.SearchConfig.RerankerConfig.APIKey != "" {
		t.Errorf("empty secret should clear, got %q", in.SearchConfig.RerankerConfig.APIKey)
	}
}
