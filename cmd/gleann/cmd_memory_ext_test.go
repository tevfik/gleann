package main

import (
	"testing"
	"time"
)

// ── truncateStr ────────────────────────────────

func TestTruncateStrShort(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateStrLong(t *testing.T) {
	got := truncateStr("this is a really long string", 15)
	if len(got) > 15 {
		t.Errorf("too long: %q", got)
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("should end with ...: %q", got)
	}
}

func TestTruncateStrNewlines(t *testing.T) {
	got := truncateStr("line1\nline2\nline3", 100)
	if got != "line1 line2 line3" {
		t.Errorf("newlines should be replaced: %q", got)
	}
}

func TestTruncateStrExact(t *testing.T) {
	got := truncateStr("12345", 5)
	if got != "12345" {
		t.Errorf("expected exact: %q", got)
	}
}

// ── formatMemAge ───────────────────────────────

func TestFormatMemAgeSeconds(t *testing.T) {
	got := formatMemAge(30 * time.Second)
	if got != "30s" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMemAgeMinutes(t *testing.T) {
	got := formatMemAge(5 * time.Minute)
	if got != "5m" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMemAgeHours(t *testing.T) {
	got := formatMemAge(3 * time.Hour)
	if got != "3h" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMemAgeDays(t *testing.T) {
	got := formatMemAge(48 * time.Hour)
	if got != "2d" {
		t.Errorf("got %q", got)
	}
}

// ── formatMemSize ──────────────────────────────

func TestFormatMemSizeBytes(t *testing.T) {
	got := formatMemSize(512)
	if got != "512 B" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMemSizeKB(t *testing.T) {
	got := formatMemSize(2048)
	if got != "2.0 KB" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMemSizeMB(t *testing.T) {
	got := formatMemSize(5 * 1024 * 1024)
	if got != "5.0 MB" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMemSizeGB(t *testing.T) {
	got := formatMemSize(2 * 1024 * 1024 * 1024)
	if got != "2.0 GB" {
		t.Errorf("got %q", got)
	}
}

// ── cmdMemory dispatch ─────────────────────────

func TestCmdMemoryHelp(t *testing.T) {
	// Should not panic. help/--help/-h subcommands just print.
	cmdMemory([]string{"help"})
	cmdMemory([]string{"--help"})
	cmdMemory([]string{"-h"})
}

func TestCmdMemoryNoArgs(t *testing.T) {
	// Calls printMemoryUsage, should not panic.
	cmdMemory([]string{})
}
