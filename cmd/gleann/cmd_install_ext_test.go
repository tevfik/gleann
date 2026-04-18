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
	if _, ok := mcp["gleann"]; !ok {
		t.Fatal("expected gleann in mcp")
	}
}

func TestPatchOpenCodeJSONExisting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "opencode.json")
	os.WriteFile(path, []byte(`{"$schema":"test","mcp":{"other":{}}}`), 0o644)
	if err := patchOpenCodeJSON(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config map[string]interface{}
	json.Unmarshal(data, &config)
	mcp := config["mcp"].(map[string]interface{})
	if _, ok := mcp["other"]; !ok {
		t.Fatal("should preserve existing mcp entries")
	}
	if _, ok := mcp["gleann"]; !ok {
		t.Fatal("should add gleann entry")
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
