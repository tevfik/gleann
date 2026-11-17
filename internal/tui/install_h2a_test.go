package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// h2aLauncher points GLEANN_LAUNCHER at a small stand-in binary, so the
// install tests copy a few bytes instead of the test executable.
func h2aLauncher(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gleann-launcher")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_LAUNCHER", p)
	return p
}

func h2aBinName() string {
	if runtime.GOOS == "windows" {
		return "gleann.exe"
	}
	return "gleann"
}

func TestInstallBinaryH2AErrors(t *testing.T) {
	h2aLauncher(t, "launcher")

	// The target directory cannot be created under a file.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallBinary(filepath.Join(file, "bin")); err == nil || !strings.Contains(err.Error(), "create target dir") {
		t.Errorf("target under a file: %v", err)
	}

	// The temporary file cannot be created (a directory has its name).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, h2aBinName()+".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallBinary(dir); err == nil || !strings.Contains(err.Error(), "create temp file") {
		t.Errorf("temp file blocked: %v", err)
	}

	// The final name is a non-empty directory: the rename fails and the
	// temporary copy is removed.
	dir = t.TempDir()
	dst := filepath.Join(dir, h2aBinName())
	if err := os.MkdirAll(filepath.Join(dst, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallBinary(dir); err == nil || !strings.Contains(err.Error(), "install binary") {
		t.Errorf("rename onto a directory: %v", err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary copy left behind: %v", err)
	}
}

// Installing the launcher onto itself is a no-op that keeps the file.
func TestInstallBinaryH2AOntoItself(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, h2aBinName())
	if err := os.WriteFile(self, []byte("me"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_LAUNCHER", self)
	if err := InstallBinary(dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(self); err != nil || string(b) != "me" {
		t.Errorf("binary changed: %q %v", b, err)
	}
	if _, err := os.Stat(self + ".tmp"); !os.IsNotExist(err) {
		t.Error("a temporary copy was created")
	}
}

// The rc file is a directory: nothing can be appended, and nothing breaks.
func TestEnsureSourceLineH2ARcIsDirectory(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.MkdirAll(rc, 0o755); err != nil {
		t.Fatal(err)
	}
	ensureSourceLine(rc, "/x/gleann")
	if fi, err := os.Stat(rc); err != nil || !fi.IsDir() {
		t.Errorf("rc directory changed: %v", err)
	}
}

// RunInstall reports an install failure and stops there.
func TestRunInstallH2AInstallFailure(t *testing.T) {
	h2aLauncher(t, "launcher")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, h2aBinName()+".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		RunInstall(&OnboardResult{InstallPath: dir, InstallCompletions: true, MCPEnabled: true})
	})
	if !strings.Contains(out, "Install failed") || strings.Contains(out, "Binary installed") || strings.Contains(out, "completions") {
		t.Errorf("output:\n%s", out)
	}
}

// When no completion directory and no editor config can be written,
// RunInstall says so instead of listing nothing.
func TestRunInstallH2ANothingWritable(t *testing.T) {
	launcher := h2aLauncher(t, "launcher")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", "")
	for _, f := range []string{".local", ".config", "Library"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "bin")
	out := captureStdout(t, func() {
		RunInstall(&OnboardResult{InstallPath: target, InstallCompletions: true, MCPEnabled: true})
	})
	for _, want := range []string{"Binary installed", "(no completions installed)", "No supported editors detected"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if b, err := os.ReadFile(filepath.Join(target, h2aBinName())); err != nil || string(b) != "launcher" {
		t.Errorf("installed binary: %q %v (from %s)", b, err, launcher)
	}
}

// A binary that cannot be removed is reported, and the run goes on.
func TestRunUninstallFromH2ARemoveFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	// A non-empty directory under the binary's name cannot be removed.
	if err := os.MkdirAll(filepath.Join(dir, h2aBinName(), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runUninstallFrom([]string{dir}, false) })
	if !strings.Contains(out, "Failed to remove") || !strings.Contains(out, "no installed binary found") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(out, "gleann has been uninstalled") {
		t.Errorf("uninstall stopped early:\n%s", out)
	}
}

// Without a home directory no editor config is written.
func TestInstallMCPH2ANoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if got := installClaudeCodeMCP("/opt/gleann"); got != "" {
		t.Errorf("claude code: %q", got)
	}
	if got := installClaudeDesktopMCP("/opt/gleann"); got != "" {
		t.Errorf("claude desktop: %q", got)
	}
}

// The desktop config path is a directory: the write fails quietly.
func TestInstallClaudeDesktopMCPH2AWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	var cfg string
	switch runtime.GOOS {
	case "darwin":
		cfg = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		cfg = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	case "linux":
		cfg = filepath.Join(home, ".config", "claude", "claude_desktop_config.json")
	default:
		t.Skip("no Claude Desktop on " + runtime.GOOS)
	}
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := installClaudeDesktopMCP("/opt/gleann"); got != "" {
		t.Errorf("got %q", got)
	}
	if fi, err := os.Stat(cfg); err != nil || !fi.IsDir() {
		t.Errorf("config directory replaced: %v", err)
	}
}

// A directory that exists but does not accept new files is not writable.
func TestIsWritableH2AReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not make directories read-only on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.MkdirAll(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	if isWritable(ro) || isWritable(filepath.Join(ro, "missing", "bin")) {
		t.Error("read-only directory reported writable")
	}
}
