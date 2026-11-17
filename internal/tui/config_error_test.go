package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSavedConfigChecked(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		missing  bool // no file at all
		wantErr  bool
		wantLine int
		wantCol  int
	}{
		{name: "missing file", missing: true},
		{name: "empty file", content: "  \n"},
		{name: "valid", content: `{"completed": true}`},
		{name: "truncated one line", content: `{"completed": true,`, wantErr: true, wantLine: 1, wantCol: 19},
		{name: "bad token line 3", content: "{\n  \"a\": 1,\n  oops: 2\n}", wantErr: true, wantLine: 3, wantCol: 3},
		{name: "wrong type", content: "{\n  \"completed\": \"yes\"\n}", wantErr: true, wantLine: 2, wantCol: 20},
		{name: "not an object", content: `[1, 2]`, wantErr: true, wantLine: 1, wantCol: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // Windows
			path := filepath.Join(home, ".gleann", "config.json")
			if !tc.missing {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := LoadSavedConfigChecked()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("want *ConfigError, got %v", err)
			}
			if cfg != nil {
				t.Errorf("config must be nil on error")
			}
			if ce.Path != path || !strings.Contains(err.Error(), path) {
				t.Errorf("error should name %s: %v", path, err)
			}
			if ce.Line != tc.wantLine || ce.Column != tc.wantCol {
				t.Errorf("line:col = %d:%d, want %d:%d (%v)", ce.Line, ce.Column, tc.wantLine, tc.wantCol, err)
			}
			if LoadSavedConfig() != nil {
				t.Errorf("LoadSavedConfig must still return nil for a malformed file")
			}
			after, _ := os.ReadFile(path)
			if string(after) != tc.content {
				t.Errorf("file was modified")
			}
		})
	}
}
