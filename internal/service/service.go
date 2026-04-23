// Package service provides cross-platform service management for gleann.
// It supports systemd (Linux), launchd (macOS), Task Scheduler (Windows),
// and a fallback PID-file mechanism.
package service

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Status represents the current state of the gleann service.
type Status struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Addr      string `json:"addr,omitempty"`
	Uptime    string `json:"uptime,omitempty"`
	Platform  string `json:"platform"`
	Installed bool   `json:"installed"`
}

// gleannDir returns the base directory for gleann data.
func gleannDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann")
}

// pidFile returns the path to the PID file.
func pidFile() string {
	return filepath.Join(gleannDir(), "server.pid")
}

// logFile returns the path to the service log file.
func logFile() string {
	return filepath.Join(gleannDir(), "server.log")
}

// Install creates the platform-appropriate service configuration.
func Install(gleannBin, addr string) error {
	if gleannBin == "" {
		var err error
		gleannBin, err = os.Executable()
		if err != nil {
			gleannBin = "gleann"
		}
	}
	if addr == "" {
		addr = ":8080"
	}

	switch runtime.GOOS {
	case "linux":
		return installSystemd(gleannBin, addr)
	case "darwin":
		return installLaunchd(gleannBin, addr)
	case "windows":
		return installTaskScheduler(gleannBin, addr)
	default:
		return fmt.Errorf("unsupported platform: %s (use 'gleann service start' for manual mode)", runtime.GOOS)
	}
}

// Uninstall removes the platform-appropriate service configuration.
func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		return uninstallSystemd()
	case "darwin":
		return uninstallLaunchd()
	case "windows":
		return uninstallTaskScheduler()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// Start starts the gleann server as a background process.
