package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── DefaultIndexDir / DefaultModelsDir ─────────────────────────

func TestDefaultIndexDirNonEmpty(t *testing.T) {
	d := DefaultIndexDir()
	if d == "" {
		t.Error("should not be empty")
	}
	if !strings.Contains(d, ".gleann") || !strings.Contains(d, "indexes") {
		t.Errorf("unexpected path: %s", d)
	}
}

func TestDefaultModelsDirNonEmpty(t *testing.T) {
	d := DefaultModelsDir()
	if d == "" {
		t.Error("should not be empty")
	}
	if !strings.Contains(d, "models") {
		t.Errorf("unexpected path: %s", d)
	}
}

// ── ExpandPath ─────────────────────────────────────────────────

func TestExpandPathEmptyCI(t *testing.T) {
	if ExpandPath("") != "" {
		t.Error("empty should return empty")
	}
}

func TestExpandPathTildeOnlyCI(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandPath("~"); got != home {
		t.Errorf("got %q, want %q", got, home)
	}
}

func TestExpandPathTildeSlashCI(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := ExpandPath("~/docs")
	want := filepath.Join(home, "docs")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandPathAbsoluteCI(t *testing.T) {
	got := ExpandPath("/usr/local/bin")
	if got != filepath.Clean("/usr/local/bin") {
		t.Errorf("got %q", got)
	}
}

func TestExpandPathRelativeCI(t *testing.T) {
	got := ExpandPath("./relative/path")
	if got != filepath.Clean("./relative/path") {
		t.Errorf("got %q", got)
	}
}

// ── SaveConfig / LoadSavedConfig ───────────────────────────────

func TestSaveAndLoadConfigCI(t *testing.T) {
	// Use temp dir as home
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg := OnboardResult{
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        "http://localhost:11434",
		LLMProvider:       "ollama",
		LLMModel:          "gemma3:4b",
		IndexDir:          filepath.Join(tmpDir, ".gleann", "indexes"),
		Completed:         true,
		Temperature:       0.7,
		MaxTokens:         4096,
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded := LoadSavedConfig()
	if loaded == nil {
		t.Fatal("LoadSavedConfig returned nil")
	}
	if loaded.EmbeddingModel != "bge-m3" {
		t.Errorf("EmbeddingModel = %q", loaded.EmbeddingModel)
	}
	if loaded.LLMModel != "gemma3:4b" {
		t.Errorf("LLMModel = %q", loaded.LLMModel)
	}
	if !loaded.Completed {
		t.Error("Completed should be true")
	}
}

func TestLoadSavedConfigNoFileCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	if cfg := LoadSavedConfig(); cfg != nil {
		t.Error("should return nil when no config file")
	}
}

func TestLoadSavedConfigInvalidJSONCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	dir := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{invalid"), 0o644)

	if cfg := LoadSavedConfig(); cfg != nil {
		t.Error("should return nil for invalid JSON")
	}
}

// ── UpdateConfig ───────────────────────────────────────────────

func TestUpdateConfigCreatesNewCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	err := UpdateConfig(func(cfg *OnboardResult) {
		cfg.LLMModel = "phi-4"
		cfg.Completed = true
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	loaded := LoadSavedConfig()
	if loaded == nil {
		t.Fatal("should be loadable")
	}
	if loaded.LLMModel != "phi-4" {
		t.Errorf("LLMModel = %q", loaded.LLMModel)
	}
}

func TestUpdateConfigModifiesExistingCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_ = SaveConfig(OnboardResult{LLMModel: "a", Completed: true})

	err := UpdateConfig(func(cfg *OnboardResult) {
		cfg.LLMModel = "b"
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded := LoadSavedConfig()
	if loaded.LLMModel != "b" {
		t.Errorf("LLMModel = %q", loaded.LLMModel)
	}
	if !loaded.Completed {
		t.Error("Completed lost during update")
	}
}

// ── Install helpers ────────────────────────────────────────────

func TestSharedLibNamesNotEmpty(t *testing.T) {
	libs := sharedLibNames()
	if len(libs) == 0 {
		t.Error("should return at least 2 lib names")
	}
	for _, lib := range libs {
		if !strings.Contains(lib, "faiss") {
			t.Errorf("unexpected lib name: %s", lib)
		}
	}
}

func TestInstallDirsNotEmpty(t *testing.T) {
	dirs := installDirs()
	if len(dirs) == 0 {
		t.Error("should return at least 1 dir")
	}
}

func TestIsWritableTempDir(t *testing.T) {
	tmp := t.TempDir()
	if !isWritable(tmp) {
		t.Error("temp dir should be writable")
	}
}

func TestIsWritableNonexistent(t *testing.T) {
	// Nonexistent directory with no parent — should return false
	if isWritable("/nonexistent/deep/path/that/does/not/exist") {
		t.Error("should not be writable")
	}
}

func TestBashCompletionNotEmpty(t *testing.T) {
	s := BashCompletion()
	if s == "" || !strings.Contains(s, "gleann") {
		t.Error("should contain completion script")
	}
}

func TestZshCompletionNotEmpty(t *testing.T) {
	s := ZshCompletion()
	if s == "" || !strings.Contains(s, "gleann") {
		t.Error("should contain completion script")
	}
}

func TestFishCompletionNotEmpty(t *testing.T) {
	s := FishCompletion()
	if s == "" || !strings.Contains(s, "gleann") {
		t.Error("should contain completion script")
	}
}

// ── pickBestModels ─────────────────────────────────────────────

func TestPickBestModelsEmb(t *testing.T) {
	result := &OnboardResult{EmbeddingModel: "default"}
	models := []ModelInfo{
		{Name: "some-model", Size: "1GB"},
		{Name: "bge-m3", Size: "500MB"},
	}
	pickBestModels(result, models)
	if result.EmbeddingModel != "bge-m3" {
		t.Errorf("got %q", result.EmbeddingModel)
	}
}

func TestPickPreferredFirstCI(t *testing.T) {
	models := []ModelInfo{{Name: "llama3"}, {Name: "phi-4"}}
	got := pickPreferred(models, []string{"gemma3", "llama3"})
	if got != "llama3" {
		t.Errorf("got %q", got)
	}
}

func TestPickPreferredFallbackCI(t *testing.T) {
	models := []ModelInfo{{Name: "unknown-model"}}
	got := pickPreferred(models, []string{"missing1", "missing2"})
	if got != "unknown-model" {
		t.Errorf("got %q, want fallback to first model", got)
	}
}

// ── IsSetupNeeded ──────────────────────────────────────────────

func TestIsSetupNeededNoConfigCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	if !IsSetupNeeded() {
		t.Error("should need setup with no config")
	}
}

func TestIsSetupNeededIncompleteCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_ = SaveConfig(OnboardResult{Completed: false})
	if !IsSetupNeeded() {
		t.Error("should need setup with incomplete config")
	}
}

func TestIsSetupNeededCompletedCI(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_ = SaveConfig(OnboardResult{Completed: true})
	if IsSetupNeeded() {
		t.Error("should not need setup with completed config")
	}
}

// ── InstallBinary ──────────────────────────────────────────────

func TestInstallBinaryToTempDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	targetDir := t.TempDir()
	err := InstallBinary(targetDir)
	if err != nil {
		t.Fatalf("InstallBinary: %v", err)
	}
	dst := filepath.Join(targetDir, "gleann")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("binary not found at %s", dst)
	}
}

// ── RemoveCompletions ──────────────────────────────────────────

func TestRemoveCompletionsNoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	removed := RemoveCompletions()
	if len(removed) != 0 {
		t.Error("should remove nothing")
	}
}

// ── InstallCompletions + RemoveCompletions ─────────────────────

func TestInstallAndRemoveCompletions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("completions not supported on Windows")
	}
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	installed := InstallCompletions()
	if len(installed) == 0 {
		t.Error("should install at least one completion")
	}

	removed := RemoveCompletions()
	if len(removed) == 0 {
		t.Error("should remove installed completions")
	}
}

// ── ensureSourceLine ───────────────────────────────────────────

func TestEnsureSourceLineCreatesFile(t *testing.T) {
	tmp := t.TempDir()
	rcFile := filepath.Join(tmp, ".bashrc")
	compPath := filepath.Join(tmp, "completion")

	ensureSourceLine(rcFile, compPath)

	data, err := os.ReadFile(rcFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), compPath) {
		t.Error("should contain source line")
	}

	// Calling again should not duplicate
	ensureSourceLine(rcFile, compPath)
	data2, _ := os.ReadFile(rcFile)
	if strings.Count(string(data2), compPath) > 1 {
		t.Error("should not duplicate source line")
	}
}
