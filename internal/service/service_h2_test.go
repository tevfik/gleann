package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeServiceTools puts no-op systemctl and launchctl scripts first on PATH so
// that Install/Uninstall in tests never reach the developer's real user
// service manager (uninstallSystemd would otherwise stop and disable a real
// gleann.service). Windows tests do not run these code paths.
func fakeServiceTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	bin := t.TempDir()
	for _, tool := range []string{"systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func h2Home(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestInstall_BarePortGetsColon(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checks the systemd unit")
	}
	h2Home(t)
	fakeServiceTools(t)
	if err := Install("/opt/gleann", "9090"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(systemdUnitPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--addr :9090 ") {
		t.Fatalf("bare port not normalised in unit:\n%s", data)
	}
}

func TestInstallSystemd_UnitPathIsDirectory(t *testing.T) {
	h2Home(t)
	fakeServiceTools(t)
	if err := os.MkdirAll(filepath.Join(systemdUnitPath(), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installSystemd("/opt/gleann", ":1"); err == nil {
		t.Fatal("writing the unit over a directory must fail")
	}
}

func TestGetStatus_CorruptPidFile(t *testing.T) {
	home := h2Home(t)
	dir := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.pid"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := GetStatus()
	if st.Running || st.PID != 0 {
		t.Fatalf("corrupt pid file must read as not running, got %+v", st)
	}
}

func TestLogs_DefaultLineCount(t *testing.T) {
	home := h2Home(t)
	dir := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, "line "+itoa(i))
	}
	if err := os.WriteFile(filepath.Join(dir, "server.log"), []byte(strings.Join(lines, "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Logs(0)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(out, "\n")
	if len(got) != 50 || got[0] != "line 30" || got[49] != "line 79" {
		t.Fatalf("Logs(0) returned %d lines, first %q", len(got), got[0])
	}
}

// A server that ignores the interrupt is killed after the grace period.
func TestStop_ForceKillsServerIgnoringInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the server")
	}
	if testing.Short() {
		t.Skip("waits for the 5 s stop grace period")
	}
	h2Home(t)
	bin := filepath.Join(t.TempDir(), "gleann")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntrap '' INT\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Start(bin, ":1"); err != nil {
		t.Fatal(err)
	}
	st := GetStatus()
	if !st.Running {
		t.Fatalf("server not running after Start: %+v", st)
	}
	// Give the shell time to install the trap before Stop interrupts it.
	time.Sleep(300 * time.Millisecond)
	if err := Stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for isProcessAlive(st.PID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(st.PID) {
		t.Fatalf("process %d survived Stop", st.PID)
	}
	if _, err := os.Stat(pidFile()); !os.IsNotExist(err) {
		t.Fatalf("pid file left behind: %v", err)
	}
}
