package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/conversations"
)

// ── pickerModel ────────────────────────────────────────────────

func TestPickerModelInitExt(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "abc123", Title: "Test conv"},
	}
	m := newPickerModel(items)
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestPickerModelUpdateUpDownExt(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "a", Title: "First"},
		{ID: "b", Title: "Second"},
		{ID: "c", Title: "Third"},
	}
	m := newPickerModel(items)

	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := result.(pickerModel)
	if pm.cursor != 1 {
		t.Errorf("cursor = %d, want 1", pm.cursor)
	}

	result, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = result.(pickerModel)
	if pm.cursor != 0 {
		t.Errorf("cursor = %d, want 0", pm.cursor)
	}

	// Up at 0 should stay at 0.
	result, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = result.(pickerModel)
	if pm.cursor != 0 {
		t.Error("should not go below 0")
	}
}

func TestPickerModelUpdateEnterExt(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "abc", Title: "Test"},
	}
	m := newPickerModel(items)

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm := result.(pickerModel)
	if pm.selected == nil {
		t.Error("enter should select")
	}
	if pm.selected.ID != "abc" {
		t.Errorf("selected ID = %q", pm.selected.ID)
	}
	_ = cmd
}

func TestPickerModelUpdateQuitExt(t *testing.T) {
	items := []conversations.Conversation{{ID: "a"}}
	m := newPickerModel(items)

	result, _ := m.Update(tea.KeyPressMsg{Code: 'q'})
	pm := result.(pickerModel)
	if !pm.quitting {
		t.Error("q should quit")
	}
}

func TestPickerModelViewExt(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "abc123", Title: "My conversation", UpdatedAt: time.Now()},
	}
	m := newPickerModel(items)
	v := m.View()
	if !strings.Contains(v.Content, "Select a conversation") {
		t.Error("should show picker header")
	}
	if !strings.Contains(v.Content, "My conversation") {
		t.Error("should show conversation title")
	}
}

func TestPickerModelViewQuittingExt(t *testing.T) {
	m := pickerModel{quitting: true}
	v := m.View()
	if v.Content != "" {
		t.Error("quitting view should be empty")
	}
}

// ── Truncate ───────────────────────────────────────────────────

func TestTruncateShortExt(t *testing.T) {
	if truncate("hello", 20) != "hello" {
		t.Error("short string should not be truncated")
	}
}

func TestTruncateLongExt(t *testing.T) {
	s := strings.Repeat("x", 100)
	got := truncate(s, 20)
	if len(got) > 20 {
		t.Errorf("truncated length = %d, want <= 20", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("should end with ...")
	}
}

func TestTruncateNewlinesExt(t *testing.T) {
	got := truncate("hello\nworld", 20)
	if strings.Contains(got, "\n") {
		t.Error("should replace newlines")
	}
}

// ── FormatAge ──────────────────────────────────────────────────

func TestFormatAgeSecondsExt(t *testing.T) {
	got := formatAge(30 * time.Second)
	if !strings.HasSuffix(got, "s") {
		t.Errorf("got %q, want seconds", got)
	}
}

func TestFormatAgeMinutesExt(t *testing.T) {
	got := formatAge(15 * time.Minute)
	if !strings.HasSuffix(got, "m") {
		t.Errorf("got %q, want minutes", got)
	}
}

func TestFormatAgeHoursExt(t *testing.T) {
	got := formatAge(5 * time.Hour)
	if !strings.HasSuffix(got, "h") {
		t.Errorf("got %q, want hours", got)
	}
}

func TestFormatAgeDaysExt(t *testing.T) {
	got := formatAge(72 * time.Hour)
	if !strings.HasSuffix(got, "d") {
		t.Errorf("got %q, want days", got)
	}
}

// ── ParseFriendlyDuration ──────────────────────────────────────

func TestParseFriendlyDurationDaysExt(t *testing.T) {
	d, err := parseFriendlyDuration("7d")
	if err != nil {
		t.Fatal(err)
	}
	if d != 7*24*time.Hour {
		t.Errorf("got %v, want 7 days", d)
	}
}

func TestParseFriendlyDurationWeeksExt(t *testing.T) {
	d, err := parseFriendlyDuration("2w")
	if err != nil {
		t.Fatal(err)
	}
	if d != 14*24*time.Hour {
		t.Errorf("got %v, want 14 days", d)
	}
}

func TestParseFriendlyDurationInvalidExt(t *testing.T) {
	_, err := parseFriendlyDuration("abc")
	if err == nil {
		t.Error("should return error for invalid duration")
	}
}

func TestParseFriendlyDurationUpperExt(t *testing.T) {
	d, err := parseFriendlyDuration("30D")
	if err != nil {
		t.Fatal(err)
	}
	if d != 30*24*time.Hour {
		t.Errorf("got %v, want 30 days", d)
	}
}

// ── GetDeleteArgs ──────────────────────────────────────────────

func TestGetDeleteArgsMultipleExt(t *testing.T) {
	ids := getDeleteArgs([]string{"--delete", "a", "--delete", "b"})
	if len(ids) != 2 {
		t.Fatalf("got %d, want 2", len(ids))
	}
	if ids[0] != "a" || ids[1] != "b" {
		t.Errorf("ids = %v", ids)
	}
}

func TestGetDeleteArgsNoneExt(t *testing.T) {
	ids := getDeleteArgs([]string{"--list"})
	if len(ids) != 0 {
		t.Errorf("got %d, want 0", len(ids))
	}
}

func TestGetDeleteArgsMissingExt(t *testing.T) {
	ids := getDeleteArgs([]string{"--delete"})
	if len(ids) != 0 {
		t.Error("should return empty when no value follows")
	}
}

// ── PrintConversation ──────────────────────────────────────────

func TestPrintConversationExt(t *testing.T) {
	conv := &conversations.Conversation{
		ID:    "test-conv-id",
		Title: "Test conversation",
		Messages: []conversations.Message{
			{Role: "system", Content: "System prompt"},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	// Just verify it doesn't panic.
	printConversation(conv)
}
