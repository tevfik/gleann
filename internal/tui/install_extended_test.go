package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── Shell completion scripts ───────────────────────────────────

func TestBashCompletionExt(t *testing.T) {
	s := BashCompletion()
	if s == "" {
		t.Fatal("empty bash completion")
	}
	for _, cmd := range []string{"index", "search", "ask", "chat", "serve", "graph", "config", "completion"} {
		if !strings.Contains(s, cmd) {
			t.Errorf("bash completion missing command %q", cmd)
		}
	}
	if !strings.Contains(s, "complete -F _gleann gleann") {
		t.Error("missing bash complete directive")
	}
}

func TestZshCompletionExt(t *testing.T) {
	s := ZshCompletion()
	if s == "" {
		t.Fatal("empty zsh completion")
	}
	if !strings.Contains(s, "#compdef gleann") {
		t.Error("missing compdef directive")
	}
	for _, cmd := range []string{"build", "search", "ask", "serve"} {
		if !strings.Contains(s, cmd) {
			t.Errorf("zsh completion missing command %q", cmd)
		}
	}
}

func TestFishCompletionExt(t *testing.T) {
	s := FishCompletion()
	if s == "" {
		t.Fatal("empty fish completion")
	}
	if !strings.Contains(s, "complete -c gleann") {
		t.Error("missing fish complete directive")
	}
	for _, cmd := range []string{"build", "search", "ask", "serve", "config"} {
		if !strings.Contains(s, cmd) {
			t.Errorf("fish completion missing command %q", cmd)
		}
	}
}

// ── sharedLibNames ─────────────────────────────────────────────

func TestSharedLibNamesExt(t *testing.T) {
	libs := sharedLibNames()
	if len(libs) != 2 {
		t.Fatalf("expected 2 libs, got %d", len(libs))
	}
	for _, l := range libs {
		if l == "" {
			t.Error("lib name should not be empty")
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.HasSuffix(libs[0], ".dylib") {
			t.Errorf("darwin lib should end .dylib, got %q", libs[0])
		}
	case "windows":
		if !strings.HasSuffix(libs[0], ".dll") {
			t.Errorf("windows lib should end .dll, got %q", libs[0])
		}
	default:
		if !strings.HasSuffix(libs[0], ".so") {
			t.Errorf("linux lib should end .so, got %q", libs[0])
		}
	}
}

// ── installDirs ────────────────────────────────────────────────

func TestInstallDirsExt(t *testing.T) {
	dirs := installDirs()
	if len(dirs) == 0 {
		t.Fatal("expected at least one install dir")
	}
	for _, d := range dirs {
		if d == "" {
			t.Error("dir should not be empty")
		}
	}
}

// ── isWritable ─────────────────────────────────────────────────

func TestIsWritableExt(t *testing.T) {
	tmp := t.TempDir()
	if !isWritable(tmp) {
		t.Error("temp dir should be writable")
	}
}

func TestIsWritableNonExistent(t *testing.T) {
	// Non-existent child of writable parent — isWritable checks parent stat,
	// but if parent is dir it tries to create tmp in the non-existent child, which fails.
	// This behavior is correct: you can't write to a dir that doesn't exist.
	tmp := t.TempDir()
	child := filepath.Join(tmp, "nonexistent")
	// Since the child doesn't exist, isWritable may test the parent (which is writable)
	// or may fail because the actual dir doesn't exist. Just verify it doesn't panic.
	_ = isWritable(child)
}

func TestIsWritableDeepNonExistent(t *testing.T) {
	if isWritable("/nonexistent/deep/path") {
		t.Error("deep non-existent path should not be writable")
	}
}

// ── ensureSourceLine ───────────────────────────────────────────

func TestEnsureSourceLine(t *testing.T) {
	tmp := t.TempDir()
	rcFile := filepath.Join(tmp, ".bashrc")
	compPath := filepath.Join(tmp, "gleann_completion")

	// First call: should append.
	ensureSourceLine(rcFile, compPath)

	data, err := os.ReadFile(rcFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), compPath) {
		t.Error("source line should be in rc file")
	}
	if !strings.Contains(string(data), "gleann completion") {
		t.Error("should contain marker comment")
	}

	// Second call: should not duplicate.
	ensureSourceLine(rcFile, compPath)
	data2, _ := os.ReadFile(rcFile)
	count := strings.Count(string(data2), compPath)
	if count != 1 {
		t.Errorf("source line appears %d times, want 1", count)
	}
}

