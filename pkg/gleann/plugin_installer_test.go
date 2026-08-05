package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallPlugin(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	pluginDir := filepath.Join(tempHome, ".gleann", "plugins", "test-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := UninstallPlugin("test-plugin")
	if err != nil {
		t.Fatalf("UninstallPlugin failed: %v", err)
	}

	if _, err := os.Stat(pluginDir); !os.IsNotExist(err) {
		t.Fatalf("expected plugin dir to be removed")
	}
}

func TestRepoName(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://github.com/tevfik/gleann.git", "gleann"},
		{"https://github.com/tevfik/gleann-plugin-docs", "gleann-plugin-docs"},
		{"invalid/url/", ""},
	}

	for _, tt := range tests {
		actual := repoName(tt.url)
		if actual != tt.expected {
			t.Errorf("repoName(%q) = %q, want %q", tt.url, actual, tt.expected)
		}
	}
}
