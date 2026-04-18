package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformByName(t *testing.T) {
	for _, name := range []string{"opencode", "claude", "cursor", "codex", "gemini", "claw", "aider", "copilot"} {
		p := platformByName(name)
		if p == nil {
			t.Errorf("platformByName(%q) returned nil", name)
		}
		if p.Name != name {
			t.Errorf("platformByName(%q).Name = %q", name, p.Name)
		}
		if p.Description == "" {
			t.Errorf("platformByName(%q).Description is empty", name)
		}
	}

	// Unknown.
	if p := platformByName("unknown"); p != nil {
		t.Error("unknown platform should return nil")
	}
}

func TestPlatformDetect(t *testing.T) {
	tmpDir := t.TempDir()
	home := t.TempDir()

	// Nothing exists → no detection.
	for _, p := range platforms {
		if p.Detect(tmpDir, home) {
			t.Errorf("platform %s detected in empty dirs", p.Name)
		}
	}

	// Create .opencode dir → opencode detected.
	os.MkdirAll(filepath.Join(tmpDir, ".opencode"), 0755)
	p := platformByName("opencode")
	if !p.Detect(tmpDir, home) {
		t.Error("opencode not detected with .opencode dir")
	}

	// Create .claude dir in home → claude detected.
	os.MkdirAll(filepath.Join(home, ".claude"), 0755)
	p = platformByName("claude")
	if !p.Detect(tmpDir, home) {
		t.Error("claude not detected with .claude in home")
	}

	// Create CLAUDE.md in dir → claude also detected.
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("# claude"), 0644)
	if !p.Detect(tmpDir, home) {
		t.Error("claude not detected with CLAUDE.md")
	}

	// Create .cursor dir → cursor detected.
	os.MkdirAll(filepath.Join(tmpDir, ".cursor"), 0755)
	p = platformByName("cursor")
	if !p.Detect(tmpDir, home) {
		t.Error("cursor not detected")
	}

	// Create .codex dir → codex detected.
	os.MkdirAll(filepath.Join(tmpDir, ".codex"), 0755)
	p = platformByName("codex")
	if !p.Detect(tmpDir, home) {
		t.Error("codex not detected")
	}

	// Create .gemini dir in home → gemini detected.
	os.MkdirAll(filepath.Join(home, ".gemini"), 0755)
	p = platformByName("gemini")
	if !p.Detect(tmpDir, home) {
		t.Error("gemini not detected")
	}

	// Create .openclaw dir in home → claw detected.
	os.MkdirAll(filepath.Join(home, ".openclaw"), 0755)
	p = platformByName("claw")
	if !p.Detect(tmpDir, home) {
		t.Error("claw not detected")
	}

	// Create .aider.conf.yml → aider detected.
	os.WriteFile(filepath.Join(tmpDir, ".aider.conf.yml"), []byte("# aider"), 0644)
	p = platformByName("aider")
	if !p.Detect(tmpDir, home) {
		t.Error("aider not detected")
	}

	// Create .copilot in home → copilot detected.
	os.MkdirAll(filepath.Join(home, ".copilot"), 0755)
	p = platformByName("copilot")
	if !p.Detect(tmpDir, home) {
		t.Error("copilot not detected")
	}
}

func TestPlatformInstallUninstall(t *testing.T) {
	tmpDir := t.TempDir()
	home := t.TempDir()

	// Test install/uninstall for each platform (best-effort, some may fail without deps).
	for _, p := range platforms {
		t.Run(p.Name, func(t *testing.T) {
			err := p.Install(tmpDir, home)
			if err != nil {
				t.Logf("Install %s: %v (may need deps)", p.Name, err)
			}
			err = p.Uninstall(tmpDir, home)
			if err != nil {
				t.Logf("Uninstall %s: %v", p.Name, err)
			}
		})
	}
}

func TestPlatforms(t *testing.T) {
	if len(platforms) < 8 {
		t.Errorf("expected >= 8 platforms, got %d", len(platforms))
	}
	names := make(map[string]bool)
	for _, p := range platforms {
		if names[p.Name] {
			t.Errorf("duplicate platform name: %s", p.Name)
		}
		names[p.Name] = true
		if p.Install == nil {
			t.Errorf("platform %s has nil Install", p.Name)
		}
		if p.Uninstall == nil {
			t.Errorf("platform %s has nil Uninstall", p.Name)
		}
		if p.Detect == nil {
			t.Errorf("platform %s has nil Detect", p.Name)
		}
	}
}

func TestAgentsMDSection(t *testing.T) {
	if agentsMDSection == "" {
		t.Error("agentsMDSection is empty")
	}
	if len(agentsMDSection) < 100 {
		t.Error("agentsMDSection seems too short")
	}
}

func TestPrintInstallUsage(t *testing.T) {
	// Should not panic.
	// Redirect stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printInstallUsage()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if n == 0 {
		t.Error("printInstallUsage produced no output")
	}
}