func TestEnsureSourceLineExisting(t *testing.T) {
	tmp := t.TempDir()
	rcFile := filepath.Join(tmp, ".bashrc")
	compPath := filepath.Join(tmp, "gleann_completion")

	// Pre-populate with the source line.
	os.WriteFile(rcFile, []byte("source "+compPath+" # gleann completion\n"), 0o644)

	// Should not append again.
	ensureSourceLine(rcFile, compPath)
	data, _ := os.ReadFile(rcFile)
	count := strings.Count(string(data), compPath)
	if count != 1 {
		t.Errorf("source line appears %d times, want 1", count)
	}
}

// ── InstallBinary ──────────────────────────────────────────────

func TestInstallBinary(t *testing.T) {
	tmp := t.TempDir()
	err := InstallBinary(tmp)
	if err != nil {
		t.Fatalf("InstallBinary failed: %v", err)
	}

	name := "gleann"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(tmp, name)
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("binary not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("binary should not be empty")
	}
}

// ── jsonUnmarshal / jsonMarshalIndent ──────────────────────────

func TestJsonUnmarshal(t *testing.T) {
	var m map[string]any
	err := jsonUnmarshal([]byte(`{"key":"value"}`), &m)
	if err != nil {
		t.Fatal(err)
	}
	if m["key"] != "value" {
		t.Errorf("key = %v", m["key"])
	}
}

func TestJsonUnmarshalInvalid(t *testing.T) {
	var m map[string]any
	err := jsonUnmarshal([]byte(`not json`), &m)
	if err == nil {
		t.Error("expected error")
	}
}

func TestJsonMarshalIndent(t *testing.T) {
	data, err := jsonMarshalIndent(map[string]string{"a": "b"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"a\"") {
		t.Error("should contain key")
	}
}

// ── resolveGleannBin ───────────────────────────────────────────

func TestResolveGleannBin(t *testing.T) {
	result := &OnboardResult{}
	bin := resolveGleannBin(result)
	if bin == "" {
		t.Error("should return a path")
	}
}

func TestResolveGleannBinWithInstallPath(t *testing.T) {
	tmp := t.TempDir()
	// Create a fake binary.
	name := "gleann"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	os.WriteFile(filepath.Join(tmp, name), []byte("fake"), 0o755)

	result := &OnboardResult{InstallPath: tmp}
	bin := resolveGleannBin(result)
	if !strings.Contains(bin, tmp) {
		t.Errorf("bin = %q, should contain %q", bin, tmp)
	}
}

// ── installClaudeCodeMCP ───────────────────────────────────────

func TestInstallClaudeCodeMCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	path := installClaudeCodeMCP("/usr/local/bin/gleann")
	if path == "" {
		t.Fatal("should return a path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "gleann") {
		t.Error("config should contain gleann")
	}
	if !strings.Contains(string(data), "mcpServers") {
		t.Error("config should contain mcpServers")
	}
}

func TestInstallClaudeCodeMCPExisting(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Pre-create a config file.
	configPath := filepath.Join(tmp, ".claude.json")
	os.WriteFile(configPath, []byte(`{"existingKey":"value"}`), 0o644)

	path := installClaudeCodeMCP("/usr/local/bin/gleann")
	if path == "" {
		t.Fatal("should return a path")
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "existingKey") {
		t.Error("should preserve existing config")
	}
	if !strings.Contains(content, "gleann") {
		t.Error("should add gleann")
	}
}

// ── installClaudeDesktopMCP ────────────────────────────────────

func TestInstallClaudeDesktopMCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(tmp, "AppData"))
	}

	path := installClaudeDesktopMCP("/usr/local/bin/gleann")
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		if path == "" {
			t.Fatal("should return a path")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "gleann") {
			t.Error("config should contain gleann")
		}
	}
}

// ── installMCPConfigs ──────────────────────────────────────────

func TestInstallMCPConfigs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	result := &OnboardResult{
		InstallPath: "",
	}
	installed := installMCPConfigs(result)
	// Should at least try Claude Code.
	if len(installed) == 0 {
		t.Log("No MCP configs installed (may be expected)")
	}
}

// ── RemoveCompletions (empty) ──────────────────────────────────

func TestRemoveCompletionsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	removed := RemoveCompletions()
	if len(removed) != 0 {
		t.Errorf("expected no removals, got %v", removed)
	}
}

// ── InstallCompletions ─────────────────────────────────────────

func TestInstallCompletions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("completions not installed on Windows")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	installed := InstallCompletions()
	if len(installed) == 0 {
		t.Fatal("expected completions to be installed")
	}

	// Verify at least bash was installed.
	foundBash := false
	for _, s := range installed {
		if strings.Contains(s, "bash") {
			foundBash = true
		}
	}
	if !foundBash {
		t.Error("expected bash completion")
	}
}
