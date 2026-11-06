package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// S-11: saving through the TUI must keep keys OnboardResult does not model.
func TestSaveConfig_PreservesUnknownKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".gleann"), 0o700)
	p := filepath.Join(home, ".gleann", "config.json")
	_ = os.WriteFile(p, []byte(`{"embedding_model":"a","search_config":{"top_k":42},"timezone":"Europe/Istanbul","rate_limit":5}`), 0o600)
	if err := SaveConfig(OnboardResult{EmbeddingModel: "b"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	data, _ := os.ReadFile(p)
	_ = json.Unmarshal(data, &got)
	if got["embedding_model"] != "b" {
		t.Fatalf("model not updated: %v", got)
	}
	for _, k := range []string{"search_config", "timezone", "rate_limit"} {
		if _, ok := got[k]; !ok {
			t.Errorf("key %q was dropped: %v", k, got)
		}
	}
	// A corrupt file is not overwritten.
	_ = os.WriteFile(p, []byte(`{"embedding_model":"a",}`), 0o600)
	if err := SaveConfig(OnboardResult{EmbeddingModel: "c"}); err == nil {
		t.Fatal("corrupt config must not be overwritten")
	}
	if d, _ := os.ReadFile(p); string(d) != `{"embedding_model":"a",}` {
		t.Fatalf("corrupt file modified: %s", d)
	}
}
