package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeLauncher makes installSource() return a tiny file instead of the (large)
// test binary, so installs in tests stay cheap.
func fakeLauncher(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gleann-launcher")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho gleann\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_LAUNCHER", p)
	return p
}

func gleannBinName() string {
	if runtime.GOOS == "windows" {
		return "gleann.exe"
	}
	return "gleann"
}

func TestRunInstall_NoPathIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out := captureStdout(t, func() { RunInstall(&OnboardResult{}) })
	if out != "" {
		t.Errorf("nothing selected, nothing printed: %q", out)
	}
}

func TestRunInstall_FullFlowIntoUserDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	launcher := fakeLauncher(t)
	target := filepath.Join(home, "bin")

	out := captureStdout(t, func() {
		RunInstall(&OnboardResult{
			InstallPath:        target,
			InstallCompletions: true,
			MCPEnabled:         true,
			ServerEnabled:      true,
			ServerAddr:         ":9090",
		})
	})
	for _, want := range []string{"Binary installed", "Installing shell completions", "Installing MCP server configs", "REST API Server enabled", "serve --addr :9090", "localhost:9090/health"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	got, err := os.ReadFile(filepath.Join(target, gleannBinName()))
	if err != nil {
		t.Fatalf("binary not installed: %v", err)
	}
	want, _ := os.ReadFile(launcher)
	if string(got) != string(want) {
		t.Error("the launcher must be what is installed")
	}

	// MCP config must point at the installed binary.
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("claude config not written: %v", err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	g := cfg.MCPServers["gleann"]
	if g.Command != filepath.Join(target, gleannBinName()) || len(g.Args) != 1 || g.Args[0] != "mcp" {
		t.Errorf("MCP entry: %+v", g)
	}
}

func TestRunInstall_DefaultServerAddrAndFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fakeLauncher(t)

	out := captureStdout(t, func() {
		RunInstall(&OnboardResult{InstallPath: filepath.Join(home, "bin"), ServerEnabled: true})
	})
	// An empty address falls back to the real default bind, not ":8080".
	if !strings.Contains(out, "serve --addr 127.0.0.1:8081") || !strings.Contains(out, "curl http://127.0.0.1:8081/health") {
		t.Errorf("default address expected:\n%s", out)
	}

	// A regular file where the directory must be created: the install fails and
	// nothing after the binary step (completions, MCP) runs.
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		RunInstall(&OnboardResult{InstallPath: filepath.Join(blocker, "sub"), MCPEnabled: true, InstallCompletions: true})
	})
	if !strings.Contains(out, "Install failed") || strings.Contains(out, "Binary installed") || strings.Contains(out, "MCP") {
		t.Errorf("failure must stop the flow:\n%s", out)
	}
}

// TestRunInstall_ServerHealthHint checks the printed health-check URL for a
// bound address and for a bare ":port". It used to prefix "localhost"
// unconditionally: curl http://localhost127.0.0.1:8081/health.
func TestRunInstall_ServerHealthHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fakeLauncher(t)

	cases := []struct{ addr, want string }{
		{"127.0.0.1:8081", "curl http://127.0.0.1:8081/health"},
		{"localhost:9000", "curl http://localhost:9000/health"},
		{":8080", "curl http://localhost:8080/health"},
	}
	for _, c := range cases {
		out := captureStdout(t, func() {
			RunInstall(&OnboardResult{InstallPath: filepath.Join(home, "bin"), ServerEnabled: true, ServerAddr: c.addr})
		})
		if !strings.Contains(out, c.want) {
			t.Errorf("addr %q: want %q in:\n%s", c.addr, c.want, out)
		}
	}
}

