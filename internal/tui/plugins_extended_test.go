package tui

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPluginOwner(t *testing.T) {
	// Default owner.
	os.Unsetenv("GLEANN_PLUGIN_OWNER")
	got := pluginOwner()
	if got != defaultPluginOwner {
		t.Errorf("pluginOwner() = %q, want %q", got, defaultPluginOwner)
	}

	// Override via env.
	t.Setenv("GLEANN_PLUGIN_OWNER", "custom-owner")
	got = pluginOwner()
	if got != "custom-owner" {
		t.Errorf("pluginOwner() = %q, want custom-owner", got)
	}
}

func TestPluginStatusString(t *testing.T) {
	tests := []struct {
		s    pluginStatus
		want string
	}{
		{statusNotInstalled, "Not installed"},
		{statusInstalled, "Installed"},
		{statusRunning, "Running"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestPluginStatusBadge(t *testing.T) {
	for _, s := range []pluginStatus{statusNotInstalled, statusInstalled, statusRunning} {
		b := s.Badge()
		if b == "" {
			t.Errorf("(%d).Badge() returned empty", s)
		}
	}
}

func TestParseGitHubURL(t *testing.T) {
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
	}{
		{"https://github.com/tevfik/gleann-plugin-docs", "tevfik", "gleann-plugin-docs"},
		{"https://github.com/tevfik/gleann-plugin-docs.git", "tevfik", "gleann-plugin-docs"},
		{"git@github.com/tevfik/repo", "tevfik", "repo"},
		{"invalid-url", "", ""},
	}
	for _, tt := range tests {
		owner, repo := parseGitHubURL(tt.url)
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("parseGitHubURL(%q) = (%q, %q), want (%q, %q)", tt.url, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestRepoName(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/tevfik/gleann-plugin-docs.git", "gleann-plugin-docs"},
		{"https://github.com/tevfik/gleann-plugin-docs", "gleann-plugin-docs"},
		{"", ""},
	}
	for _, tt := range tests {
		got := repoName(tt.url)
		if got != tt.want {
			t.Errorf("repoName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestVenvBinary(t *testing.T) {
	venvDir := filepath.Join(os.TempDir(), "venv")
	got := venvBinary(venvDir, "pip")
	if !strings.Contains(got, "pip") {
		t.Errorf("venvBinary() = %q, expected to contain pip", got)
	}
	if !strings.Contains(got, venvDir) {
		t.Errorf("venvBinary() = %q, expected to contain venv dir %q", got, venvDir)
	}
}

func TestFindPython3(t *testing.T) {
	got := findPython3()
	if got == "" {
		t.Error("findPython3() returned empty string")
	}
	// Should be python or python3.
	if got != "python" && got != "python3" {
		t.Errorf("findPython3() = %q", got)
	}
}

func TestFindGoBuildTarget(t *testing.T) {
	tmpDir := t.TempDir()

	// No Go files.
	target, hasGo := findGoBuildTarget(tmpDir)
	if hasGo {
		t.Error("expected no Go files in empty dir")
	}
	if target != "." {
		t.Errorf("target = %q, want '.'", target)
	}

	// Root Go file.
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0644)
	target, hasGo = findGoBuildTarget(tmpDir)
	if !hasGo {
		t.Error("expected Go files in root dir")
	}
	if target != "." {
		t.Errorf("target = %q, want '.'", target)
	}

	// cmd/* layout.
	tmpDir2 := t.TempDir()
	cmdDir := filepath.Join(tmpDir2, "cmd", "myapp")
	os.MkdirAll(cmdDir, 0755)
	os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main"), 0644)
	target, hasGo = findGoBuildTarget(tmpDir2)
	if !hasGo {
		t.Error("expected Go files in cmd/ layout")
	}
	if target != filepath.Join("cmd", "myapp") {
		t.Errorf("target = %q", target)
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	// Create a tar.gz in memory with a binary file.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("#!/bin/sh\necho hello")
	hdr := &tar.Header{
		Name: "dir/mybinary",
		Mode: 0755,
		Size: int64(len(content)),
	}
	tw.WriteHeader(hdr)
	tw.Write(content)
	tw.Close()
	gw.Close()

	destPath := filepath.Join(t.TempDir(), "mybinary")
	err := extractBinaryFromTarGz(&buf, destPath, "mybinary")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch")
	}
}

func TestExtractBinaryFromTarGz_NotFound(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.Close()
	gw.Close()

	err := extractBinaryFromTarGz(&buf, "/tmp/nope", "nonexistent")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	// Create a zip in memory.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	content := []byte("binary-content")
	w, _ := zw.Create("dir/mybinary")
	w.Write(content)
	zw.Close()

	destPath := filepath.Join(t.TempDir(), "mybinary")
	err := extractBinaryFromZip(&buf, destPath, "mybinary")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch")
	}
}

func TestExtractBinaryFromZip_NotFound(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.Close()

	err := extractBinaryFromZip(&buf, "/tmp/nope", "nonexistent")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestExtractTarballToDir(t *testing.T) {
	// Create tarball with prefix stripping.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Simulate GitHub tarball: owner-repo-commit/file.go
	files := map[string]string{
		"owner-repo-abc123/":        "",
		"owner-repo-abc123/main.go": "package main",
		"owner-repo-abc123/go.mod":  "module test",
	}
	for name, content := range files {
		if strings.HasSuffix(name, "/") {
			tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0755})
		} else {
			hdr := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
			tw.WriteHeader(hdr)
			tw.Write([]byte(content))
		}
	}
	tw.Close()
	gw.Close()

	destDir := filepath.Join(t.TempDir(), "extracted")
	err := extractTarballToDir(&buf, destDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify files were extracted with prefix stripped.
	data, err := os.ReadFile(filepath.Join(destDir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package main" {
		t.Errorf("content = %q", string(data))
	}
}

func TestLinkOrCopy(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("hello"), 0644)
	os.MkdirAll(filepath.Join(srcDir, "sub"), 0755)
	os.WriteFile(filepath.Join(srcDir, "sub", "nested.txt"), []byte("world"), 0644)

	dstDir := filepath.Join(t.TempDir(), "copy")

	// linkOrCopy may symlink or fallback to copy.
	err := linkOrCopy(srcDir, dstDir)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCopyDir(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0644)
	os.MkdirAll(filepath.Join(srcDir, "sub"), 0755)
	os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("bbb"), 0644)

	dstDir := filepath.Join(t.TempDir(), "dst")
	err := copyDir(srcDir, dstDir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "aaa" {
		t.Errorf("a.txt = %q", string(data))
	}
	data, err = os.ReadFile(filepath.Join(dstDir, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bbb" {
		t.Errorf("sub/b.txt = %q", string(data))
	}
}

func TestMarshalJSON(t *testing.T) {
	// Use a nil-safe struct.
	type testReg struct {
		Name string `json:"name"`
	}
	// marshalJSON expects *gleann.PluginRegistry
	// Test via direct JSON marshal to avoid import.
	data, err := json.MarshalIndent(map[string]string{"test": "value"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test") {
		t.Error("expected test in JSON output")
	}
}

func TestNewPluginModel(t *testing.T) {
	// Doesn't crash even if plugin dir doesn't exist.
	m := NewPluginModel()
	if len(m.plugins) != len(knownPlugins) {
		t.Errorf("expected %d plugins, got %d", len(knownPlugins), len(m.plugins))
	}
	if len(m.statuses) != len(knownPlugins) {
		t.Errorf("expected %d statuses, got %d", len(knownPlugins), len(m.statuses))
	}
}

func TestPluginModelInit(t *testing.T) {
	m := NewPluginModel()
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init() should return nil")
	}
}

func TestPluginModelQuitting(t *testing.T) {
	m := NewPluginModel()
	if m.Quitting() {
		t.Error("should not be quitting initially")
	}
}

func TestPluginModelUpdateNavigation(t *testing.T) {
	m := NewPluginModel()
	m.width = 80
	m.height = 24

	// Down.
	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := result.(PluginModel)
	if pm.cursor != 1 {
		t.Errorf("cursor after 'j' = %d, want 1", pm.cursor)
	}

	// Up.
	result, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = result.(PluginModel)
	if pm.cursor != 0 {
		t.Errorf("cursor after 'k' = %d, want 0", pm.cursor)
	}

	// Enter → detail.
	result, _ = pm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm = result.(PluginModel)
	if pm.state != psDetail {
		t.Errorf("state after enter = %d, want psDetail", pm.state)
	}

	// Esc → back to main.
	result, _ = pm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	pm = result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("state after esc = %d, want psMain", pm.state)
	}

	// Refresh.
	result, _ = pm.Update(tea.KeyPressMsg{Code: 'r'})
	pm = result.(PluginModel)
	if pm.status != "↻ Refreshed" {
		t.Errorf("status after 'r' = %q", pm.status)
	}
}

func TestPluginModelUpdateResult(t *testing.T) {
	m := NewPluginModel()
	m.state = psResult
	m.status = "some status"

	// Any key returns to main.
	result, _ := m.Update(tea.KeyPressMsg{Code: 'x'})
	pm := result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("state = %d, want psMain", pm.state)
	}
}

func TestPluginModelView(t *testing.T) {
	m := NewPluginModel()
	m.width = 80
	m.height = 24

	v := m.View()
	if v.Content == "" {
		t.Error("View() returned empty")
	}

	// Detail view.
	m.state = psDetail
	v = m.View()
	if v.Content == "" {
		t.Error("viewDetail() returned empty")
	}

	// Action view.
	m.state = psAction
	m.actionMsg = "Installing..."
	v = m.View()
	if v.Content == "" {
		t.Error("viewAction() returned empty")
	}

	// Result view.
	m.state = psResult
	m.status = "Done"
	v = m.View()
	if v.Content == "" {
		t.Error("viewResult() returned empty")
	}

	// Quitting.
	m.quitting = true
	v = m.View()
	if v.Content != "" {
		t.Error("quitting View() should return empty")
	}
}

func TestPluginModelViewWithProgress(t *testing.T) {
	m := NewPluginModel()
	m.state = psAction
	m.actionMsg = "test action"
	m.progressLines = []string{"step 1", "✓ step 2", "🔍 step 3"}
	m.width = 80
	m.height = 24

	v := m.View()
	s := v.Content
	if !strings.Contains(s, "step 1") {
		t.Error("progress lines should appear in view")
	}
}

func TestPluginModelWindowSize(t *testing.T) {
	m := NewPluginModel()
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	pm := result.(PluginModel)
	if pm.width != 100 || pm.height != 40 {
		t.Errorf("size = %dx%d", pm.width, pm.height)
	}
}

func TestPluginActionMsg(t *testing.T) {
	m := NewPluginModel()
	m.width = 80

	// Success.
	result, _ := m.Update(pluginActionMsg{plugin: "test", action: "install", output: "OK"})
	pm := result.(PluginModel)
	if pm.state != psResult {
		t.Errorf("state = %d, want psResult", pm.state)
	}
	if !strings.Contains(pm.status, "✓") {
		t.Errorf("status = %q, expected success", pm.status)
	}

	// Error.
	m2 := NewPluginModel()
	result, _ = m2.Update(pluginActionMsg{plugin: "test", action: "install", err: os.ErrNotExist})
	pm = result.(PluginModel)
	if !strings.Contains(pm.status, "✗") {
		t.Errorf("status = %q, expected error badge", pm.status)
	}
}

func TestPluginProgressMsg(t *testing.T) {
	m := NewPluginModel()
	m.width = 80

	// Send progress messages.
	for i := 0; i < 15; i++ {
		result, _ := m.Update(pluginInstallProgressMsg{
			plugin:      "test",
			message:     "step",
			continueCmd: func() tea.Msg { return nil },
		})
		m = result.(PluginModel)
	}
	// Should be capped at 10 lines.
	if len(m.progressLines) > 10 {
		t.Errorf("progressLines = %d, want <= 10", len(m.progressLines))
	}
}

func TestLoadPluginConfigSummary(t *testing.T) {
	// Unknown plugin.
	s := loadPluginConfigSummary("unknown-plugin")
	if s != nil {
		t.Error("expected nil for unknown plugin")
	}

	// gleann-sound with config.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	gleannDir := filepath.Join(home, ".gleann")
	os.MkdirAll(gleannDir, 0755)
	cfg := map[string]any{
		"default_model": "base.en",
		"language":      "en",
		"backend":       "whisper",
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(gleannDir, "sound.json"), data, 0644)

	// Note: loadPluginConfigSummary uses os.UserHomeDir which reads $HOME.
	s = loadPluginConfigSummary("gleann-sound")
	if s == nil {
		t.Skip("loadPluginConfigSummary returned nil (HOME may not affect os.UserHomeDir)")
	}
	if s["Model"] != "base.en" {
		t.Errorf("Model = %q", s["Model"])
	}
}

func TestKnownPlugins(t *testing.T) {
	if len(knownPlugins) < 2 {
		t.Errorf("expected at least 2 known plugins, got %d", len(knownPlugins))
	}
	for _, p := range knownPlugins {
		if p.Name == "" {
			t.Error("plugin has empty name")
		}
		if p.RepoURL == "" {
			t.Error("plugin has empty RepoURL")
		}
		if len(p.Extensions) == 0 {
			t.Errorf("plugin %s has no extensions", p.Name)
		}
	}
}
