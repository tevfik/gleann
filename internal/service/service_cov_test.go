package service

import (
	"strings"
	"testing"
)

func TestFormatStatus_RunningNoUptime(t *testing.T) {
	st := Status{
		Running:   true,
		PID:       999,
		Addr:      ":8080",
		Platform:  "linux-systemd",
		Installed: true,
	}
	out := FormatStatus(st)
	if !strings.Contains(out, "PID 999") {
		t.Error("missing PID")
	}
	// Uptime should not appear if empty
	if strings.Contains(out, "Uptime") {
		t.Error("uptime should be absent when empty")
	}
}

func TestLaunchdPlistPath(t *testing.T) {
	p := launchdPlistPath()
	if p == "" {
		t.Fatal("expected non-empty path")
	}
	if !strings.HasSuffix(p, "com.gleann.server.plist") {
		t.Errorf("unexpected plist path: %s", p)
	}
	if !strings.Contains(p, "LaunchAgents") {
		t.Errorf("expected LaunchAgents in path: %s", p)
	}
}

func TestSysProcAttr(t *testing.T) {
	// sysProcAttr should not panic
	attr := sysProcAttr()
	_ = attr
}
