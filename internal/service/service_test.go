package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGleannDir(t *testing.T) {
	d := gleannDir()
	if d == "" {
		t.Fatal("expected non-empty gleannDir")
	}
	if !strings.HasSuffix(d, ".gleann") {
		t.Errorf("expected path to end with .gleann, got %s", d)
	}
}

func TestPidFile(t *testing.T) {
	p := pidFile()
	if !strings.HasSuffix(p, "server.pid") {
		t.Errorf("expected path to end with server.pid, got %s", p)
	}
	if !strings.Contains(p, ".gleann") {
		t.Errorf("expected path to contain .gleann, got %s", p)
	}
}

func TestLogFile(t *testing.T) {
	l := logFile()
	if !strings.HasSuffix(l, "server.log") {
		t.Errorf("expected path to end with server.log, got %s", l)
	}
}

func TestGetStatus_NoPidFile(t *testing.T) {
	// Override HOME to a temp dir so no PID file exists.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	st := GetStatus()
	if st.Running {
		t.Error("expected Running=false when no PID file exists")
	}
	if st.Platform != runtime.GOOS {
		t.Errorf("expected Platform=%s, got %s", runtime.GOOS, st.Platform)
	}
}

func TestGetStatus_StalePidFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Create a PID file with a non-existent PID.
	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidPath := filepath.Join(gleannD, "server.pid")
	// Use PID 99999999 which almost certainly doesn't exist.
	os.WriteFile(pidPath, []byte(`{"pid":99999999,"addr":":8080","started":"2024-01-01T00:00:00Z"}`), 0o644)

	st := GetStatus()
	if st.Running {
		t.Error("expected Running=false for stale PID")
	}
	// Stale PID file should be cleaned up.
	if _, err := os.Stat(pidPath); err == nil {
		t.Error("expected stale PID file to be cleaned up")
	}
}

func TestGetStatus_RunningProcess(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Use our own PID (guaranteed alive).
	myPid := os.Getpid()
	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidPath := filepath.Join(gleannD, "server.pid")
	pidData := []byte(`{"pid":` + itoa(myPid) + `,"addr":":8080","started":"2024-01-01T00:00:00Z"}`)
	os.WriteFile(pidPath, pidData, 0o644)

	st := GetStatus()
	if !st.Running {
		t.Error("expected Running=true for own PID")
	}
	if st.PID != myPid {
		t.Errorf("expected PID=%d, got %d", myPid, st.PID)
	}
	if st.Addr != ":8080" {
		t.Errorf("expected Addr=:8080, got %s", st.Addr)
	}
}

func TestFormatStatus_Running(t *testing.T) {
	st := Status{
		Running:   true,
		PID:       12345,
		Addr:      ":8080",
		Uptime:    "5m30s",
		Platform:  "linux",
		Installed: true,
	}

	s := FormatStatus(st)
	if !strings.Contains(s, "running") {
		t.Error("expected 'running' in status output")
	}
	if !strings.Contains(s, "12345") {
		t.Error("expected PID in status output")
	}
	if !strings.Contains(s, ":8080") {
		t.Error("expected address in status output")
	}
	if !strings.Contains(s, "installed") {
		t.Error("expected 'installed' in status output")
	}
}

func TestFormatStatus_Stopped(t *testing.T) {
	st := Status{
		Running:  false,
		Platform: "linux",
	}

	s := FormatStatus(st)
	if !strings.Contains(s, "not running") {
		t.Error("expected 'not running' in status output")
	}
	if !strings.Contains(s, "not installed") {
		t.Error("expected 'not installed' in status output")
	}
}

func TestLogs_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	_, err := Logs(10)
	if err == nil {
		t.Error("expected error when log file doesn't exist")
	}
}

func TestLogs_WithFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	logPath := filepath.Join(gleannD, "server.log")

	// Write 20 lines.
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line "+itoa(i))
	}
	os.WriteFile(logPath, []byte(strings.Join(lines, "\n")), 0o644)

	// Request last 5 lines.
	output, err := Logs(5)
	if err != nil {
		t.Fatal(err)
	}
	outLines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(outLines) > 5 {
		t.Errorf("expected at most 5 lines, got %d", len(outLines))
	}
}

func TestIsProcessAlive_OwnPid(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Error("expected own process to be alive")
	}
}

func TestIsProcessAlive_DeadPid(t *testing.T) {
	// PID 99999999 is almost certainly not running.
	if isProcessAlive(99999999) {
		t.Skip("PID 99999999 exists on this system, skipping")
	}
}

func TestSystemdUnitPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd test only on Linux")
	}
	p := systemdUnitPath()
	if !strings.Contains(p, "systemd") {
		t.Errorf("expected systemd in path, got %s", p)
	}
	if !strings.HasSuffix(p, "gleann.service") {
		t.Errorf("expected path to end with gleann.service, got %s", p)
	}
}

func TestStop_NotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	err := Stop()
	if err == nil {
		t.Error("expected error when stopping non-running service")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' in error, got %s", err.Error())
	}
}

// itoa avoids importing strconv for a tiny helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
