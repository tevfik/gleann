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

// SaveConfig persists the configuration to disk.
// Exported so chat settings can call it after applying changes.
func SaveConfig(r OnboardResult) error {
	path := configPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
