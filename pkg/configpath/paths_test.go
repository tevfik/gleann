package configpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetConfigDir_EnvSet(t *testing.T) {
	expected := "/tmp/test-gleann-config"
	t.Setenv("GLEANN_CONFIG_DIR", expected)

	dir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != expected {
		t.Errorf("got %q, want %q", dir, expected)
	}
}

func TestGetConfigDir_Default(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", "")

	dir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("could not get home dir: %v", err)
	}
	expected := filepath.Join(home, ".gleann")
	if dir != expected {
		t.Errorf("got %q, want %q", dir, expected)
	}
}

func TestModelsDir_EnvSet(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", "/tmp/test-gleann-config")

	expected := filepath.Join("/tmp/test-gleann-config", "models")
	if dir := ModelsDir(); dir != expected {
		t.Errorf("got %q, want %q", dir, expected)
	}
}

func TestModelsDir_Default(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("could not get home dir: %v", err)
	}
	expected := filepath.Join(home, ".gleann", "models")
	if dir := ModelsDir(); dir != expected {
		t.Errorf("got %q, want %q", dir, expected)
	}
}

func TestModelsDir_Fallback(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", "")
	// Make os.UserHomeDir fail on both Unix ($HOME) and Windows (%USERPROFILE%).
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	expected := filepath.Join(".gleann", "models")
	if dir := ModelsDir(); dir != expected {
		t.Errorf("got %q, want %q", dir, expected)
	}
}
