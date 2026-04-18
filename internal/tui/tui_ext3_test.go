package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── BashCompletion / ZshCompletion / FishCompletion ────────────

func TestBashCompletionExt3(t *testing.T) {
	s := BashCompletion()
	if !strings.Contains(s, "_gleann") {
		t.Fatal("should contain _gleann function")
	}
	if !strings.Contains(s, "complete -F _gleann gleann") {
		t.Fatal("should register completion")
	}
}

func TestZshCompletionExt3(t *testing.T) {
	s := ZshCompletion()
	if !strings.Contains(s, "#compdef gleann") {
		t.Fatal("should contain zsh compdef")
	}
	if !strings.Contains(s, "_gleann") {
		t.Fatal("should contain _gleann function")
	}
}

func TestFishCompletionExt3(t *testing.T) {
	s := FishCompletion()
	if !strings.Contains(s, "complete -c gleann") {
		t.Fatal("should contain fish complete")
	}
}

// ── sharedLibNames ─────────────────────────────

func TestSharedLibNamesExt3(t *testing.T) {
	libs := sharedLibNames()
	if len(libs) == 0 {
		t.Fatal("should return library names")
	}
	for _, lib := range libs {
		if lib == "" {
			t.Fatal("empty library name")
		}
	}
}

// ── installDirs ────────────────────────────────

func TestInstallDirsExt3(t *testing.T) {
	dirs := installDirs()
	if len(dirs) == 0 {
		t.Fatal("should return at least one directory")
	}
}

// ── isWritable ─────────────────────────────────

func TestIsWritableExistingDir(t *testing.T) {
	tmp := t.TempDir()
	if !isWritable(tmp) {
		t.Fatal("temp dir should be writable")
	}
}

func TestIsWritableNonExistentDir(t *testing.T) {
	// Non-existent dir with non-existent parent should not be writable.
	if isWritable("/nonexistent/deeply/nested/path") {
		t.Fatal("non-existent parent should not be writable")
	}
}

func TestIsWritableNonExistentParent(t *testing.T) {
	if isWritable("/nonexistent/deeply/nested/path") {
		t.Fatal("non-existent parent should not be writable")
	}
}

// ── InstallBinary ──────────────────────────────

func TestInstallBinaryExt3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	tmp := t.TempDir()
	// This will try to copy the test binary.
	err := InstallBinary(tmp)
	if err != nil {
		t.Logf("InstallBinary error (expected in test env): %v", err)
	}
}

// ── RemoveCompletions ──────────────────────────

func TestRemoveCompletionsExt3(t *testing.T) {
	// No completion files installed in test env, should return empty.
	removed := RemoveCompletions()
	// Just verify it doesn't panic and returns a slice.
	_ = removed
}

// ── ensureSourceLine ───────────────────────────

func TestEnsureSourceLineNew(t *testing.T) {
	tmp := t.TempDir()
	rcFile := filepath.Join(tmp, ".bashrc")
	completionPath := filepath.Join(tmp, "gleann_completion")

	ensureSourceLine(rcFile, completionPath)

	data, err := os.ReadFile(rcFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), completionPath) {
		t.Fatal("should add source line")
	}
}

func TestEnsureSourceLineAlreadyPresent(t *testing.T) {
	tmp := t.TempDir()
	rcFile := filepath.Join(tmp, ".bashrc")
	completionPath := filepath.Join(tmp, "gleann_completion")

	// Write existing line.
	os.WriteFile(rcFile, []byte("source "+completionPath+" # gleann completion\n"), 0o644)

	ensureSourceLine(rcFile, completionPath)

	data, _ := os.ReadFile(rcFile)
	count := strings.Count(string(data), completionPath)
	if count > 1 {
		t.Fatal("should not duplicate source line")
	}
}

// ── copySharedLibs ─────────────────────────────

func TestCopySharedLibsNoLibs(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "gleann")
	os.WriteFile(exe, []byte("fake"), 0o755)
	target := t.TempDir()
	// Should not panic when no shared libs exist.
	copySharedLibs(exe, target)
}

func TestCopySharedLibsWithLib(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	exeDir := t.TempDir()
	exe := filepath.Join(exeDir, "gleann")
	os.WriteFile(exe, []byte("fake"), 0o755)

	// Create a fake shared lib.
	libs := sharedLibNames()
	if len(libs) > 0 {
		os.WriteFile(filepath.Join(exeDir, libs[0]), []byte("library"), 0o755)
	}

	target := t.TempDir()
	copySharedLibs(exe, target)

	if len(libs) > 0 {
		if _, err := os.Stat(filepath.Join(target, libs[0])); err != nil {
			t.Fatal("shared lib should be copied")
		}
	}
}

