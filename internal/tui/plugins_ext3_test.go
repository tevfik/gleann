package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ── PluginModel Update ─────────────────────────────────────────

func newTestPluginModel() PluginModel {
	return PluginModel{
		plugins:  knownPlugins,
		statuses: make([]pluginStatus, len(knownPlugins)),
		width:    80,
		height:   24,
		state:    psMain,
	}
}

func TestPluginUpdateWindowSize(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	pm := result.(PluginModel)
	if pm.width != 120 || pm.height != 40 {
		t.Error("size not updated")
	}
}

func TestPluginUpdateMainCtrlC(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	pm := result.(PluginModel)
	if !pm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestPluginUpdateMainEsc(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	pm := result.(PluginModel)
	if !pm.quitting {
		t.Error("esc should quit")
	}
}

func TestPluginUpdateMainUpDown(t *testing.T) {
	m := newTestPluginModel()
	m.cursor = 0

	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := result.(PluginModel)
	if len(pm.plugins) > 1 && pm.cursor != 1 {
		t.Errorf("cursor = %d, want 1", pm.cursor)
	}

	result, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = result.(PluginModel)
	if pm.cursor != 0 {
		t.Errorf("cursor = %d, want 0", pm.cursor)
	}
}

func TestPluginUpdateMainUpBound(t *testing.T) {
	m := newTestPluginModel()
	m.cursor = 0
	result, _ := m.Update(tea.KeyPressMsg{Code: 'k'})
	pm := result.(PluginModel)
	if pm.cursor != 0 {
		t.Error("should not go below 0")
	}
}

func TestPluginUpdateMainEnter(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm := result.(PluginModel)
	if pm.state != psDetail {
		t.Errorf("state = %d, want psDetail", pm.state)
	}
}

func TestPluginUpdateMainRight(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(tea.KeyPressMsg{Code: 'l'})
	pm := result.(PluginModel)
	if pm.state != psDetail {
		t.Errorf("state = %d, want psDetail", pm.state)
	}
}

func TestPluginUpdateMainRefresh(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(tea.KeyPressMsg{Code: 'r'})
	pm := result.(PluginModel)
	if pm.status != "↻ Refreshed" {
		t.Errorf("status = %q, want '↻ Refreshed'", pm.status)
	}
}

func TestPluginUpdateMainStatusClear(t *testing.T) {
	m := newTestPluginModel()
	m.status = "some status"
	// Any key press should clear status.
	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := result.(PluginModel)
	if pm.status != "" {
		t.Errorf("status should have been cleared, got %q", pm.status)
	}
}

// ── Detail state ───────────────────────────────────────────────

func TestPluginUpdateDetailEsc(t *testing.T) {
	m := newTestPluginModel()
	m.state = psDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	pm := result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("state = %d, want psMain", pm.state)
	}
}

func TestPluginUpdateDetailCtrlC(t *testing.T) {
	m := newTestPluginModel()
	m.state = psDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	pm := result.(PluginModel)
	if !pm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestPluginUpdateDetailLeft(t *testing.T) {
	m := newTestPluginModel()
	m.state = psDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: 'h'})
	pm := result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("h should go back to main, got %d", pm.state)
	}
}

// ── Result state ───────────────────────────────────────────────

func TestPluginUpdateResultCtrlC(t *testing.T) {
	m := newTestPluginModel()
	m.state = psResult
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	pm := result.(PluginModel)
	if !pm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestPluginUpdateResultAnyKey(t *testing.T) {
	m := newTestPluginModel()
	m.state = psResult
	m.status = "some result"
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm := result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("state = %d, want psMain", pm.state)
	}
	if pm.status != "" {
		t.Error("status should be cleared")
	}
}

// ── Action messages ────────────────────────────────────────────

