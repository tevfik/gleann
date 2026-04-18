package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ── Chat helpers ───────────────────────────────────────────────

func TestFindClosestFloat(t *testing.T) {
	tests := []struct {
		presets []float64
		value   float64
		want    int
	}{
		{temperaturePresets, 0.0, 0},
		{temperaturePresets, 0.7, 7},
		{temperaturePresets, 2.0, 20},
		{temperaturePresets, 0.15, 1},  // closest to 0.1
		{temperaturePresets, 1.95, 19}, // closest to 1.9
	}
	for _, tt := range tests {
		got := findClosestFloat(tt.presets, tt.value)
		if got != tt.want {
			t.Errorf("findClosestFloat(%v, %v) = %d, want %d", tt.presets, tt.value, got, tt.want)
		}
	}
}

func TestFindClosestInt(t *testing.T) {
	tests := []struct {
		presets []int
		value   int
		want    int
	}{
		{maxTokensPresets, 256, 0},
		{maxTokensPresets, 512, 1},
		{maxTokensPresets, 1024, 2},
		{maxTokensPresets, 8192, 5},
		{maxTokensPresets, 300, 0},  // closest to 256
		{maxTokensPresets, 3000, 3}, // closest to 2048
		{topKPresets, 3, 0},
		{topKPresets, 30, 5},
		{topKPresets, 7, 1}, // closest to 5
	}
	for _, tt := range tests {
		got := findClosestInt(tt.presets, tt.value)
		if got != tt.want {
			t.Errorf("findClosestInt(%v, %d) = %d, want %d", tt.presets, tt.value, got, tt.want)
		}
	}
}

func TestIntAbs(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, 0},
		{5, 5},
		{-5, 5},
		{-1, 1},
		{100, 100},
	}
	for _, tt := range tests {
		got := intAbs(tt.in)
		if got != tt.want {
			t.Errorf("intAbs(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestRenderMarkdownContent(t *testing.T) {
	// Simple markdown.
	result := renderMarkdownContent("# Hello\n\nWorld", 80)
	if result == "" {
		t.Error("renderMarkdownContent returned empty")
	}

	// Very narrow width.
	result = renderMarkdownContent("test", 10)
	if result == "" {
		t.Error("narrow renderMarkdownContent returned empty")
	}

	// Plain text fallback.
	result = renderMarkdownContent("plain text", 80)
	if !strings.Contains(result, "plain") {
		t.Error("plain text should be preserved")
	}
}

func TestSettingsFieldConstants(t *testing.T) {
	if fieldTemperature != 0 {
		t.Error("fieldTemperature should be 0")
	}
	if fieldCount <= fieldSystemPrompt {
		t.Error("fieldCount should be > fieldSystemPrompt")
	}
}

func TestTemperaturePresets(t *testing.T) {
	if len(temperaturePresets) < 20 {
		t.Errorf("expected >= 20 temperature presets, got %d", len(temperaturePresets))
	}
	if temperaturePresets[0] != 0.0 {
		t.Error("first preset should be 0.0")
	}
	if temperaturePresets[len(temperaturePresets)-1] != 2.0 {
		t.Error("last preset should be 2.0")
	}
}

func TestMaxTokensPresets(t *testing.T) {
	if len(maxTokensPresets) < 5 {
		t.Errorf("expected >= 5 max tokens presets, got %d", len(maxTokensPresets))
	}
	if maxTokensPresets[0] != 256 {
		t.Error("first preset should be 256")
	}
}

func TestTopKPresets(t *testing.T) {
	if len(topKPresets) < 5 {
		t.Errorf("expected >= 5 topK presets, got %d", len(topKPresets))
	}
}

// ── Home model ─────────────────────────────────────────────────

func TestNewHomeModel(t *testing.T) {
	m := NewHomeModel()
	if m.cursor != 0 {
		t.Error("initial cursor should be 0")
	}
	if m.quitting {
		t.Error("should not be quitting")
	}
}

func TestHomeModelInit(t *testing.T) {
	m := NewHomeModel()
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init should return nil")
	}
}

func TestHomeModelNavigation(t *testing.T) {
	m := NewHomeModel()
	m.width = 80
	m.height = 24

	// Down.
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	hm := result.(HomeModel)
	if hm.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", hm.cursor)
	}

	// Down again.
	result, _ = hm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	hm = result.(HomeModel)
	if hm.cursor != 2 {
		t.Errorf("cursor after second down = %d, want 2", hm.cursor)
	}

	// Up.
	result, _ = hm.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	hm = result.(HomeModel)
	if hm.cursor != 1 {
		t.Errorf("cursor after up = %d, want 1", hm.cursor)
	}

	// Don't go below 0.
	hm.cursor = 0
	result, _ = hm.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	hm = result.(HomeModel)
	if hm.cursor != 0 {
		t.Error("cursor should not go below 0")
	}
}

