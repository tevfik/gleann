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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	if cfg := LoadSavedConfig(); cfg != nil {
		t.Error("should return nil when no config file")
	}
}

func TestLoadSavedConfigInvalidJSONCI(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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
	if isWritable(unwritableMissingPath(t)) {
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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	if !IsSetupNeeded() {
		t.Error("should need setup with no config")
	}
}

func TestIsSetupNeededIncompleteCI(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	_ = SaveConfig(OnboardResult{Completed: false})
	if !IsSetupNeeded() {
		t.Error("should need setup with incomplete config")
	}
}

func TestIsSetupNeededCompletedCI(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

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

func TestSanitizeConfig(t *testing.T) {
	// Case 1: EIF + Ollama (OllamaHost preserved, OpenAI/Anthropic wiped)
	cfg := &OnboardResult{
		EmbeddingProvider: "eif",
		LLMProvider:       "ollama",
		OllamaHost:        "http://localhost:11434",
		OllamaAPIKey:      "ollama-secret",
		OpenAIBaseURL:     "https://api.openai.com",
		OpenAIKey:         "sk-test",
		AnthropicKey:      "ant-test",
	}
	SanitizeConfig(cfg)
	if cfg.OllamaHost != "http://localhost:11434" {
		t.Errorf("OllamaHost should be preserved, got %q", cfg.OllamaHost)
	}
	if cfg.OllamaAPIKey != "ollama-secret" {
		t.Errorf("OllamaAPIKey should be preserved, got %q", cfg.OllamaAPIKey)
	}
	if cfg.OpenAIBaseURL != "" {
		t.Errorf("OpenAIBaseURL should be stripped for EIF+Ollama, got %q", cfg.OpenAIBaseURL)
	}
	if cfg.OpenAIKey != "" {
		t.Errorf("OpenAIKey should be stripped for EIF+Ollama, got %q", cfg.OpenAIKey)
	}
	if cfg.AnthropicKey != "" {
		t.Errorf("AnthropicKey should be stripped for EIF+Ollama, got %q", cfg.AnthropicKey)
	}

	// Case 2: EIF + Llamacpp / local (all remote hosts and keys wiped)
	cfg2 := &OnboardResult{
		EmbeddingProvider: "eif",
		LLMProvider:       "llamacpp",
		OllamaHost:        "http://localhost:11434",
		OpenAIBaseURL:     "https://api.openai.com",
		AnthropicKey:      "ant-test",
	}
	SanitizeConfig(cfg2)
	if cfg2.OllamaHost != "" || cfg2.OpenAIBaseURL != "" || cfg2.AnthropicKey != "" {
		t.Errorf("all remote endpoints/keys should be stripped for EIF+Llamacpp, got %+v", cfg2)
	}

	// Case 3: OpenAI provider (OpenAI credentials preserved)
	cfg3 := &OnboardResult{
		EmbeddingProvider: "openai",
		LLMProvider:       "openai",
		OpenAIBaseURL:     "https://api.openai.com",
		OpenAIKey:         "sk-valid",
		OllamaHost:        "http://localhost:11434",
	}
	SanitizeConfig(cfg3)
	if cfg3.OpenAIBaseURL != "https://api.openai.com" || cfg3.OpenAIKey != "sk-valid" {
		t.Errorf("OpenAI settings should be preserved, got %+v", cfg3)
	}
	if cfg3.OllamaHost != "" {
		t.Errorf("OllamaHost should be stripped when only OpenAI is used, got %q", cfg3.OllamaHost)
	}
}

func TestSaveConfig_DeepMergePreservesExistingFields(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	initial := OnboardResult{
		EmbeddingProvider:  "eif",
		EmbeddingModel:     "e5-small",
		CodeEmbeddingModel: "granite-30m",
		ServerAddr:         ":8081",
		ServerEnabled:      true,
		Completed:          true,
	}
	if err := SaveConfig(initial); err != nil {
		t.Fatalf("initial SaveConfig failed: %v", err)
	}

	// Update only LLM model via partial SaveConfig
	partial := OnboardResult{
		LLMProvider: "ollama",
		LLMModel:    "llama3.2:3b",
	}
	if err := SaveConfig(partial); err != nil {
		t.Fatalf("partial SaveConfig failed: %v", err)
	}

	loaded := LoadSavedConfig()
	if loaded == nil {
		t.Fatal("expected config to load")
	}
	if loaded.ServerAddr != ":8081" {
		t.Errorf("expected ServerAddr :8081 to be preserved, got %q", loaded.ServerAddr)
	}
	if loaded.CodeEmbeddingModel != "granite-30m" {
		t.Errorf("expected CodeEmbeddingModel granite-30m to be preserved, got %q", loaded.CodeEmbeddingModel)
	}
	if loaded.EmbeddingProvider != "eif" {
		t.Errorf("expected EmbeddingProvider eif to be preserved, got %q", loaded.EmbeddingProvider)
	}
	if loaded.LLMModel != "llama3.2:3b" {
		t.Errorf("expected LLMModel llama3.2:3b to be set, got %q", loaded.LLMModel)
	}
}
