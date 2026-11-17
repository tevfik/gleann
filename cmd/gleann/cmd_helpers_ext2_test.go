package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/conversations"
)

// ── formatSize ─────────────────────────────────────────────────

func TestFormatSizeBytes(t *testing.T) {
	if got := formatSize(42); got != "42 B" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSizeKB(t *testing.T) {
	if got := formatSize(2048); got != "2.0 KB" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSizeMB(t *testing.T) {
	if got := formatSize(5 * 1024 * 1024); got != "5.0 MB" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSizeGB(t *testing.T) {
	if got := formatSize(3 * 1024 * 1024 * 1024); got != "3.0 GB" {
		t.Errorf("got %q", got)
	}
}

// ── getFlag ────────────────────────────────────────────────────

func TestGetFlagPresent(t *testing.T) {
	args := []string{"--host", "localhost:11434", "--model", "bge-m3"}
	if got := getFlag(args, "--model"); got != "bge-m3" {
		t.Errorf("got %q", got)
	}
}

func TestGetFlagMissing(t *testing.T) {
	args := []string{"--host", "localhost"}
	if got := getFlag(args, "--model"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestGetFlagNoValue(t *testing.T) {
	args := []string{"--model"}
	if got := getFlag(args, "--model"); got != "" {
		t.Errorf("got %q, want empty (no value after flag)", got)
	}
}

// ── hasFlag ────────────────────────────────────────────────────

func TestHasFlagPresent(t *testing.T) {
	args := []string{"--graph", "--docs", "/tmp"}
	if !hasFlag(args, "--graph") {
		t.Error("expected --graph to be present")
	}
}

func TestHasFlagMissing(t *testing.T) {
	args := []string{"--docs", "/tmp"}
	if hasFlag(args, "--graph") {
		t.Error("expected --graph to be missing")
	}
}

func TestHasFlagEmpty(t *testing.T) {
	if hasFlag(nil, "--anything") {
		t.Error("nil args should have no flags")
	}
}

// ── truncate ───────────────────────────────────────────────────

func TestTruncateExt2Short(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateExt2Long(t *testing.T) {
	s := strings.Repeat("a", 50)
	got := truncate(s, 20)
	if len(got) != 20 || !strings.HasSuffix(got, "...") {
		t.Errorf("got %q (len=%d)", got, len(got))
	}
}

func TestTruncateExt2Newlines(t *testing.T) {
	s := "line1\nline2\nline3"
	got := truncate(s, 100)
	if strings.Contains(got, "\n") {
		t.Error("newlines should be removed")
	}
}

// ── formatAge ──────────────────────────────────────────────────

func TestFormatAgeExt2Seconds(t *testing.T) {
	if got := formatAge(30 * time.Second); got != "30s" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgeExt2Minutes(t *testing.T) {
	if got := formatAge(5 * time.Minute); got != "5m" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgeExt2Hours(t *testing.T) {
	if got := formatAge(3 * time.Hour); got != "3h" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgeExt2Days(t *testing.T) {
	if got := formatAge(48 * time.Hour); got != "2d" {
		t.Errorf("got %q", got)
	}
}

// ── parseFriendlyDuration ──────────────────────────────────────

func TestParseFriendlyDurationExt2Days(t *testing.T) {
	d, err := parseFriendlyDuration("7d")
	if err != nil {
		t.Fatal(err)
	}
	if d != 7*24*time.Hour {
		t.Errorf("got %v", d)
	}
}

func TestParseFriendlyDurationExt2Weeks(t *testing.T) {
	d, err := parseFriendlyDuration("2w")
	if err != nil {
		t.Fatal(err)
	}
	if d != 14*24*time.Hour {
		t.Errorf("got %v", d)
	}
}

func TestParseFriendlyDurationExt2Invalid(t *testing.T) {
	_, err := parseFriendlyDuration("5x")
	if err == nil {
		t.Error("expected error for unknown unit")
	}
}

func TestParseFriendlyDurationExt2UpperCase(t *testing.T) {
	d, err := parseFriendlyDuration("3D")
	if err != nil {
		t.Fatal(err)
	}
	if d != 3*24*time.Hour {
		t.Errorf("got %v", d)
	}
}

// ── getDeleteArgs ──────────────────────────────────────────────

func TestGetDeleteArgsExt2(t *testing.T) {
	args := []string{"--delete", "abc123", "--delete", "def456"}
	got := getDeleteArgs(args)
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	if got[0] != "abc123" || got[1] != "def456" {
		t.Errorf("got %v", got)
	}
}

func TestGetDeleteArgsExt2None(t *testing.T) {
	args := []string{"--list"}
	got := getDeleteArgs(args)
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestGetDeleteArgsExt2Trailing(t *testing.T) {
	args := []string{"--delete"} // last flag, no value
	got := getDeleteArgs(args)
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// ── pickerModel ────────────────────────────────────────────────

func TestNewPickerModel(t *testing.T) {
	m := newPickerModel(nil)
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestPickerModelView(t *testing.T) {
	m := newPickerModel(nil)
	v := m.View()
	if v.Content == "" {
		t.Error("View should produce output")
	}
}

// ── formatBytes ────────────────────────────────────────────────

func TestFormatBytesSmall(t *testing.T) {
	if got := formatBytes(500); got != "500 B" {
		t.Errorf("got %q", got)
	}
}

func TestFormatBytesKB(t *testing.T) {
	got := formatBytes(2048)
	if !strings.Contains(got, "KB") {
		t.Errorf("got %q, want KB", got)
	}
}

func TestFormatBytesMB(t *testing.T) {
	got := formatBytes(5 * 1024 * 1024)
	if !strings.Contains(got, "MB") {
		t.Errorf("got %q, want MB", got)
	}
}

func TestFormatBytesGB(t *testing.T) {
	got := formatBytes(3 * 1024 * 1024 * 1024)
	if !strings.Contains(got, "GB") {
		t.Errorf("got %q, want GB", got)
	}
}

// ── checkDiskSpace ─────────────────────────────────────────────

func TestCheckDiskSpaceEmptyDir(t *testing.T) {
	tmp := t.TempDir()
	var msgs []string
	okFn := func(msg string) { msgs = append(msgs, "ok: "+msg) }
	warnFn := func(msg string) { msgs = append(msgs, "warn: "+msg) }

	checkDiskSpace(tmp, okFn, warnFn)
	if len(msgs) == 0 {
		t.Error("should produce at least one message")
	}
}

func TestCheckDiskSpaceWithFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "data.bin"), make([]byte, 1024), 0o644)

	var msgs []string
	okFn := func(msg string) { msgs = append(msgs, msg) }
	warnFn := func(msg string) { msgs = append(msgs, msg) }

	checkDiskSpace(tmp, okFn, warnFn)
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "storage") || strings.Contains(m, "used") {
			found = true
		}
	}
	if !found {
		t.Errorf("messages = %v", msgs)
	}
}

func TestCheckDiskSpaceNonexistent(t *testing.T) {
	var msgs []string
	okFn := func(msg string) { msgs = append(msgs, msg) }
	warnFn := func(msg string) { msgs = append(msgs, msg) }

	checkDiskSpace("/nonexistent/path/deep", okFn, warnFn)
	// Should not panic, may produce a warning or ok about root.
}

// ── configFilePath ─────────────────────────────────────────────

func TestConfigFilePathExt2(t *testing.T) {
	p := configFilePath()
	if p == "" {
		t.Error("should not be empty")
	}
	if !strings.Contains(p, "config.json") {
		t.Errorf("path = %q", p)
	}
}

// ── pickerModel Update ─────────────────────────────────────────

func TestPickerModelUpdateQuit(t *testing.T) {
	m := newPickerModel(nil)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'q'})
	pm := updated.(pickerModel)
	if !pm.quitting {
		t.Error("q should quit")
	}
}

func TestPickerModelUpdateNav(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "1", Title: "Conv1"},
		{ID: "2", Title: "Conv2"},
	}
	m := newPickerModel(items)

	// Down.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := updated.(pickerModel)
	if pm.cursor != 1 {
		t.Errorf("cursor = %d, want 1", pm.cursor)
	}

	// Up.
	updated, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = updated.(pickerModel)
	if pm.cursor != 0 {
		t.Errorf("cursor = %d, want 0", pm.cursor)
	}
}

func TestPickerModelUpdateEnter(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "abc", Title: "Test"},
	}
	m := newPickerModel(items)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm := updated.(pickerModel)
	if pm.selected == nil {
		t.Error("enter should set selected")
	}
}

func TestPickerModelViewWithItems(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "1", Title: "My Chat"},
	}
	m := newPickerModel(items)
	v := m.View()
	if !strings.Contains(v.Content, "My Chat") {
		t.Error("should show conversation title")
	}
}