func TestHomeModelQuit(t *testing.T) {
	m := NewHomeModel()

	result, cmd := m.Update(tea.KeyPressMsg{Code: 'q'})
	hm := result.(HomeModel)
	if !hm.Quitting() {
		t.Error("should be quitting after 'q'")
	}
	if cmd == nil {
		t.Error("should return tea.Quit cmd")
	}
}

func TestHomeModelChoose(t *testing.T) {
	tests := []struct {
		cursor int
		want   Screen
	}{
		{0, ScreenOnboard},
		{1, ScreenChat},
		{2, ScreenIndexes},
		{3, ScreenPlugins},
	}
	for _, tt := range tests {
		m := NewHomeModel()
		m.cursor = tt.cursor
		result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		hm := result.(HomeModel)
		if hm.Chosen() != tt.want {
			t.Errorf("cursor=%d: Chosen() = %d, want %d", tt.cursor, hm.Chosen(), tt.want)
		}
	}

	// Quit option (cursor 4).
	m := NewHomeModel()
	m.cursor = 4
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	hm := result.(HomeModel)
	if !hm.Quitting() {
		t.Error("cursor=4 should quit")
	}
}

func TestHomeModelView(t *testing.T) {
	m := NewHomeModel()
	m.width = 80
	m.height = 24

	v := m.View()
	s := v.Content
	if s == "" {
		t.Error("View() returned empty")
	}
	if !strings.Contains(s, "gleann") {
		t.Error("View should contain 'gleann'")
	}

	// Quitting view.
	m.quitting = true
	v = m.View()
	s = v.Content
	if !strings.Contains(s, "Bye") {
		t.Error("quitting view should say Bye")
	}
}

func TestHomeModelWindowSize(t *testing.T) {
	m := NewHomeModel()
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	hm := result.(HomeModel)
	if hm.width != 120 || hm.height != 50 {
		t.Errorf("size = %dx%d", hm.width, hm.height)
	}
}

// ── Screen constants ───────────────────────────────────────────

func TestScreenConstants(t *testing.T) {
	if ScreenHome != 0 {
		t.Error("ScreenHome should be 0")
	}
	if ScreenOnboard <= ScreenHome {
		t.Error("ScreenOnboard should be > ScreenHome")
	}
	if ScreenPlugins <= ScreenIndexes {
		t.Error("ScreenPlugins should be > ScreenIndexes")
	}
}

// ── Menu items ─────────────────────────────────────────────────

func TestMenuItems(t *testing.T) {
	if len(menuItems) < 4 {
		t.Errorf("expected >= 4 menu items, got %d", len(menuItems))
	}
	for _, item := range menuItems {
		if item.title == "" {
			t.Error("menu item has empty title")
		}
		if item.desc == "" {
			t.Error("menu item has empty desc")
		}
		if item.icon == "" {
			t.Error("menu item has empty icon")
		}
	}
}

// ── Styles ─────────────────────────────────────────────────────

func TestLogo(t *testing.T) {
	logo := Logo()
	if logo == "" {
		t.Error("Logo() returned empty")
	}
	if !strings.Contains(logo, "gleann") && !strings.Contains(logo, "▄") {
		// Logo should contain ASCII art characters
	}
}

func TestSmallLogo(t *testing.T) {
	logo := SmallLogo()
	if logo == "" {
		t.Error("SmallLogo() returned empty")
	}
}

// ── Index list model ───────────────────────────────────────────

func TestIndexListModelEmpty(t *testing.T) {
	m := NewIndexListModel(t.TempDir())
	if len(m.indexes) != 0 {
		t.Errorf("expected 0 indexes, got %d", len(m.indexes))
	}
}

func TestIndexListModelInit(t *testing.T) {
	m := NewIndexListModel(t.TempDir())
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init should return nil")
	}
}

