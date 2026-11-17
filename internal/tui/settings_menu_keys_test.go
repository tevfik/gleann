package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var (
	keyUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	keyLeft  = tea.KeyPressMsg{Code: tea.KeyLeft}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyBksp  = tea.KeyPressMsg{Code: tea.KeyBackspace}
	keyDel   = tea.KeyPressMsg{Code: tea.KeyDelete}
	keyCtrlU = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	keyCtrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
)

func TestSettingsLeaveKeys(t *testing.T) {
	for name, key := range map[string]tea.KeyPressMsg{"esc": keyEsc, "q": keyText("q"), "ctrl+c": keyCtrlC} {
		t.Run(name, func(t *testing.T) {
			settingsHome(t)
			m := NewSettingsModel()
			r, cmd := m.Update(key)
			if !r.(SettingsModel).Done() || cmd == nil {
				t.Fatal("key must finish the screen and quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("expected tea.QuitMsg")
			}
		})
	}
	settingsHome(t)
	m := NewSettingsModel()
	m.cursor = len(m.rows()) - 1 // the Back row
	r, cmd := m.Update(keyEnter)
	if !r.(SettingsModel).Done() || cmd == nil {
		t.Fatal("enter on Back must leave")
	}
	if NewSettingsModel().Init() != nil {
		t.Fatal("Init has nothing to run")
	}
}

func TestSettingsCursorMovesAndStaysInRange(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = spress(m, keyUp)
	if m.cursor != 0 {
		t.Fatalf("cursor above first row: %d", m.cursor)
	}
	m = spress(m, keyDown, keyText("j"))
	if m.cursor != 2 {
		t.Fatalf("down+j = %d, want 2", m.cursor)
	}
	m = spress(m, keyText("k"))
	if m.cursor != 1 {
		t.Fatalf("k = %d, want 1", m.cursor)
	}
	for range m.rows() {
		m = spress(m, keyDown)
	}
	if m.cursor != len(m.rows())-1 {
		t.Fatalf("cursor must stop on Back, got %d", m.cursor)
	}
}

func TestSettingsWindowSizeAndUnknownMessages(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	r, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = r.(SettingsModel)
	if m.height != 30 || cmd != nil {
		t.Fatalf("height = %d cmd=%v", m.height, cmd)
	}
	if r, cmd = m.Update(struct{}{}); cmd != nil || r.(SettingsModel).height != 30 {
		t.Fatal("unknown messages are ignored")
	}
	// A short terminal shows a window of the rows, still including the cursor row.
	m.cursor = 20
	out := m.View().Content
	if !strings.Contains(out, "▸") {
		t.Fatal("the cursor row must stay in the window")
	}
	if strings.Count(out, "\n") > 30 {
		t.Fatalf("view exceeds terminal height: %d lines", strings.Count(out, "\n"))
	}
}

func TestSettingsStepBackAndForthAndReset(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = cursorTo(t, m, "storage_mode")
	m = spress(m, keyRight)
	first := m.reg.Display(m.cfg, "storage_mode")
	m = spress(m, keyText("l"), keyLeft)
	if got := m.reg.Display(m.cfg, "storage_mode"); got != first {
		t.Fatalf("right,right,left should land on the first step: %q vs %q", got, first)
	}
	m = cursorTo(t, m, "top_k")
	m = spress(m, keyRight, keyRight)
	if saved, _ := Settings().Load(); saved.TopK == 0 {
		t.Fatal("stepping a number must save it")
	}
	m = spress(m, keyDel)
	if saved, _ := Settings().Load(); saved.TopK != 0 {
		t.Fatalf("delete must reset to the default, got %d", saved.TopK)
	}
	if m.notice != "Saved." {
		t.Fatalf("notice = %q", m.notice)
	}
	// Left/right on a text row does nothing.
	m = cursorTo(t, m, "ollama_host")
	m = spress(m, keyRight)
	if m.notice != "Saved." || m.err != nil {
		t.Fatal("a text row is not steppable")
	}
}

func TestSettingsEnterOnBoolTogglesAndRestartNoteShows(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = cursorTo(t, m, "server_enabled")
	m = spress(m, keyEnter)
	if saved, _ := Settings().Load(); !saved.ServerEnabled {
		t.Fatal("enter on a bool toggles it")
	}
	if !strings.Contains(m.notice, "Restart") {
		t.Fatalf("a restart-only setting must say so: %q", m.notice)
	}
}

func TestSettingsTextEditing(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = cursorTo(t, m, "ollama_host")
	m = spress(m, keyEnter)
	if !m.editing || m.input == "" {
		t.Fatalf("editing a text starts from the current value, input=%q", m.input)
	}
	m = spress(m, keyCtrlU)
	if m.input != "" {
		t.Fatal("ctrl+u clears")
	}
	m = spress(m, keyText("h"), keyText("x"), keyBksp)
	if m.input != "h" {
		t.Fatalf("input = %q", m.input)
	}
	m = spress(m, keyBksp, keyBksp) // backspace on empty is harmless
	if m.input != "" {
		t.Fatal("empty after backspaces")
	}
	if !strings.Contains(m.render(), "█") {
		t.Error("an open input shows the caret")
	}
	if !strings.Contains(m.render(), "ctrl+u clear") {
		t.Error("the hint explains the editing keys")
	}
	m = spress(m, keyEsc)
	if m.editing {
		t.Fatal("esc cancels")
	}
	// Secrets are never prefilled.
	m = cursorTo(t, m, "openai_api_key")
	m = spress(m, keyEnter)
	if !m.editing || m.input != "" {
		t.Fatalf("a secret opens empty, input=%q", m.input)
	}
	m = spress(m, keyText("s"), keyText("k"), keyEnter)
	if saved, _ := Settings().Load(); saved.OpenAIKey != "sk" {
		t.Fatalf("secret not saved: %q", saved.OpenAIKey)
	}
}

func TestSettingsPickerNavigationEscAndCustomValue(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = spress(cursorTo(t, m, "rerank_enabled"), keyRight)
	m.cfg.RerankProvider = "cohere"
	m = cursorTo(t, m, "rerank_model")
	m = spress(m, keyEnter)
	if !m.picking {
		t.Fatal("a select with options opens the picker")
	}
	m = spress(m, keyUp)
	if m.pick != 0 {
		t.Fatal("up stops at the first option")
	}
	m = spress(m, keyDown, keyDown, keyDown)
	if m.pick != len(m.filtered("rerank_model"))-1 {
		t.Fatalf("down stops at the last option: %d", m.pick)
	}
	if out := m.render(); !strings.Contains(out, "filter:") || !strings.Contains(out, "esc cancel") {
		t.Fatalf("picker view: %q", out)
	}
	m = spress(m, keyEsc)
	if m.picking {
		t.Fatal("esc closes the picker")
	}

	// A value no option matches is accepted as typed for AllowCustom fields.
	m = spress(m, keyEnter, keyText("m"), keyText("y"), keyText("-"), keyText("r"))
	if len(m.filtered("rerank_model")) != 0 {
		t.Fatal("the filter should match nothing")
	}
	if out := m.render(); !strings.Contains(out, `use "my-r"`) || !strings.Contains(out, "enter any name") {
		t.Fatalf("custom hint missing: %q", out)
	}
	m = spress(m, keyBksp, keyEnter)
	if saved, _ := Settings().Load(); saved.RerankModel != "my-" {
		t.Fatalf("custom model = %q", saved.RerankModel)
	}
}

func TestSettingsPickerRefusesEmptyEnterAndShowsNoMatch(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = spress(cursorTo(t, m, "storage_mode"), keyEnter, keyText("z"), keyText("z"))
	if !strings.Contains(m.render(), "(no match)") {
		t.Fatal("no options and no custom values: say so")
	}
	m = spress(m, keyEnter)
	if !m.picking {
		t.Fatal("enter with nothing to pick keeps the picker open")
	}
}

func TestSettingsPickerErrorKeepsPickerOpen(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m.cfg.ServerAddr = "0.0.0.0:8080"
	if err := Settings().Save(m.cfg); err != nil {
		t.Fatal(err)
	}
	m = cursorTo(t, m, "auth")
	m = spress(m, keyEnter, keyText("o"), keyText("f"), keyText("f"), keyEnter)
	if m.err == nil || !m.picking {
		t.Fatalf("auth=off on a public address must be refused and keep the picker: err=%v", m.err)
	}
	if !strings.Contains(m.render(), "loopback") {
		t.Error("the picker shows the refusal")
	}
}

func TestSettingsDownloadedMessage(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m.downloading = "x"
	r, _ := m.Update(settingsDownloadedMsg{name: "bge", err: errors.New("disk full")})
	m = r.(SettingsModel)
	if m.downloading != "" || m.err == nil || m.notice != "" {
		t.Fatalf("failed download: %+v", m)
	}
	r, _ = m.Update(settingsDownloadedMsg{name: "bge"})
	m = r.(SettingsModel)
	if m.err != nil || m.notice != "Downloaded bge." {
		t.Fatalf("ok download: err=%v notice=%q", m.err, m.notice)
	}
	m.downloading = "bge"
	if !strings.Contains(m.render(), "Downloading bge") {
		t.Error("a running download is shown")
	}
}

func TestSettingsWindowHelper(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = string(rune('a' + i))
	}
	tests := []struct {
		name    string
		cur, n  int
		first   string
		wantLen int
	}{
		{"too small to window", 10, 4, "a", 20},
		{"fits", 3, 30, "a", 20},
		{"cursor at top", 0, 10, "a", 10},
		{"cursor in middle", 10, 10, "f", 10},
		{"cursor at bottom", 19, 10, "k", 10},
	}
	for _, tc := range tests {
		got := window(lines, tc.cur, tc.n)
		if len(got) != tc.wantLen || got[0] != tc.first {
			t.Errorf("%s: len=%d first=%q", tc.name, len(got), got[0])
		}
	}
}

func TestEditTextKeys(t *testing.T) {
	if got := editText("héé", keyBksp); got != "hé" {
		t.Fatalf("backspace must drop one rune, got %q", got)
	}
	if got := editText("abc", keyCtrlU); got != "" {
		t.Fatalf("ctrl+u = %q", got)
	}
	if got := editText("a", keyText("b")); got != "ab" {
		t.Fatalf("typing = %q", got)
	}
	if got := editText("a", keyEsc); got != "a" {
		t.Fatalf("non-text keys leave the text: %q", got)
	}
}

func TestSettingsLoadErrorIsShown(t *testing.T) {
	home := settingsHome(t)
	if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewSettingsModel()
	if m.err == nil || m.cfg == nil {
		t.Fatalf("a broken config must be reported, with a usable empty config: err=%v", m.err)
	}
	if !strings.Contains(m.render(), "not valid JSON") {
		t.Error("the error must be on screen")
	}
}
