package vault

import (
	"os"
	"testing"
)

// TestMain gives the package a private home directory. DefaultDBPath creates
// $HOME/.gleann/vault, so a test that calls it would otherwise touch the real
// one.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "gleann-vault-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
