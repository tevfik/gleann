package configpath

import (
	"os"
	"path/filepath"
)

// GetConfigDir returns the path to the gleann configuration directory.
// It prioritizes the GLEANN_CONFIG_DIR environment variable, falling back
// to ~/.gleann if the environment variable is not set.
func GetConfigDir() (string, error) {
	if envDir := os.Getenv("GLEANN_CONFIG_DIR"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gleann"), nil
}
