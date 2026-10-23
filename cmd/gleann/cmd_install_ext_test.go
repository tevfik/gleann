package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── platformByName ─────────────────────────────

func TestPlatformByNameFound(t *testing.T) {
	for _, name := range []string{"opencode", "claude", "cursor", "codex", "gemini", "claw", "aider", "copilot"} {
		if p := platformByName(name); p == nil {
			t.Errorf("platformByName(%q) returned nil", name)
		}
	}
}

func TestPlatformByNameUnknown(t *testing.T) {
	if p := platformByName("nonexistent"); p != nil {
		t.Errorf("expected nil, got %v", p.Name)
	}
}

// ── appendOrCreateFile ─────────────────────────

func TestAppendOrCreateFileNew(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "TEST.md")
	if err := appendOrCreateFile(path, "# Hello\n", "Hello"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# Hello") {
		t.Fatal("expected content")
	}
}

func TestAppendOrCreateFileAlreadyPresent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "TEST.md")
	os.WriteFile(path, []byte("# Hello\n"), 0o644)
	if err := appendOrCreateFile(path, "EXTRA\n", "Hello"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "EXTRA") {
		t.Fatal("should not have appended duplicate")
	}
}

func TestAppendOrCreateFileAppend(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "TEST.md")
	os.WriteFile(path, []byte("# Existing\n"), 0o644)
	if err := appendOrCreateFile(path, "# New\n", "sentinel"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# Existing") || !strings.Contains(string(data), "# New") {
		t.Fatal("expected both sections")
	}
}

// ── removeSection ──────────────────────────────

func TestRemoveSectionPresent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.md")
	os.WriteFile(path, []byte("before\n<!-- start -->\nmiddle\n<!-- end -->\nafter\n"), 0o644)
	if err := removeSection(path, "<!-- start -->", "<!-- end -->"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "middle") {
		t.Fatal("section should have been removed")
	}
	if !strings.Contains(string(data), "before") || !strings.Contains(string(data), "after") {
		t.Fatal("surrounding text should remain")
	}
}

func TestRemoveSectionNotPresent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.md")
	os.WriteFile(path, []byte("hello\n"), 0o644)
	if err := removeSection(path, "<!-- start -->", "<!-- end -->"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello\n" {
		t.Fatal("file should be unchanged")
	}
}

func TestRemoveSectionFileNotExist(t *testing.T) {
	if err := removeSection("/nonexistent/path/file.md", "a", "b"); err != nil {
		t.Fatal("should return nil for missing file")
	}
}

func TestRemoveSectionNoEndSentinel(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.md")
	os.WriteFile(path, []byte("before\n<!-- start -->\nall rest\n"), 0o644)
	if err := removeSection(path, "<!-- start -->", "<!-- end -->"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "all rest") {
		t.Fatal("content after start sentinel should be removed when no end sentinel")
	}
}

// ── writeJSONIfAbsent ──────────────────────────