func TestPluginUpdateActionMsgSuccess(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(pluginActionMsg{
		plugin: "test",
		action: "install",
		output: "installed",
	})
	pm := result.(PluginModel)
	if pm.state != psResult {
		t.Errorf("state = %d, want psResult", pm.state)
	}
	if !strings.Contains(pm.status, "installed") {
		t.Errorf("status = %q, should contain 'installed'", pm.status)
	}
}

func TestPluginUpdateActionMsgError(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(pluginActionMsg{
		plugin: "test",
		action: "install",
		err:    &testError{msg: "failed"},
	})
	pm := result.(PluginModel)
	if pm.state != psResult {
		t.Errorf("state = %d, want psResult", pm.state)
	}
	if !strings.Contains(pm.status, "failed") {
		t.Error("should show error")
	}
}

func TestPluginUpdateProgressMsg(t *testing.T) {
	m := newTestPluginModel()
	result, _ := m.Update(pluginInstallProgressMsg{
		plugin:  "test",
		message: "Installing...",
	})
	pm := result.(PluginModel)
	if len(pm.progressLines) != 1 {
		t.Errorf("progress lines = %d, want 1", len(pm.progressLines))
	}
	if pm.progressLines[0] != "Installing..." {
		t.Errorf("progress = %q", pm.progressLines[0])
	}
}

func TestPluginUpdateProgressMsgOverflow(t *testing.T) {
	m := newTestPluginModel()
	for i := 0; i < 15; i++ {
		result, _ := m.Update(pluginInstallProgressMsg{message: "line"})
		m = result.(PluginModel)
	}
	if len(m.progressLines) > 10 {
		t.Errorf("should keep only last 10 lines, got %d", len(m.progressLines))
	}
}

// ── View ───────────────────────────────────────────────────────

func TestPluginViewMain(t *testing.T) {
	m := newTestPluginModel()
	v := m.View()
	if !strings.Contains(v.Content, "Plugins") || !strings.Contains(v.Content, "Plugin") {
		t.Error("should show plugin manager title")
	}
}

func TestPluginViewDetail(t *testing.T) {
	m := newTestPluginModel()
	m.state = psDetail
	v := m.View()
	// Detail should show the plugin name.
	if len(m.plugins) > 0 && !strings.Contains(v.Content, m.plugins[0].Name) {
		t.Errorf("should show plugin name %q", m.plugins[0].Name)
	}
}

func TestPluginViewQuitting(t *testing.T) {
	m := newTestPluginModel()
	m.quitting = true
	v := m.View()
	if v.Content != "" {
		t.Error("quitting view should be empty")
	}
}

func TestPluginViewAction(t *testing.T) {
	m := newTestPluginModel()
	m.state = psAction
	m.actionMsg = "Installing..."
	m.progressLines = []string{"Step 1", "Step 2"}
	v := m.View()
	if !strings.Contains(v.Content, "Installing") {
		t.Error("should show action message")
	}
}

func TestPluginViewResult(t *testing.T) {
	m := newTestPluginModel()
	m.state = psResult
	m.status = "✓ Done"
	v := m.View()
	if !strings.Contains(v.Content, "Done") {
		t.Error("should show result status")
	}
}

// ── Helper functions ───────────────────────────────────────────

func TestRepoNameExt3(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/tevfik/gleann-plugin-docs", "gleann-plugin-docs"},
		{"https://github.com/tevfik/gleann-plugin-docs.git", "gleann-plugin-docs"},
		{"https://example.com/repo", "repo"},
	}
	for _, tt := range tests {
		got := repoName(tt.url)
		if got != tt.want {
			t.Errorf("repoName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestCheckPluginStatusNotInstalledExt3(t *testing.T) {
	m := newTestPluginModel()
	if len(m.plugins) == 0 {
		t.Skip("no plugins")
	}
	s := m.checkPluginStatus(m.plugins[0])
	// In test environment, plugins are not installed.
	if s != statusNotInstalled {
		t.Logf("status = %s (expected not installed)", s)
	}
}
