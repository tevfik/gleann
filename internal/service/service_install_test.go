package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInstallSystemd_WritesUnit verifies the unit file is written with the
// expected ExecStart line on Linux. systemctl invocations are best-effort
// (errors are ignored), so this works inside CI without a real systemd.
func TestInstallSystemd_WritesUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd test only on Linux")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := installSystemd("/usr/local/bin/gleann", ":7777"); err != nil {
		t.Fatalf("installSystemd: %v", err)
	}
	if !systemdInstalled() {
		t.Fatal("systemdInstalled() should be true after installSystemd")
	}
	data, err := os.ReadFile(systemdUnitPath())
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	body := string(data)
	for _, want := range []string{"/usr/local/bin/gleann", ":7777", "Description=", "WantedBy=default.target"} {
		if !strings.Contains(body, want) {
			t.Errorf("unit file missing %q\n%s", want, body)
		}
	}

	// uninstall removes the file even if systemctl is missing.
	if err := uninstallSystemd(); err != nil {
		t.Fatalf("uninstallSystemd: %v", err)
	}
	if systemdInstalled() {
		t.Error("expected unit file to be removed")
	}
}

// TestInstallLaunchd_WritesPlist verifies the plist body on macOS or any
// platform (file write is platform-agnostic).
func TestInstallLaunchd_WritesPlist(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := installLaunchd("/opt/gleann", ":9090"); err != nil {
		t.Fatalf("installLaunchd: %v", err)
	}
	if !launchdInstalled() {
		t.Fatal("launchdInstalled() should be true")
	}
	data, err := os.ReadFile(launchdPlistPath())
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	body := string(data)
	for _, want := range []string{"/opt/gleann", ":9090", "<key>Label</key>", "com.gleann.server"} {
		if !strings.Contains(body, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	// uninstallLaunchd removes the plist (launchctl errors are ignored).
	if err := uninstallLaunchd(); err != nil {
		t.Fatalf("uninstallLaunchd: %v", err)
	}
	if launchdInstalled() {
		t.Error("expected plist to be removed")
	}
}

// TestInstall_DispatchesByOS exercises the cross-platform Install/Uninstall
// dispatcher. Windows is skipped (requires schtasks). Errors from schtasks
// on Linux are NOT exercised by this test.
func TestInstall_DispatchesByOS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("schtasks not available in CI for the Windows branch")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Install("/tmp/fake-gleann", ":8181"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Validate side effect by platform.
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat(systemdUnitPath()); err != nil {
			t.Fatalf("expected systemd unit to exist: %v", err)
		}
	case "darwin":
		if _, err := os.Stat(launchdPlistPath()); err != nil {
			t.Fatalf("expected plist to exist: %v", err)
		}
	}

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
}

// TestInstall_DefaultsBinaryAndAddr exercises the default-resolution branches
// of Install (empty bin → os.Executable, empty addr → :8080).
func TestInstall_DefaultsBinaryAndAddr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Install("", ""); err != nil {
		t.Fatalf("Install with empty args: %v", err)
	}

	switch runtime.GOOS {
	case "linux":
		data, _ := os.ReadFile(systemdUnitPath())
		if !strings.Contains(string(data), ":8080") {
			t.Error("default addr :8080 missing from unit")
		}
	case "darwin":
		data, _ := os.ReadFile(launchdPlistPath())
		if !strings.Contains(string(data), ":8080") {
			t.Error("default addr :8080 missing from plist")
		}
	}
	_ = Uninstall()
}

// TestStart_DefaultsAndAlreadyRunning hits the early "already running" branch
// of Start by writing a PID file pointing at our own (live) process.
func TestStart_DefaultsAndAlreadyRunning(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	gleannD := filepath.Join(tmp, ".gleann")
	if err := os.MkdirAll(gleannD, 0o755); err != nil {
		t.Fatal(err)
	}
	pidJSON := []byte(`{"pid":` + itoa(os.Getpid()) + `,"addr":":8080","started":"2024-01-01T00:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(gleannD, "server.pid"), pidJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	err := Start("", "")
	if err == nil {
		t.Fatal("expected 'already running' error")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected 'already running' in error, got %v", err)
	}
}

// TestTaskSchedulerInstalled_SafeWhenAbsent ensures the Windows probe doesn't
// panic on non-Windows systems (it shells out and returns false on error).
func TestTaskSchedulerInstalled_SafeWhenAbsent(t *testing.T) {
	if taskSchedulerInstalled() && runtime.GOOS != "windows" {
		t.Errorf("schtasks probe should be false off-Windows, got true")
	}
}

// TestGleannDir_HomeOverride verifies HOME env override takes effect.
func TestGleannDir_HomeOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	got := gleannDir()
	if !strings.HasPrefix(got, tmp) {
		t.Errorf("expected gleannDir to start with %s, got %s", tmp, got)
	}
}
