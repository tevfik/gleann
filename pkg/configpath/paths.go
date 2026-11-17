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

// ModelsDir returns the directory where gleann stores downloaded models:
// <config dir>/models. It follows GLEANN_CONFIG_DIR like GetConfigDir; if
// the config dir cannot be determined it falls back to .gleann/models.
func ModelsDir() string {
	dir, err := GetConfigDir()
	if err != nil {
		return filepath.Join(".gleann", "models")
	}
	return filepath.Join(dir, "models")
}
