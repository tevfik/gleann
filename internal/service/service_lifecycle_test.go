//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBin writes an executable shell script into dir and returns its path.
func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// pathWithRecorder installs fake system tools (systemctl, launchctl, schtasks)
// that append their argv to a log file, and puts them first on PATH. It
// returns the log path.
func pathWithRecorder(t *testing.T, tools ...string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	for _, tool := range tools {
		fakeBin(t, bin, tool, `echo "`+tool+` $*" >> "`+logPath+`"`)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestStartThenStop_ManagesDetachedProcess(t *testing.T) {
	home := setHome(t)
	// The fake server ignores its args and just stays alive until signalled.
	bin := fakeBin(t, t.TempDir(), "gleann", `exec sleep 60`)

	if err := Start(bin, "9191"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := GetStatus()
	if !st.Running || st.PID == 0 {
		t.Fatalf("expected running server after Start, got %+v", st)
	}
	if st.Addr != ":9191" {
		t.Errorf("bare port should be normalised to :9191, got %q", st.Addr)
	}
	if _, err := os.Stat(filepath.Join(home, ".gleann", "server.log")); err != nil {
		t.Errorf("log file not created: %v", err)
	}

	// A second Start must be refused while the first is alive.
	if err := Start(bin, ""); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Start: want 'already running', got %v", err)
	}

	pid := st.PID
	if err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for isProcessAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		t.Errorf("process %d still alive after Stop", pid)
	}
	if _, err := os.Stat(pidFile()); !os.IsNotExist(err) {
		t.Errorf("pid file should be removed after Stop, stat err=%v", err)
	}
	if st := GetStatus(); st.Running {
		t.Errorf("status after Stop = %+v, want not running", st)
	}
}

func TestStart_BinaryMissing(t *testing.T) {
	setHome(t)
	err := Start(filepath.Join(t.TempDir(), "does-not-exist"), ":1")
	if err == nil || !strings.Contains(err.Error(), "failed to start server") {
		t.Fatalf("want 'failed to start server', got %v", err)
	}
	if _, statErr := os.Stat(pidFile()); !os.IsNotExist(statErr) {
		t.Error("pid file must not be written when the process failed to start")
	}
}

func TestStart_LogFileUnopenable(t *testing.T) {
	home := setHome(t)
	// server.log is a directory, so OpenFile for append fails.
	if err := os.MkdirAll(filepath.Join(home, ".gleann", "server.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Start("/bin/true", ":1")
	if err == nil || !strings.Contains(err.Error(), "cannot open log file") {
		t.Fatalf("want 'cannot open log file', got %v", err)
	}
}

func TestStart_PidFileWriteFailureReported(t *testing.T) {
	home := setHome(t)
	// server.pid is a directory: WriteFile fails after the process started.
	if err := os.MkdirAll(filepath.Join(home, ".gleann", "server.pid"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Start("/bin/true", ":1")
	if err == nil || !strings.Contains(err.Error(), "PID file write failed") {
		t.Fatalf("want PID file warning error, got %v", err)
	}
}

func TestStop_NotRunningWithStalePidFile(t *testing.T) {
	home := setHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Beyond any realistic pid_max, so the liveness probe fails.
	if err := os.WriteFile(pidFile(), []byte(`{"pid":2147483000,"addr":":1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Stop()
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("want 'not running', got %v", err)
	}
	if _, statErr := os.Stat(pidFile()); !os.IsNotExist(statErr) {
		t.Error("stale pid file should be cleaned up")
	}
}

func TestDefaultAddr_ConfigFile(t *testing.T) {
	cases := []struct {
		name, cfg, want string
	}{
		{"port only gets colon", `{"server_addr":"9000"}`, ":9000"},
		{"host:port kept and trimmed", `{"server_addr":" 127.0.0.1:9001 "}`, "127.0.0.1:9001"},
		{"empty value falls back", `{"server_addr":""}`, ":8080"},
		{"malformed json falls back", `{not json`, ":8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := setHome(t)
			if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte(tc.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := DefaultAddr(); got != tc.want {
				t.Errorf("DefaultAddr() = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("no config file", func(t *testing.T) {
		setHome(t)
		if got := DefaultAddr(); got != ":8080" {
			t.Errorf("DefaultAddr() = %q, want :8080", got)
		}
	})
}

func TestInstallSystemd_InvokesSystemctlUser(t *testing.T) {
	setHome(t)
	calls := pathWithRecorder(t, "systemctl")

	if err := installSystemd("/opt/gleann", ":7000"); err != nil {
		t.Fatalf("installSystemd: %v", err)
	}
	if err := uninstallSystemd(); err != nil {
		t.Fatalf("uninstallSystemd: %v", err)
	}
	data, _ := os.ReadFile(calls)
	got := string(data)
	for _, want := range []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable gleann.service",
		"systemctl --user stop gleann.service",
		"systemctl --user disable gleann.service",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing systemctl call %q in:\n%s", want, got)
		}
	}
}

func TestInstallSystemd_UnwritableConfigDir(t *testing.T) {
	home := setHome(t)
	// ~/.config is a regular file so the unit directory cannot be created.
	if err := os.WriteFile(filepath.Join(home, ".config"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installSystemd("/opt/gleann", ":1"); err == nil {
		t.Error("expected error when unit dir cannot be created")
	}
}

func TestInstallLaunchd_UnwritableDir(t *testing.T) {
	home := setHome(t)
	if err := os.WriteFile(filepath.Join(home, "Library"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installLaunchd("/opt/gleann", ":1"); err == nil {
		t.Error("expected error when LaunchAgents dir cannot be created")
	}
}

func TestUninstallLaunchd_UnloadsAndRemovesPlist(t *testing.T) {
	setHome(t)
	calls := pathWithRecorder(t, "launchctl")
	if err := installLaunchd("/opt/gleann", ":1"); err != nil {
		t.Fatal(err)
	}
	if err := uninstallLaunchd(); err != nil {
		t.Fatal(err)
	}
	if launchdInstalled() {
		t.Error("plist should be removed by uninstallLaunchd")
	}
	data, _ := os.ReadFile(calls)
	if !strings.Contains(string(data), "launchctl unload "+launchdPlistPath()) {
		t.Errorf("expected launchctl unload call, got %q", data)
	}
}

func TestTaskScheduler_CommandLines(t *testing.T) {
	calls := pathWithRecorder(t, "schtasks")

	if err := installTaskScheduler(`C:\gleann.exe`, ":9"); err != nil {
		t.Fatalf("installTaskScheduler: %v", err)
	}
	if err := uninstallTaskScheduler(); err != nil {
		t.Fatalf("uninstallTaskScheduler: %v", err)
	}
	data, _ := os.ReadFile(calls)
	got := string(data)
	for _, want := range []string{"/create", "/tn GleannServer", "/sc ONLOGON", "serve --addr :9 --quiet", "/delete /tn GleannServer /f"} {
		if !strings.Contains(got, want) {
			t.Errorf("schtasks calls missing %q:\n%s", want, got)
		}
	}
}

func TestTaskSchedulerInstalled_ParsesQueryOutput(t *testing.T) {
	bin := t.TempDir()
	fakeBin(t, bin, "schtasks", `echo '"GleannServer","N/A","Ready"'`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if !taskSchedulerInstalled() {
		t.Error("expected installed=true when schtasks output names the task")
	}

	bin2 := t.TempDir()
	fakeBin(t, bin2, "schtasks", `echo "ERROR: not found" >&2; exit 1`)
	t.Setenv("PATH", bin2+string(os.PathListSeparator)+os.Getenv("PATH"))
	if taskSchedulerInstalled() {
		t.Error("expected installed=false when schtasks fails")
	}
}