func TestIndexListModelQuit(t *testing.T) {
	m := NewIndexListModel(t.TempDir())
	result, _ := m.Update(tea.KeyPressMsg{Code: 'q'})
	ilm := result.(IndexListModel)
	if !ilm.Quitting() {
		t.Error("should be quitting")
	}
	if ilm.Selected() != "" {
		t.Error("Selected should be empty when quitting")
	}
}

func TestIndexListModelSkip(t *testing.T) {
	m := NewIndexListModel(t.TempDir())
	// With no indexes, enter should skip (pure LLM).
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ilm := result.(IndexListModel)
	if !ilm.Skipped() {
		t.Error("should be skipped with no indexes")
	}
}

func TestIndexListModelView(t *testing.T) {
	m := NewIndexListModel(t.TempDir())
	m.width = 80
	m.height = 24
	v := m.View()
	if v.Content == "" {
		t.Error("View returned empty")
	}
}

// ── Index manage model ─────────────────────────────────────────

func TestIndexManageModelEmpty(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	if len(m.indexes) != 0 {
		t.Errorf("expected 0 indexes, got %d", len(m.indexes))
	}
}

func TestIndexManageModelInit(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init should return nil")
	}
}

func TestIndexManageModelNavigation(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	m.width = 80
	m.height = 24

	// Quit.
	result, cmd := m.Update(tea.KeyPressMsg{Code: 'q'})
	im := result.(IndexManageModel)
	if !im.Quitting() {
		t.Error("should quit")
	}
	if cmd == nil {
		t.Error("should return quit cmd")
	}

	// Refresh.
	m2 := NewIndexManageModel(t.TempDir())
	result, _ = m2.Update(tea.KeyPressMsg{Code: 'r'})
	im = result.(IndexManageModel)
	if im.status != "↻ Refreshed" {
		t.Errorf("status = %q", im.status)
	}
}

func TestIndexManageModelView(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	m.width = 80
	m.height = 24

	// List view empty.
	v := m.View()
	s := v.Content
	if !strings.Contains(s, "No indexes") {
		t.Error("empty view should mention 'No indexes'")
	}

	// Quitting.
	m.quitting = true
	v = m.View()
	if v.Content != "" {
		t.Error("quitting view should be empty")
	}
}

func TestIndexManageModelWindowSize(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	im := result.(IndexManageModel)
	if im.width != 100 || im.height != 40 {
		t.Errorf("size = %dx%d", im.width, im.height)
	}
}

// ── Index manage timeAgo ───────────────────────────────────────

func TestTimeAgo(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{5 * 24 * time.Hour, "5d ago"},
	}
	for _, tt := range tests {
		got := timeAgo(time.Now().Add(-tt.d))
		if got != tt.want {
			t.Errorf("timeAgo(-%v) = %q, want %q", tt.d, got, tt.want)
		}
	}

	// Zero time.
	if timeAgo(time.Time{}) != "" {
		t.Error("zero time should return empty")
	}

	// Old date.
	old := time.Now().Add(-365 * 24 * time.Hour)
	result := timeAgo(old)
	if !strings.Contains(result, "20") {
		t.Errorf("old date = %q, expected year", result)
	}
}

// ── Onboard model ──────────────────────────────────────────────

func TestNewOnboardModel(t *testing.T) {
	m := NewOnboardModel()
	if m.phase != phaseQuickOrAdv {
		t.Errorf("initial phase = %d, want phaseQuickOrAdv", m.phase)
	}
	if m.done {
		t.Error("should not be done")
	}
	if m.cancelled {
		t.Error("should not be cancelled")
	}
}

func TestSettingsMenuItems(t *testing.T) {
	items := settingsMenuItems()
	if len(items) < 10 {
		t.Errorf("expected >= 10 settings items, got %d", len(items))
	}
	for _, item := range items {
		if item.label == "" {
			t.Error("settings item has empty label")
		}
	}
}

func TestWizardPhaseConstants(t *testing.T) {
	if phaseMenu != -1 {
		t.Error("phaseMenu should be -1")
	}
	if phaseQuickOrAdv != 0 {
		t.Error("phaseQuickOrAdv should be 0")
	}
	if totalVisibleSteps < 10 {
		t.Errorf("totalVisibleSteps = %d, expected >= 10", totalVisibleSteps)
	}
}
