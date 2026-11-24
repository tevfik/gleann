package multimodal

import (
	"os"
	"testing"
)

// TestMain gives the package a private home directory. The description cache
// falls back to $HOME/.gleann/cache/multimodal, so tests that process files
// without setting a cache directory would otherwise write into the real one.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "gleann-multimodal-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