func TestWriteJSONIfAbsentNew(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	if err := writeJSONIfAbsent(path, `{"key":"val"}`); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"key":"val"`) {
		t.Fatal("expected content")
	}
}

func TestWriteJSONIfAbsentExists(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	os.WriteFile(path, []byte(`{"original":true}`), 0o644)
	if err := writeJSONIfAbsent(path, `{"key":"val"}`); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "original") {
		t.Fatal("should not overwrite existing file")
	}
}

// ── patchOpenCodeJSON ──────────────────────────

func TestPatchOpenCodeJSONNew(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "opencode.json")
	if err := patchOpenCodeJSON(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	mcp, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcp section")
	}
	servers, ok := mcp["servers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected servers in mcp")
	}
	if _, ok := servers["gleann"]; !ok {
		t.Fatal("expected gleann in mcp.servers")
	}
}

func TestPatchOpenCodeJSONExisting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "opencode.json")
	os.WriteFile(path, []byte(`{"$schema":"test","mcp":{"servers":{"other":{}}}}`), 0o644)
	if err := patchOpenCodeJSON(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config map[string]interface{}
	json.Unmarshal(data, &config)
	mcp := config["mcp"].(map[string]interface{})
	servers, ok := mcp["servers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected servers section")
	}
	if _, ok := servers["other"]; !ok {
		t.Fatal("should preserve existing mcp servers entries")
	}
	if _, ok := servers["gleann"]; !ok {
		t.Fatal("expected gleann in mcp.servers")
	}
}

func TestPatchOpenCodeJSONIdempotent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "opencode.json")
	patchOpenCodeJSON(path)
	data1, _ := os.ReadFile(path)
	patchOpenCodeJSON(path)
	data2, _ := os.ReadFile(path)
	if string(data1) != string(data2) {
		t.Fatal("should be idempotent")
	}
}

func TestPatchOpenCodeJSONCleanupLegacyPlugins(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "opencode.json")
	os.WriteFile(path, []byte(`{"plugins":{"gleann":{"old":true}}}`), 0o644)
	if err := patchOpenCodeJSON(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config map[string]interface{}
	json.Unmarshal(data, &config)
	if _, ok := config["plugins"]; ok {
		t.Fatal("should have removed empty plugins section")
	}
}

func TestPatchOpenCodeJSONInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "opencode.json")
	os.WriteFile(path, []byte(`not valid json`), 0o644)
	err := patchOpenCodeJSON(path)
	if err == nil {
		t.Fatal("should fail on invalid JSON")
	}
}

// ── patchClaudeSettings ────────────────────────

func TestPatchClaudeSettingsNew(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	if err := patchClaudeSettings(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		t.Fatal("expected hooks")
	}
	pre, _ := hooks["PreToolUse"].([]interface{})
	if len(pre) == 0 {
		t.Fatal("expected PreToolUse hook")
	}
}

func TestPatchClaudeSettingsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	patchClaudeSettings(path)
	data1, _ := os.ReadFile(path)
	patchClaudeSettings(path)
	data2, _ := os.ReadFile(path)
	if string(data1) != string(data2) {
		t.Fatal("should be idempotent")
	}
}

func TestPatchClaudeSettingsInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	os.WriteFile(path, []byte(`{bad}`), 0o644)
	err := patchClaudeSettings(path)
	if err == nil {
		t.Fatal("should fail")
	}
}

// ── patchGeminiSettings ────────────────────────

func TestPatchGeminiSettingsNew(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	if err := patchGeminiSettings(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)
	hooks := settings["hooks"].(map[string]interface{})
	if hooks["gleann"] == nil {
		t.Fatal("expected gleann hook")
	}
}

func TestPatchGeminiSettingsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	patchGeminiSettings(path)
	data1, _ := os.ReadFile(path)
	patchGeminiSettings(path)
	data2, _ := os.ReadFile(path)
	if string(data1) != string(data2) {
		t.Fatal("should be idempotent")
	}
}

func TestPatchGeminiSettingsInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	os.WriteFile(path, []byte(`{bad}`), 0o644)
	if err := patchGeminiSettings(path); err == nil {
		t.Fatal("should fail")
	}
}

// ── Platform Detect tests ──────────────────────

func TestPlatformDetectOpenCode(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	// No .opencode dir → not detected
	p := platformByName("opencode")
	if p.Detect(tmp, home) {
		t.Fatal("should not detect without .opencode")
	}
	os.MkdirAll(filepath.Join(tmp, ".opencode"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect with .opencode in dir")
	}
}

func TestPlatformDetectClaude(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("claude")
	if p.Detect(tmp, home) {
		t.Fatal("should not detect")
	}
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect with ~/.claude")
	}
}

func TestPlatformDetectCursor(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("cursor")
	if p.Detect(tmp, home) {
		t.Fatal("should not detect")
	}
	os.MkdirAll(filepath.Join(tmp, ".cursor"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect")
	}
}

func TestPlatformDetectCodex(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("codex")
	os.MkdirAll(filepath.Join(tmp, ".codex"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect with .codex")
	}
}

func TestPlatformDetectGemini(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("gemini")
	os.MkdirAll(filepath.Join(tmp, ".gemini"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect")
	}
}

func TestPlatformDetectClaw(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("claw")
	os.MkdirAll(filepath.Join(home, ".openclaw"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect")
	}
}

func TestPlatformDetectAider(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("aider")
	os.WriteFile(filepath.Join(tmp, ".aider.conf.yml"), []byte(""), 0o644)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect with .aider.conf.yml")
	}
}

func TestPlatformDetectCopilot(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	p := platformByName("copilot")
	os.MkdirAll(filepath.Join(home, ".copilot"), 0o755)
	if !p.Detect(tmp, home) {
		t.Fatal("should detect")
	}
}

// ── Install / Uninstall per platform ───────────

func TestInstallUninstallOpenCode(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installOpenCode(dir, home); err != nil {
		t.Fatal(err)
	}
	// Verify AGENTS.md
	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(data), "gleann: Code Intelligence") {
		t.Fatal("AGENTS.md should contain gleann section")
	}
	// Verify plugin file
	if _, err := os.Stat(filepath.Join(dir, ".opencode", "plugins", "gleann.js")); err != nil {
		t.Fatal("plugin file should exist")
	}
	// Verify opencode.json
	data, _ = os.ReadFile(filepath.Join(dir, "opencode.json"))
	if !strings.Contains(string(data), "gleann") {
		t.Fatal("opencode.json should contain gleann mcp")
	}
	// Uninstall
	if err := uninstallOpenCode(dir, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".opencode", "plugins", "gleann.js")); err == nil {
		t.Fatal("plugin should be removed")
	}
}

func TestInstallUninstallClaude(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(data), "gleann") {
		t.Fatal("CLAUDE.md should contain gleann")
	}
	settingsData, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(settingsData), "PreToolUse") {
		t.Fatal("settings.json should contain hook")
	}
	// Uninstall
	if err := uninstallClaude(dir, home); err != nil {
		t.Fatal(err)
	}
}

func TestInstallUninstallCursor(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installCursor(dir, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "rules", "gleann.mdc")); err != nil {
		t.Fatal("rules file should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "mcp.json")); err != nil {
		t.Fatal("mcp.json should exist")
	}
	if err := uninstallCursor(dir, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "rules", "gleann.mdc")); err == nil {
		t.Fatal("rules file should be removed")
	}
}

func TestInstallUninstallCodex(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installCodex(dir, home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(data), "gleann") {
		t.Fatal("AGENTS.md should contain gleann")
	}
	if _, err := os.Stat(filepath.Join(dir, ".codex", "hooks.json")); err != nil {
		t.Fatal("hooks.json should exist")
	}
	if err := uninstallCodex(dir, home); err != nil {
		t.Fatal(err)
	}
}

func TestInstallUninstallGemini(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installGemini(dir, home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "GEMINI.md"))
	if !strings.Contains(string(data), "gleann") {
		t.Fatal("GEMINI.md should contain gleann")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gemini", "settings.json")); err != nil {
		t.Fatal("settings.json should exist")
	}
	if err := uninstallGemini(dir, home); err != nil {
		t.Fatal(err)
	}
}

func TestInstallUninstallClaw(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installClaw(dir, home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(data), "gleann") {
		t.Fatal("AGENTS.md should contain gleann")
	}
	if _, err := os.Stat(filepath.Join(home, ".openclaw", "skills", "gleann", "SKILL.md")); err != nil {
		t.Fatal("SKILL.md should exist")
	}
	if err := uninstallClaw(dir, home); err != nil {
		t.Fatal(err)
	}
}

func TestInstallUninstallAider(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installAider(dir, home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(data), "gleann") {
		t.Fatal("should contain gleann")
	}
	if err := uninstallAider(dir, home); err != nil {
		t.Fatal(err)
	}
}

func TestInstallUninstallCopilot(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := installCopilot(dir, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "skills", "gleann", "SKILL.md")); err != nil {
		t.Fatal("SKILL.md should exist")
	}
	if err := uninstallCopilot(dir, home); err != nil {
		t.Fatal(err)
	}
}

// ── printInstallUsage ──────────────────────────

func TestPrintInstallUsageExt3(t *testing.T) {
	// Just verify it doesn't panic.
	printInstallUsage()
}

// ── printMemoryUsage ───────────────────────────

func TestPrintMemoryUsageExt3(t *testing.T) {
	printMemoryUsage()
}

func TestInstallClaudeUserWritesGlobalFile(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := installClaudeUser(home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil || !strings.Contains(string(data), "gleann") {
		t.Fatalf("~/.claude/CLAUDE.md should contain gleann, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "CLAUDE.md")); err == nil {
		t.Fatal("user-level install must not write into the working directory")
	}
	if err := uninstallClaudeUser(home); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if strings.Contains(string(data), "gleann: Code Intelligence") {
		t.Fatal("section should be removed")
	}
}

func TestInstallGeminiUserWritesGlobalFiles(t *testing.T) {
	home := t.TempDir()
	if err := installGeminiUser(home); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"GEMINI.md", "settings.json"} {
		if _, err := os.Stat(filepath.Join(home, ".gemini", f)); err != nil {
			t.Fatalf("~/.gemini/%s should exist: %v", f, err)
		}
	}
	if err := uninstallGeminiUser(home); err != nil {
		t.Fatal(err)
	}
}

func TestPositionalPlatform(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"claude"}, "claude"},
		{[]string{"uninstall", "gemini"}, "gemini"},
		{[]string{"--dir", "claude", "cursor"}, "cursor"},
		{[]string{"--index", "myindex", "opencode"}, "opencode"},
		{[]string{"--project", "claude"}, "claude"},
		{[]string{"uninstall"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := positionalPlatform(c.args); got != c.want {
			t.Errorf("positionalPlatform(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestSymlinkGuard_AppendOrCreateFile(t *testing.T) {
	tmp := t.TempDir()
	targetPath := filepath.Join(tmp, "canonical.md")
	os.WriteFile(targetPath, []byte("# Canonical fleet document\n"), 0o644)

	symlinkPath := filepath.Join(tmp, "symlink.md")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	followSymlinkFlag = false
	defer func() { followSymlinkFlag = false }()

	// 1. Without --follow-symlink, symlink target must remain untouched
	err := appendOrCreateFile(symlinkPath, "## gleann: Code Intelligence\n", "gleann: Code Intelligence")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	targetData, _ := os.ReadFile(targetPath)
	if strings.Contains(string(targetData), "gleann: Code Intelligence") {
		t.Fatal("target file must NOT be modified when path is a symlink and followSymlinkFlag is false")
	}

	// 2. With --follow-symlink, symlink target should be updated
	followSymlinkFlag = true
	err = appendOrCreateFile(symlinkPath, "## gleann: Code Intelligence\n", "gleann: Code Intelligence")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	targetData, _ = os.ReadFile(targetPath)
	if !strings.Contains(string(targetData), "gleann: Code Intelligence") {
		t.Fatal("target file SHOULD be modified when followSymlinkFlag is true")
	}
}

func TestSymlinkGuard_PatchOpenCodeJSON(t *testing.T) {
	tmp := t.TempDir()
	targetPath := filepath.Join(tmp, "canonical_opencode.json")
	os.WriteFile(targetPath, []byte(`{"$schema":"https://opencode.ai/config.json"}`), 0o644)

	symlinkPath := filepath.Join(tmp, "symlink_opencode.json")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	followSymlinkFlag = false
	defer func() { followSymlinkFlag = false }()

	err := patchOpenCodeJSON(symlinkPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(targetPath)
	if strings.Contains(string(data), "mcp") {
		t.Fatal("canonical config must not be modified through symlink without follow flag")
	}
}

func TestResolveTargetDirectory(t *testing.T) {
	tmp := t.TempDir()

	// 1. Explicit directory takes highest priority
	resolved, err := resolveTargetDirectory(tmp, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != tmp {
		t.Errorf("resolveTargetDirectory(%q) = %q, want %q", tmp, resolved, tmp)
	}

	// 2. Fallback to git root or cwd
	resolvedDefault, err := resolveTargetDirectory("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolvedDefault == "" {
		t.Fatal("expected non-empty resolved directory")
	}
}

func TestResolveIndexNameForDir(t *testing.T) {
	// Explicit index flag returns directly
	if got := resolveIndexNameForDir("/some/dir", "my-idx"); got != "my-idx" {
		t.Errorf("resolveIndexNameForDir with explicit index = %q, want my-idx", got)
	}

	// Empty when no index matches an unrelated directory
	unrelated := t.TempDir()
	if got := resolveIndexNameForDir(unrelated, ""); got != "" {
		t.Errorf("resolveIndexNameForDir on empty temp dir = %q, want empty", got)
	}
}

func TestResolveIndexNameForDir_RelativeSourceDirDoesNotMatch(t *testing.T) {
	tempIdxDir := t.TempDir()
	idxSubDir := filepath.Join(tempIdxDir, "devran")
	if err := os.MkdirAll(idxSubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	meta := map[string]any{
		"name":       "devran",
		"source_dir": ".",
	}
	data, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(idxSubDir, "devran.meta.json"), data, 0o644)

	t.Setenv("GLEANN_INDEX_DIR", tempIdxDir)

	unrelated := t.TempDir()
	if got := resolveIndexNameForDir(unrelated, ""); got != "" {
		t.Errorf("expected relative source_dir '.' to NOT match unrelated temp dir %s, got %q", unrelated, got)
	}
}

func TestResolveIndexNameForDir_SymlinkMatchesCanonicalPath(t *testing.T) {
	tempIdxDir := t.TempDir()
	realRepoDir := t.TempDir()

	idxSubDir := filepath.Join(tempIdxDir, "myrepo")
	if err := os.MkdirAll(idxSubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{
		"name":       "myrepo",
		"source_dir": realRepoDir,
	}
	data, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(idxSubDir, "myrepo.meta.json"), data, 0o644)

	t.Setenv("GLEANN_INDEX_DIR", tempIdxDir)

	// Create a symlink to realRepoDir
	symlinkDir := filepath.Join(t.TempDir(), "repo_link")
	if err := os.Symlink(realRepoDir, symlinkDir); err != nil {
		t.Skip("symlinks not supported on this platform")
	}

	if got := resolveIndexNameForDir(symlinkDir, ""); got != "myrepo" {
		t.Errorf("expected symlink %s to resolve to myrepo, got %q", symlinkDir, got)
	}
}

func TestDynamicIndexInjection(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()

	activeInstallIndex = "quantum_engine"
	defer func() { activeInstallIndex = "" }()

	// OpenCode AGENTS.md
	if err := installOpenCode(dir, home); err != nil {
		t.Fatal(err)
	}
	agentsData, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agentsData), "quantum_engine") {
		t.Errorf("AGENTS.md should contain index name quantum_engine, got:\n%s", string(agentsData))
	}

	// Claude CLAUDE.md
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	claudeData, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(claudeData), "quantum_engine") {
		t.Errorf("CLAUDE.md should contain index name quantum_engine, got:\n%s", string(claudeData))
	}

	// Cursor .cursor/rules/gleann.mdc
	if err := installCursor(dir, home); err != nil {
		t.Fatal(err)
	}
	cursorData, _ := os.ReadFile(filepath.Join(dir, ".cursor", "rules", "gleann.mdc"))
	if !strings.Contains(string(cursorData), "quantum_engine") {
		t.Errorf("Cursor rule should contain index name quantum_engine, got:\n%s", string(cursorData))
	}

	// Gemini GEMINI.md
	if err := installGemini(dir, home); err != nil {
		t.Fatal(err)
	}
	geminiData, _ := os.ReadFile(filepath.Join(dir, "GEMINI.md"))
	if !strings.Contains(string(geminiData), "quantum_engine") {
		t.Errorf("GEMINI.md should contain index name quantum_engine, got:\n%s", string(geminiData))
	}

	// Claw SKILL.md
	if err := installClaw(dir, home); err != nil {
		t.Fatal(err)
	}
	skillData, _ := os.ReadFile(filepath.Join(home, ".openclaw", "skills", "gleann", "SKILL.md"))
	if !strings.Contains(string(skillData), "quantum_engine") {
		t.Errorf("SKILL.md should contain index name quantum_engine, got:\n%s", string(skillData))
	}
}

func TestPatchOpenCodeJSON_OpenCodeV2Schema(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "opencode.json")

	// 1. Fresh install creates mcp.servers.gleann
	if err := patchOpenCodeJSON(configPath); err != nil {
		t.Fatalf("patchOpenCodeJSON failed: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	mcpSec, ok := cfg["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcp section in config")
	}
	if _, hasLegacy := mcpSec["gleann"]; hasLegacy {
		t.Error("fresh install should NOT produce legacy flat mcp.gleann key")
	}
	servers, ok := mcpSec["servers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcp.servers section in config")
	}
	gleannEntry, ok := servers["gleann"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcp.servers.gleann")
	}
	if gleannEntry["type"] != "local" {
		t.Errorf("expected type=local, got %v", gleannEntry["type"])
	}

	// 2. Legacy config with flat mcp.gleann is migrated and flat key is removed
	legacyConfigPath := filepath.Join(tmp, "legacy_opencode.json")
	legacyRaw := `{
		"$schema": "https://opencode.ai/config.json",
		"mcp": {
			"gleann": {
				"type": "local",
				"command": ["/usr/bin/gleann", "mcp"],
				"enabled": true
			}
		}
	}`
	if err := os.WriteFile(legacyConfigPath, []byte(legacyRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := patchOpenCodeJSON(legacyConfigPath); err != nil {
		t.Fatalf("patchOpenCodeJSON on legacy failed: %v", err)
	}
	legData, _ := os.ReadFile(legacyConfigPath)
	var legCfg map[string]interface{}
	_ = json.Unmarshal(legData, &legCfg)
	legMcp := legCfg["mcp"].(map[string]interface{})
	if _, hasFlat := legMcp["gleann"]; hasFlat {
		t.Error("legacy flat mcp.gleann was not cleaned up")
	}
	legServers := legMcp["servers"].(map[string]interface{})
	if legServers["gleann"] == nil {
		t.Error("gleann was not migrated to mcp.servers.gleann")
	}

	// 3. Uninstall cleans up mcp.servers.gleann
	if err := uninstallOpenCode(tmp, tmp); err != nil {
		t.Fatalf("uninstallOpenCode failed: %v", err)
	}
	dataAfter, _ := os.ReadFile(configPath)
	var cfgAfter map[string]interface{}
	_ = json.Unmarshal(dataAfter, &cfgAfter)
	if cfgAfter["mcp"] != nil {
		if mcpAfter, ok := cfgAfter["mcp"].(map[string]interface{}); ok {
			if mcpAfter["servers"] != nil {
				t.Error("servers section should be removed when empty")
			}
		}
	}
}
