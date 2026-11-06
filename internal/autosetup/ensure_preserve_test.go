package autosetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// E-03/S-06: EnsureConfig must never destroy an existing config file.
func TestEnsureConfig_InvalidJSONIsLeftUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	_ = os.MkdirAll(filepath.Join(home, ".gleann"), 0o700)
	p := filepath.Join(home, ".gleann", "config.json")
	bad := `{"openai_api_key":"sk-secret","embedding_model":"x",}`
	_ = os.WriteFile(p, []byte(bad), 0o600)
	if _, err := EnsureConfig("http://127.0.0.1:1", true); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
	if d, _ := os.ReadFile(p); string(d) != bad {
		t.Fatalf("file was modified: %s", d)
	}
}

func TestEnsureConfig_PartialConfigIsMergedNotReplaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	_ = os.MkdirAll(filepath.Join(home, ".gleann"), 0o700)
	p := filepath.Join(home, ".gleann", "config.json")
	_ = os.WriteFile(p, []byte(`{"server_addr":"127.0.0.1:9999","openai_api_key":"sk-keep"}`), 0o600)
	if _, err := EnsureConfig("http://127.0.0.1:1", true); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	d, _ := os.ReadFile(p)
	_ = json.Unmarshal(d, &got)
	if got["server_addr"] != "127.0.0.1:9999" || got["openai_api_key"] != "sk-keep" {
		t.Fatalf("user keys lost: %v", got)
	}
	if got["embedding_provider"] == nil || got["completed"] != true {
		t.Fatalf("defaults not added: %v", got)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatalf("no .bak: %v", err)
	}
}