func Start(gleannBin, addr string) error {
	if gleannBin == "" {
		var err error
		gleannBin, err = os.Executable()
		if err != nil {
			gleannBin = "gleann"
		}
	}
	if addr == "" {
		addr = ":8080"
	}

	// Check if already running.
	if st := GetStatus(); st.Running {
		return fmt.Errorf("gleann server already running (PID %d)", st.PID)
	}

	// Open log file.
	if err := os.MkdirAll(gleannDir(), 0o755); err != nil {
		return err
	}
	logF, err := os.OpenFile(logFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open log file: %w", err)
	}

	cmd := exec.Command(gleannBin, "serve", "--addr", addr, "--quiet")
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = sysProcAttr() // platform-specific: setsid on Unix

	if err := cmd.Start(); err != nil {
		logF.Close()
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Write PID file.
	pidInfo := struct {
		PID     int    `json:"pid"`
		Addr    string `json:"addr"`
		Started string `json:"started"`
	}{
		PID:     cmd.Process.Pid,
		Addr:    addr,
		Started: time.Now().Format(time.RFC3339),
	}
	pidData, _ := json.Marshal(pidInfo)
	if err := os.WriteFile(pidFile(), pidData, 0o644); err != nil {
		return fmt.Errorf("warning: server started but PID file write failed: %w", err)
	}

	// Detach: don't wait for the child.
	go func() {
		cmd.Wait()
		logF.Close()
	}()

	return nil
}

// Stop stops the running gleann server.
func Stop() error {
	st := GetStatus()
	if !st.Running {
		// Clean up stale PID file.
		os.Remove(pidFile())
		return fmt.Errorf("gleann server is not running")
	}

	proc, err := os.FindProcess(st.PID)
	if err != nil {
		os.Remove(pidFile())
		return fmt.Errorf("cannot find process %d: %w", st.PID, err)
	}

	// Graceful shutdown: os.Interrupt works on all platforms.
	// On Unix this sends SIGINT; on Windows it calls GenerateConsoleCtrlEvent.
	if err := proc.Signal(os.Interrupt); err != nil {
		// If interrupt fails, force-kill (os.Kill is cross-platform).
		proc.Kill()
	}

	// Poll for process exit instead of Wait() (which only works for child processes).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(st.PID) {
			os.Remove(pidFile())
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Force-kill after timeout.
	proc.Kill()
	time.Sleep(500 * time.Millisecond)

	os.Remove(pidFile())
	return nil
}

// GetStatus returns the current service status.
func GetStatus() Status {
	st := Status{Platform: runtime.GOOS}

	// Check if service is installed.
	switch runtime.GOOS {
	case "linux":
		st.Installed = systemdInstalled()
	case "darwin":
		st.Installed = launchdInstalled()
	case "windows":
		st.Installed = taskSchedulerInstalled()
	}

	// Check PID file.
	data, err := os.ReadFile(pidFile())
	if err != nil {
		return st
	}

	var pidInfo struct {
		PID     int    `json:"pid"`
		Addr    string `json:"addr"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(data, &pidInfo); err != nil {
		return st
	}

	st.PID = pidInfo.PID
	st.Addr = pidInfo.Addr

	// Check if process is alive.
	if isProcessAlive(pidInfo.PID) {
		st.Running = true
		if started, err := time.Parse(time.RFC3339, pidInfo.Started); err == nil {
			st.Uptime = time.Since(started).Round(time.Second).String()
		}
	} else {
		// Stale PID file.
		os.Remove(pidFile())
	}

	return st
}

// Logs returns the last N lines of the service log.
func Logs(n int) (string, error) {
	if n <= 0 {
		n = 50
	}
	data, err := os.ReadFile(logFile())
	if err != nil {
		return "", fmt.Errorf("no log file found: %w", err)
	}
	// Normalize CRLF → LF for Windows compatibility.
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// FormatStatus returns a human-readable status string.
func FormatStatus(st Status) string {
	var sb strings.Builder
	if st.Running {
		sb.WriteString(fmt.Sprintf("● gleann server is running (PID %d)\n", st.PID))
		sb.WriteString(fmt.Sprintf("  Address: %s\n", st.Addr))
		if st.Uptime != "" {
			sb.WriteString(fmt.Sprintf("  Uptime:  %s\n", st.Uptime))
		}
	} else {
		sb.WriteString("○ gleann server is not running\n")
	}
	sb.WriteString(fmt.Sprintf("  Platform: %s\n", st.Platform))
	if st.Installed {
		sb.WriteString("  Service:  installed (auto-start on login)\n")
	} else {
		sb.WriteString("  Service:  not installed\n")
	}
	sb.WriteString(fmt.Sprintf("  Log file: %s\n", logFile()))
	return sb.String()
}

// ── Platform: Linux (systemd user service) ────────────────────────────────

func systemdUnitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "gleann.service")
}

func systemdInstalled() bool {
	_, err := os.Stat(systemdUnitPath())
	return err == nil
}

func installSystemd(gleannBin, addr string) error {
	unit := fmt.Sprintf(`[Unit]
Description=gleann server — semantic search, RAG & memory engine
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s serve --addr %s --quiet
Restart=on-failure
RestartSec=5
Environment=HOME=%%h

[Install]
WantedBy=default.target
`, gleannBin, addr)

	dir := filepath.Dir(systemdUnitPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(systemdUnitPath(), []byte(unit), 0o644); err != nil {
		return err
	}

	// Enable and start.
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	exec.Command("systemctl", "--user", "enable", "gleann.service").Run()

	return nil
}

func uninstallSystemd() error {
	exec.Command("systemctl", "--user", "stop", "gleann.service").Run()
	exec.Command("systemctl", "--user", "disable", "gleann.service").Run()
	os.Remove(systemdUnitPath())
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

// ── Platform: macOS (launchd) ─────────────────────────────────────────────

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.gleann.server.plist")
}

func launchdInstalled() bool {
	_, err := os.Stat(launchdPlistPath())
	return err == nil
}

func installLaunchd(gleannBin, addr string) error {
	logPath := logFile()
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.gleann.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
        <string>--addr</string>
        <string>%s</string>
        <string>--quiet</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, gleannBin, addr, logPath, logPath)

	dir := filepath.Dir(launchdPlistPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(launchdPlistPath(), []byte(plist), 0o644)
}

func uninstallLaunchd() error {
	exec.Command("launchctl", "unload", launchdPlistPath()).Run()
	os.Remove(launchdPlistPath())
	return nil
}

// ── Platform: Windows (Task Scheduler) ────────────────────────────────────

func taskSchedulerInstalled() bool {
	out, err := exec.Command("schtasks", "/query", "/tn", "GleannServer", "/fo", "CSV").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "GleannServer")
}

func installTaskScheduler(gleannBin, addr string) error {
	// Create a scheduled task that runs at user logon.
	return exec.Command("schtasks", "/create",
		"/tn", "GleannServer",
		"/tr", fmt.Sprintf(`"%s" serve --addr %s --quiet`, gleannBin, addr),
		"/sc", "ONLOGON",
		"/rl", "LIMITED",
		"/f",
	).Run()
}

func uninstallTaskScheduler() error {
	return exec.Command("schtasks", "/delete", "/tn", "GleannServer", "/f").Run()
}

// sysProcAttr returns platform-specific SysProcAttr for detaching child processes.
func sysProcAttr() *syscall.SysProcAttr {
	return platformSysProcAttr()
}
