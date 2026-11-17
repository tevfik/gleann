package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestPluginStatusString(t *testing.T) {
	tests := []struct {
		s    pluginStatus
		want string
	}{
		{statusNotInstalled, "Not installed"},
		{statusInstalled, "Installed"},
		{statusRunning, "Running"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestPluginStatusBadge(t *testing.T) {
	for _, s := range []pluginStatus{statusNotInstalled, statusInstalled, statusRunning} {
		b := s.Badge()
		if b == "" {
			t.Errorf("(%d).Badge() returned empty", s)
		}
	}
}

func TestNewPluginModel(t *testing.T) {
	// Doesn't crash even if plugin dir doesn't exist.
	m := NewPluginModel()
	if len(m.plugins) != len(gleann.FetchPluginCatalog()) {
		t.Errorf("expected %d plugins, got %d", len(gleann.FetchPluginCatalog()), len(m.plugins))
	}
	if len(m.statuses) != len(gleann.FetchPluginCatalog()) {
		t.Errorf("expected %d statuses, got %d", len(gleann.FetchPluginCatalog()), len(m.statuses))
	}
}

func TestPluginModelInit(t *testing.T) {
	m := NewPluginModel()
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init() should return nil")
	}
}

func TestPluginModelQuitting(t *testing.T) {
	m := NewPluginModel()
	if m.Quitting() {
		t.Error("should not be quitting initially")
	}
}

func TestPluginModelUpdateNavigation(t *testing.T) {
	m := NewPluginModel()
	m.width = 80
	m.height = 24

	// Down.
	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := result.(PluginModel)
	if pm.cursor != 1 {
		t.Errorf("cursor after 'j' = %d, want 1", pm.cursor)
	}

	// Up.
	result, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = result.(PluginModel)
	if pm.cursor != 0 {
		t.Errorf("cursor after 'k' = %d, want 0", pm.cursor)
	}

	// Enter → detail.
	result, _ = pm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm = result.(PluginModel)
	if pm.state != psDetail {
		t.Errorf("state after enter = %d, want psDetail", pm.state)
	}

	// Esc → back to main.
	result, _ = pm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	pm = result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("state after esc = %d, want psMain", pm.state)
	}

	// Refresh.
	result, _ = pm.Update(tea.KeyPressMsg{Code: 'r'})
	pm = result.(PluginModel)
	if pm.status != "↻ Refreshed" {
		t.Errorf("status after 'r' = %q", pm.status)
	}
}

func TestPluginModelUpdateResult(t *testing.T) {
	m := NewPluginModel()
	m.state = psResult
	m.status = "some status"

	// Any key returns to main.
	result, _ := m.Update(tea.KeyPressMsg{Code: 'x'})
	pm := result.(PluginModel)
	if pm.state != psMain {
		t.Errorf("state = %d, want psMain", pm.state)
	}
}

func TestPluginModelView(t *testing.T) {
	m := NewPluginModel()
	m.width = 80
	m.height = 24

	v := m.View()
	if v.Content == "" {
		t.Error("View() returned empty")
	}

	// Detail view.
	m.state = psDetail
	v = m.View()
	if v.Content == "" {
		t.Error("viewDetail() returned empty")
	}

	// Action view.
	m.state = psAction
	m.actionMsg = "Installing..."
	v = m.View()
	if v.Content == "" {
		t.Error("viewAction() returned empty")
	}

	// Result view.
	m.state = psResult
	m.status = "Done"
	v = m.View()
	if v.Content == "" {
		t.Error("viewResult() returned empty")
	}

	// Quitting.
	m.quitting = true
	v = m.View()
	if v.Content != "" {
		t.Error("quitting View() should return empty")
	}
}

func TestPluginModelViewWithProgress(t *testing.T) {
	m := NewPluginModel()
	m.state = psAction
	m.actionMsg = "test action"
	m.progressLines = []string{"step 1", "✓ step 2", "🔍 step 3"}
	m.width = 80
	m.height = 24

	v := m.View()
	s := v.Content
	if !strings.Contains(s, "step 1") {
		t.Error("progress lines should appear in view")
	}
}

func TestPluginModelWindowSize(t *testing.T) {
	m := NewPluginModel()
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	pm := result.(PluginModel)
	if pm.width != 100 || pm.height != 40 {
		t.Errorf("size = %dx%d", pm.width, pm.height)
	}
}

func TestPluginActionMsg(t *testing.T) {
	m := NewPluginModel()
	m.width = 80

	// Success.
	result, _ := m.Update(pluginActionMsg{plugin: "test", action: "install", output: "OK"})
	pm := result.(PluginModel)
	if pm.state != psResult {
		t.Errorf("state = %d, want psResult", pm.state)
	}
	if !strings.Contains(pm.status, "✓") {
		t.Errorf("status = %q, expected success", pm.status)
	}

	// Error.
	m2 := NewPluginModel()
	result, _ = m2.Update(pluginActionMsg{plugin: "test", action: "install", err: os.ErrNotExist})
	pm = result.(PluginModel)
	if !strings.Contains(pm.status, "✗") {
		t.Errorf("status = %q, expected error badge", pm.status)
	}
}

func TestPluginProgressMsg(t *testing.T) {
	m := NewPluginModel()
	m.width = 80

	// Send progress messages.
	for i := 0; i < 15; i++ {
		result, _ := m.Update(pluginInstallProgressMsg{
			plugin:      "test",
			message:     "step",
			continueCmd: func() tea.Msg { return nil },
		})
		m = result.(PluginModel)
	}
	// Should be capped at 10 lines.
	if len(m.progressLines) > 10 {
		t.Errorf("progressLines = %d, want <= 10", len(m.progressLines))
	}
}

func TestFetchSettingsSummaryReadsThePluginForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"schema_version":2,"fields":[
			{"key":"engine","label":"Page parser","type":"select","value":"auto"},
			{"key":"vlm","label":"Granite VLM","type":"bool","value":false},
			{"key":"deep","label":"Deep tables","type":"bool","value":false,"visible_if":{"key":"vlm","equals":true}},
			{"key":"ort","label":"ONNX Runtime","type":"path","value":"","placeholder":"/lib/libonnxruntime.so"},
			{"key":"out","label":"Output","type":"path","value":""},
			{"key":"token","label":"Token","type":"secret","value":"","is_set":true}]}`))
	}))
	defer srv.Close()

	msg := fetchSettingsSummary("p", srv.URL, "")().(settingsSummaryMsg)
	want := []settingRow{{"Page parser", "auto"}, {"Granite VLM", "off"}, {"ONNX Runtime", "auto: /lib/libonnxruntime.so"}}
	if !reflect.DeepEqual(msg.rows, want) {
		t.Errorf("rows = %+v\nwant  %+v", msg.rows, want)
	}
	// A plugin without a settings form shows no settings, and no error.
	if msg := fetchSettingsSummary("p", srv.URL+"/nothing", "")().(settingsSummaryMsg); msg.rows != nil {
		t.Errorf("rows for a plugin without settings: %+v", msg.rows)
	}
}

func TestResolveBinaryPrefersTheRegisteredCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gleann-plugin-docs")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info := gleann.PluginMeta{Name: "gleann-plugin-docs", SettingsCmd: []string{"gleann-plugin-docs", "tui"}}
	reg := &gleann.Plugin{Name: "gleann-plugin-docs", Command: []string{bin, "--serve"}}
	if got := resolveBinary(info, reg); got != bin {
		t.Errorf("resolveBinary = %q, want the registered %q", got, bin)
	}
	// A wrapper script under another name is not the settings binary.
	reg.Command = []string{filepath.Join(dir, "run-docs.sh")}
	if got := resolveBinary(info, reg); got == reg.Command[0] {
		t.Error("a differently named registered command was used")
	}
}
