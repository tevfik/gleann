package tui

import (
	"os"
	"testing"
	"time"
)

// ── timeAgo ───────────────────────────────────────────────────

func TestTimeAgoCov4(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"just now", time.Now().Add(-30 * time.Second), "just now"},
		{"minutes", time.Now().Add(-15 * time.Minute), "15m ago"},
		{"hours", time.Now().Add(-5 * time.Hour), "5h ago"},
		{"days", time.Now().Add(-3 * 24 * time.Hour), "3d ago"},
		{"old", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), "Jan 1, 2020"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timeAgo(tt.t)
			if got != tt.want {
				t.Errorf("timeAgo() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── findClosestFloat ──────────────────────────────────────────

func TestFindClosestFloatCov4(t *testing.T) {
	presets := []float64{0.0, 0.3, 0.5, 0.7, 1.0}
	tests := []struct {
		value float64
		want  int
	}{
		{0.0, 0},
		{0.29, 1},
		{0.5, 2},
		{0.8, 3},
		{1.0, 4},
		{0.51, 2},
	}
	for _, tt := range tests {
		got := findClosestFloat(presets, tt.value)
		if got != tt.want {
			t.Errorf("findClosestFloat(%v) = %d, want %d", tt.value, got, tt.want)
		}
	}
}

// ── findClosestInt ────────────────────────────────────────────

func TestFindClosestIntCov4(t *testing.T) {
	presets := []int{128, 256, 512, 1024, 2048}
	tests := []struct {
		value int
		want  int
	}{
		{128, 0},
		{200, 1},
		{512, 2},
		{1500, 3},
		{3000, 4},
	}
	for _, tt := range tests {
		got := findClosestInt(presets, tt.value)
		if got != tt.want {
			t.Errorf("findClosestInt(%d) = %d, want %d", tt.value, got, tt.want)
		}
	}
}

// ── intAbs ────────────────────────────────────────────────────

func TestIntAbsCov4(t *testing.T) {
	if intAbs(5) != 5 {
		t.Fatal("positive")
	}
	if intAbs(-5) != 5 {
		t.Fatal("negative")
	}
	if intAbs(0) != 0 {
		t.Fatal("zero")
	}
}

// ── capitalize ────────────────────────────────────────────────

func TestCapitalizeCov4(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"hello", "Hello"},
		{"Hello", "Hello"},
		{"a", "A"},
	}
	for _, tt := range tests {
		if got := capitalize(tt.in); got != tt.want {
			t.Errorf("capitalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── parseGitHubURL ────────────────────────────────────────────

func TestParseGitHubURLCov4(t *testing.T) {
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
	}{
		{"https://github.com/tevfik/gleann-plugin-docs", "tevfik", "gleann-plugin-docs"},
		{"https://github.com/tevfik/gleann-plugin-docs.git", "tevfik", "gleann-plugin-docs"},
		{"git@github.com:tevfik/gleann.git", "", ""}, // SSH format not supported
		{"not-a-url", "", ""},
	}
	for _, tt := range tests {
		owner, repo := parseGitHubURL(tt.url)
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("parseGitHubURL(%q) = (%q, %q), want (%q, %q)", tt.url, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}

// ── pluginOwner ───────────────────────────────────────────────

func TestPluginOwnerCov4_Default(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_OWNER", "")
	if got := pluginOwner(); got != defaultPluginOwner {
		t.Fatalf("expected %q, got %q", defaultPluginOwner, got)
	}
}

func TestPluginOwnerCov4_Override(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_OWNER", "custom-owner")
	if got := pluginOwner(); got != "custom-owner" {
		t.Fatalf("expected custom-owner, got %q", got)
	}
}

// ── pluginStatus String/Badge ─────────────────────────────────

func TestPluginStatusStringCov4(t *testing.T) {
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

func TestPluginStatusBadgeCov4(t *testing.T) {
	// Just verify they return non-empty strings
	for _, s := range []pluginStatus{statusNotInstalled, statusInstalled, statusRunning} {
		badge := s.Badge()
		if badge == "" {
			t.Errorf("Badge() for %d is empty", s)
		}
	}
}

// ── settingsMenuItems ─────────────────────────────────────────

func TestSettingsMenuItemsCov4(t *testing.T) {
	items := settingsMenuItems()
	if len(items) < 10 {
		t.Fatalf("expected at least 10 items, got %d", len(items))
	}
	// Check first and last
	if items[0].label == "" {
		t.Fatal("first item has empty label")
	}
}

// ── Logo / SmallLogo ──────────────────────────────────────────

func TestLogoCov4(t *testing.T) {
	logo := Logo()
	if logo == "" || len(logo) < 10 {
		t.Fatal("expected non-empty logo")
	}
}

// ── DefaultIndexDir / DefaultModelsDir / configPath ───────────

func TestDefaultIndexDirCov4(t *testing.T) {
	dir := DefaultIndexDir()
	if dir == "" {
		t.Fatal("expected non-empty")
	}
}

func TestDefaultModelsDirCov4(t *testing.T) {
	dir := DefaultModelsDir()
	if dir == "" {
		t.Fatal("expected non-empty")
	}
}

func TestConfigPathCov4(t *testing.T) {
	path := configPath()
	if path == "" {
		t.Fatal("expected non-empty")
	}
}

// ── pickPreferred ─────────────────────────────────────────────

func TestPickPreferredCov4(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3:latest"},
		{Name: "bge-m3:latest"},
		{Name: "gemma3:8b"},
	}
	// Should match first preferred prefix
	got := pickPreferred(models, []string{"bge-m3", "nomic-embed-text"})
	if got != "bge-m3:latest" {
		t.Fatalf("expected bge-m3:latest, got %q", got)
	}

	// No match → falls back to first
	got = pickPreferred(models, []string{"nonexistent"})
	if got != "llama3:latest" {
		t.Fatalf("expected fallback to first, got %q", got)
	}
}

// ── pickBestModels ────────────────────────────────────────────

func TestPickBestModelsCov4(t *testing.T) {
	result := &OnboardResult{}
	models := []ModelInfo{
		{Name: "bge-m3:latest", Size: "1.0 GB"},
		{Name: "llama3:8b", Size: "5.0 GB"},
		{Name: "jina-reranker-v2-base-multilingual", Size: "500 MB"},
	}
	pickBestModels(result, models)
	if result.EmbeddingModel == "" {
		t.Fatal("expected embedding model to be set")
	}
}

// ── jsonUnmarshal / jsonMarshalIndent ─────────────────────────

func TestJsonUnmarshalCov4(t *testing.T) {
	var m map[string]any
	err := jsonUnmarshal([]byte(`{"key": "value"}`), &m)
	if err != nil {
		t.Fatal(err)
	}
	if m["key"] != "value" {
		t.Fatal("unexpected value")
	}
}

func TestJsonMarshalIndentCov4(t *testing.T) {
	data := map[string]string{"key": "value"}
	b, err := jsonMarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty")
	}
}

// ── ExpandPath ────────────────────────────────────────────────

func TestExpandPathCov4(t *testing.T) {
	// Other tests in this package modify HOME (os.Setenv / os.Unsetenv)
	// which races with os.UserHomeDir() under -race. Guard tilde-only case.
	home, _ := os.UserHomeDir()

	tests := []struct {
		in   string
		want bool // just check non-empty
	}{
		{"", false},
		{"~", home != ""}, // returns home dir; empty if HOME unset
		{"~/docs", true},  // always non-empty: at worst returns "docs"
		{"/absolute/path", true},
		{"relative/path", true},
	}
	for _, tt := range tests {
		got := ExpandPath(tt.in)
		if tt.want && got == "" {
			t.Errorf("ExpandPath(%q) returned empty", tt.in)
		}
		if !tt.want && got != "" {
			t.Errorf("ExpandPath(%q) = %q, want empty", tt.in, got)
		}
	}
}
