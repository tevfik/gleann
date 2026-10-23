package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultIndexDir returns the platform-appropriate default index directory.
//   - Linux/Mac: /home/user/.gleann/indexes
//   - Windows:   C:\Users\user\.gleann\indexes
func DefaultIndexDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "indexes")
}

// DefaultModelsDir returns the platform-appropriate default models directory.
//   - Linux/Mac: /home/user/.gleann/models
//   - Windows:   C:\Users\user\.gleann\models
func DefaultModelsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "models")
}

// ExpandPath expands ~ and ~/ prefixes to the user's home directory
// and cleans the path using filepath.Clean for cross-platform correctness.
// On Windows, forward slashes in the path are converted to backslashes.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}

	// Expand ~ prefix.
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}

	// On Windows, convert forward slashes from Linux-originated configs.
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "/", "\\")
	}

	return filepath.Clean(p)
}

// configPath returns the platform-appropriate config file path.
func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "config.json")
}

// SanitizeConfig strips provider-specific credentials/endpoints when that provider is not selected.
func SanitizeConfig(r *OnboardResult) {
	if r == nil {
		return
	}
	embIsOllama := strings.EqualFold(r.EmbeddingProvider, "ollama")
	llmIsOllama := strings.EqualFold(r.LLMProvider, "ollama")
	if r.EmbeddingProvider != "" || r.LLMProvider != "" {
		if !embIsOllama && !llmIsOllama {
			r.OllamaHost = ""
			r.OllamaAPIKey = ""
		}
	}

	embIsOpenAI := strings.EqualFold(r.EmbeddingProvider, "openai")
	llmIsOpenAI := strings.EqualFold(r.LLMProvider, "openai")
	if r.EmbeddingProvider != "" || r.LLMProvider != "" {
		if !embIsOpenAI && !llmIsOpenAI {
			r.OpenAIKey = ""
			r.OpenAIBaseURL = ""
		}
	}

	llmIsAnthropic := strings.EqualFold(r.LLMProvider, "anthropic")
	if r.LLMProvider != "" {
		if !llmIsAnthropic {
			r.AnthropicKey = ""
		}
	}
}

// mergeConfig overlays non-empty incoming values on top of existing configuration.
func mergeConfig(existing, incoming *OnboardResult) OnboardResult {
	if existing == nil {
		return *incoming
	}
	merged := *existing

	// Core providers & models
	if incoming.EmbeddingProvider != "" {
		merged.EmbeddingProvider = incoming.EmbeddingProvider
	}
	if incoming.EmbeddingModel != "" {
		merged.EmbeddingModel = incoming.EmbeddingModel
	}
	if incoming.CodeEmbeddingModel != "" {
		merged.CodeEmbeddingModel = incoming.CodeEmbeddingModel
	}
	if incoming.DocEmbeddingModel != "" {
		merged.DocEmbeddingModel = incoming.DocEmbeddingModel
	}
	if incoming.OllamaHost != "" {
		merged.OllamaHost = incoming.OllamaHost
	}
	if incoming.OllamaAPIKey != "" {
		merged.OllamaAPIKey = incoming.OllamaAPIKey
	}
	if incoming.OpenAIKey != "" {
		merged.OpenAIKey = incoming.OpenAIKey
	}
	if incoming.OpenAIBaseURL != "" {
		merged.OpenAIBaseURL = incoming.OpenAIBaseURL
	}
	if incoming.AnthropicKey != "" {
		merged.AnthropicKey = incoming.AnthropicKey
	}
	if incoming.LLMProvider != "" {
		merged.LLMProvider = incoming.LLMProvider
	}
	if incoming.LLMModel != "" {
		merged.LLMModel = incoming.LLMModel
	}
	if incoming.IndexDir != "" {
		merged.IndexDir = incoming.IndexDir
	}
	if incoming.Backend != "" {
		merged.Backend = incoming.Backend
	}
	if incoming.MultimodalModel != "" {
		merged.MultimodalModel = incoming.MultimodalModel
	}

	// Booleans & toggles
	if incoming.Completed {
		merged.Completed = true
		merged.RerankEnabled = incoming.RerankEnabled
		merged.RerankModel = incoming.RerankModel
		merged.ServerEnabled = incoming.ServerEnabled
		merged.MCPEnabled = incoming.MCPEnabled
		if incoming.A2AEnabled != nil {
			merged.A2AEnabled = incoming.A2AEnabled
		}
	} else {
		if incoming.RerankEnabled {
			merged.RerankEnabled = true
			merged.RerankModel = incoming.RerankModel
		}
		if incoming.ServerEnabled {
			merged.ServerEnabled = true
		}
		if incoming.MCPEnabled {
			merged.MCPEnabled = true
		}
		if incoming.A2AEnabled != nil {
			merged.A2AEnabled = incoming.A2AEnabled
		}
	}

	if incoming.ServerAddr != "" {
		merged.ServerAddr = incoming.ServerAddr
	}
	if incoming.InstallPath != "" {
		merged.InstallPath = incoming.InstallPath
	}
	if incoming.InstallCompletions {
		merged.InstallCompletions = true
	}
	if incoming.Uninstall {
		merged.Uninstall = incoming.Uninstall
		merged.UninstallData = incoming.UninstallData
	}
	if incoming.LlamaCPPConfig.ModelStorageDir != "" {
		merged.LlamaCPPConfig = incoming.LlamaCPPConfig
	}
	if incoming.SystemPrompt != "" {
		merged.SystemPrompt = incoming.SystemPrompt
	}
	if incoming.Temperature > 0 {
		merged.Temperature = incoming.Temperature
	}
	if incoming.RepeatPenalty > 0 {
		merged.RepeatPenalty = incoming.RepeatPenalty
	}
	if incoming.MaxTokens > 0 {
		merged.MaxTokens = incoming.MaxTokens
	}
	if incoming.TopK > 0 {
		merged.TopK = incoming.TopK
	}
	if len(incoming.Roles) > 0 {
		merged.Roles = incoming.Roles
	}
	if len(incoming.FormatText) > 0 {
		merged.FormatText = incoming.FormatText
	}
	return merged
}

// SaveConfig persists the configuration to disk with deep merge.
// Exported so chat settings can call it after applying changes.
func SaveConfig(r OnboardResult) error {
	path := configPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	existing := LoadSavedConfig()
	merged := mergeConfig(existing, &r)

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return writeSecretFile(path, data)
}

// writeSecretFile writes data to path with mode 0600. The config holds API
// keys, so it must not be readable by other users; os.WriteFile only applies
// the mode when it creates the file, so existing files are tightened too.
func writeSecretFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// UpdateConfig reads the existing config, applies the mutator function,
// and writes it back. Creates a new config if none exists.
func UpdateConfig(mutate func(cfg *OnboardResult)) error {
	cfg := LoadSavedConfig()
	if cfg == nil {
		cfg = &OnboardResult{}
	}
	mutate(cfg)
	return SaveConfig(*cfg)
}

// LoadSavedConfig reads the saved config. Returns nil if not found.
// Paths in the config are expanded for the current platform.
func LoadSavedConfig() *OnboardResult {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	var r OnboardResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	// Expand paths for cross-platform compatibility.
	if r.IndexDir != "" {
		r.IndexDir = ExpandPath(r.IndexDir)
	}
	return &r
}