// ── findClosestFloat ───────────────────────────

func TestFindClosestFloatExact(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, 0.5)
	if temperaturePresets[idx] != 0.5 {
		t.Errorf("expected 0.5, got %.1f", temperaturePresets[idx])
	}
}

func TestFindClosestFloatBetween(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, 0.55)
	got := temperaturePresets[idx]
	if got != 0.5 && got != 0.6 {
		t.Errorf("expected 0.5 or 0.6, got %.1f", got)
	}
}

func TestFindClosestFloatMin(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, -1.0)
	if idx != 0 {
		t.Errorf("expected first, got %d", idx)
	}
}

func TestFindClosestFloatMax(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, 999.0)
	if idx != len(temperaturePresets)-1 {
		t.Errorf("expected last, got %d", idx)
	}
}

// ── findClosestInt ─────────────────────────────

func TestFindClosestIntExact(t *testing.T) {
	idx := findClosestInt(maxTokensPresets, 2048)
	if maxTokensPresets[idx] != 2048 {
		t.Errorf("expected 2048, got %d", maxTokensPresets[idx])
	}
}

func TestFindClosestIntBetween(t *testing.T) {
	idx := findClosestInt(maxTokensPresets, 1500)
	got := maxTokensPresets[idx]
	if got != 1024 && got != 2048 {
		t.Errorf("expected 1024 or 2048, got %d", got)
	}
}

func TestFindClosestIntMin(t *testing.T) {
	idx := findClosestInt(maxTokensPresets, 1)
	if idx != 0 {
		t.Errorf("expected first, got %d", idx)
	}
}

func TestFindClosestIntMax(t *testing.T) {
	idx := findClosestInt(maxTokensPresets, 999999)
	if idx != len(maxTokensPresets)-1 {
		t.Errorf("expected last, got %d", idx)
	}
}

// ── intAbs ─────────────────────────────────────

func TestIntAbsPositive(t *testing.T) {
	if intAbs(5) != 5 {
		t.Fatal()
	}
}

func TestIntAbsNegative(t *testing.T) {
	if intAbs(-5) != 5 {
		t.Fatal()
	}
}

func TestIntAbsZero(t *testing.T) {
	if intAbs(0) != 0 {
		t.Fatal()
	}
}

// ── OnboardResult settingsMenuItems ────────────

func TestSettingsMenuItemsExt3(t *testing.T) {
	items := settingsMenuItems()
	if len(items) == 0 {
		t.Fatal("should return items")
	}
	for _, item := range items {
		if item.label == "" {
			t.Fatal("empty label")
		}
	}
}

// ── OnboardModel prevPhase ─────────────────────

func TestPrevPhaseKnown(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseEmbModel
	p := m.prevPhase()
	if p == int(phaseEmbModel) {
		t.Fatal("should return a different phase")
	}
}

func TestPrevPhaseFirst(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseQuickOrAdv
	p := m.prevPhase()
	// Phase 0 should go back to menu or stay.
	_ = p
}

// ── buildInstallOptions ────────────────────────

func TestBuildInstallOptionsExt3(t *testing.T) {
	opts := buildInstallOptions()
	if len(opts) == 0 {
		t.Fatal("should return options")
	}
}

// ── NewOnboardModel ────────────────────────────

func TestNewOnboardModelNotNil(t *testing.T) {
	m := NewOnboardModel()
	if m.phase != phaseQuickOrAdv {
		t.Errorf("initial phase = %d, expected %d", m.phase, phaseQuickOrAdv)
	}
	if m.cancelled {
		t.Fatal("should not be cancelled")
	}
}

func TestNewOnboardModelWithConfigExt3(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test-model",
		LLMProvider:       "openai",
		LLMModel:          "gpt-4",
		OllamaHost:        "http://localhost:11434",
	}
	m := NewOnboardModelWithConfig(cfg)
	if m.existingCfg == nil {
		t.Fatal("existing config should be set")
	}
}

// ── openAIBaseURL ──────────────────────────────

func TestOpenAIBaseURLExt3(t *testing.T) {
	m := NewOnboardModel()
	url := m.openAIBaseURL()
	if url == "" {
		t.Fatal("should return a URL")
	}
	if !strings.HasPrefix(url, "http") {
		t.Errorf("expected http URL, got %q", url)
	}
}