func TestRunUninstallFrom(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, gleannBinName())
	lib := filepath.Join(bin, sharedLibNames()[0])
	for _, f := range []string{exe, lib} {
		if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(bin, "unrelated")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Completion files that RemoveCompletions knows about.
	comp := filepath.Join(home, ".config", "fish", "completions", "gleann.fish")
	if err := os.MkdirAll(filepath.Dir(comp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(comp, []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without removeData: binary, lib and completions go, data stays.
	out := captureStdout(t, func() { runUninstallFrom([]string{bin}, false) })
	for _, p := range []string{exe, lib, comp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be removed", p)
		}
		if !strings.Contains(out, "Removed "+p) {
			t.Errorf("output should report %s:\n%s", p, out)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("unrelated files must be left alone")
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Error("data must survive without removeData")
	}

	// Nothing installed any more: says so, then removes the data on request.
	out = captureStdout(t, func() { runUninstallFrom([]string{bin}, true) })
	for _, want := range []string{"no installed binary found", "no completion files found", "Removed " + dataDir, "has been uninstalled"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Error("data directory should be gone")
	}
	out = captureStdout(t, func() { runUninstallFrom(nil, true) })
	if !strings.Contains(out, "no data directory found") {
		t.Errorf("repeat removal:\n%s", out)
	}
}

func TestResolveGleannBinPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "inst")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, gleannBinName())
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveGleannBin(&OnboardResult{InstallPath: dir}); got != bin {
		t.Errorf("installed binary must win: %q", got)
	}

	// Not installed there: found on PATH (absolute path).
	pathDir := t.TempDir()
	onPath := filepath.Join(pathDir, gleannBinName())
	if err := os.WriteFile(onPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if got := resolveGleannBin(&OnboardResult{InstallPath: filepath.Join(home, "empty")}); got != onPath {
		t.Errorf("PATH lookup: %q", got)
	}

	// Nothing on PATH: the running executable is the fallback.
	t.Setenv("PATH", t.TempDir())
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if got := resolveGleannBin(&OnboardResult{}); got != exe {
		t.Errorf("executable fallback: got %q want %q", got, exe)
	}
}

func TestInstallMCP_RepairsCorruptConfigAndKeepsOtherServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))

	claude := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claude, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := installClaudeCodeMCP("/opt/gleann"); got != claude {
		t.Fatalf("path: %q", got)
	}
	if b, err := os.ReadFile(claude + ".bak"); err != nil || string(b) != "{not json" {
		t.Errorf("corrupt config must be kept as .bak: %q %v", b, err)
	}

	// An existing, valid config keeps its other servers and keys.
	if err := os.WriteFile(claude, []byte(`{"theme":"dark","mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installClaudeCodeMCP("/opt/gleann")
	var cfg map[string]any
	data, _ := os.ReadFile(claude)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	if cfg["theme"] != "dark" || servers["other"] == nil || servers["gleann"] == nil {
		t.Errorf("existing settings lost: %s", data)
	}

	// Claude Desktop: same repair behaviour at its platform path.
	desktop := installClaudeDesktopMCP("/opt/gleann")
	if desktop == "" {
		t.Skip("no Claude Desktop path on " + runtime.GOOS)
	}
	if err := os.WriteFile(desktop, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := installClaudeDesktopMCP("/opt/gleann"); got != desktop {
		t.Errorf("desktop path: %q", got)
	}
	if _, err := os.Stat(desktop + ".bak"); err != nil {
		t.Error("corrupt desktop config must be backed up")
	}
}

func TestInstallMCP_UnwritableTargetsReturnEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory-as-file trick differs on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// ~/.claude.json is a directory: the write must fail quietly.
	if err := os.MkdirAll(filepath.Join(home, ".claude.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := installClaudeCodeMCP("/opt/gleann"); got != "" {
		t.Errorf("unwritable config must report no path, got %q", got)
	}
	// ~/.config is a file: the desktop config directory cannot be created.
	if err := os.WriteFile(filepath.Join(home, ".config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := installClaudeDesktopMCP("/opt/gleann"); got != "" && runtime.GOOS == "linux" {
		t.Errorf("uncreatable directory must report no path, got %q", got)
	}
}
