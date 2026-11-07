package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func spress(m SettingsModel, keys ...tea.KeyPressMsg) SettingsModel {
	for _, k := range keys {
		r, _ := m.Update(k)
		m = r.(SettingsModel)
	}
	return m
}

func keyText(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
)

func cursorTo(t *testing.T, m SettingsModel, key string) SettingsModel {
	t.Helper()
	for i, k := range m.rows() {
		if k == key {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("row %s not visible", key)
	return m
}

func TestSettingsScreenToggleSavesAndRevealsRerankFields(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	if strings.Contains(strings.Join(m.rows(), ","), "rerank_model") {
		t.Fatal("rerank_model must be hidden while reranking is off")
	}
	m = cursorTo(t, m, "rerank_enabled")
	m = spress(m, keyRight)
	if m.err != nil {
		t.Fatal(m.err)
	}
	saved, _ := Settings().Load()
	if !saved.RerankEnabled {
		t.Fatal("toggling must save at once")
	}
	if !strings.Contains(strings.Join(m.rows(), ","), "rerank_model") {
		t.Fatal("rerank_model must appear once reranking is on")
	}
	if !strings.Contains(m.render(), "Reranker model") {
		t.Error("the screen must show the reranker model row")
	}
}

func TestSettingsScreenPickerFiltersAndSaves(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = cursorTo(t, m, "storage_mode")
	m = spress(m, keyEnter)
	if !m.picking {
		t.Fatal("enter on a select opens the picker")
	}
	m = spress(m, keyText("r"), keyText("e"), keyText("c"), keyText("o"), keyText("m"), keyText("p"))
	if opts := m.filtered("storage_mode"); len(opts) != 1 || opts[0].Value != "recompute" {
		t.Fatalf("filter 'recomp' must leave only recompute, got %+v", opts)
	}
	m = spress(m, keyEnter)
	if m.picking || m.err != nil {
		t.Fatalf("enter must pick and close: picking=%v err=%v", m.picking, m.err)
	}
	saved, _ := Settings().Load()
	if saved.StorageMode != "recompute" {
		t.Fatalf("storage_mode not saved: %q", saved.StorageMode)
	}
}

func TestSettingsScreenTypedValueIsValidated(t *testing.T) {
	settingsHome(t)
	m := NewSettingsModel()
	m = cursorTo(t, m, "top_k")
	m = spress(m, keyEnter)
	m.input = "500" // above Max
	m = spress(m, keyEnter)
	if m.err == nil || !m.editing {
		t.Fatal("an out-of-range value must be refused and keep the input open")
	}
	m.input = "15"
	m = spress(m, keyEnter)
	if m.err != nil || m.editing {
		t.Fatalf("a valid value must save: %v", m.err)
	}
	if saved, _ := Settings().Load(); saved.TopK != 15 {
		t.Fatalf("top_k = %d, want 15", saved.TopK)
	}
	_ = keyDown
}
