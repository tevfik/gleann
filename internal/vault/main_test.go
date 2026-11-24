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
	unsetGleannDirEnv()
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// unsetGleannDirEnv removes the directory overrides the product code reads
// (grep os.Getenv("GLEANN_ ... _DIR)): a developer shell exporting one would
// send the tests to the real directory even with a private HOME. The list is
// explicit because child processes of the tests pass their own GLEANN_*_DIR
// variables through TestMain; the guard test fails on any directory override
// that is not listed here and points outside the private home.
func unsetGleannDirEnv() {
	for _, name := range []string{
		"GLEANN_CONFIG_DIR", "GLEANN_MEMORY_DIR", "GLEANN_MULTIMODAL_CACHE_DIR",
		"GLEANN_CONVERSATIONS_DIR", "GLEANN_INDEX_DIR", "GLEANN_PACKS_DIR", "GLEANN_AUTO_INDEX_DIRS",
	} {
		os.Unsetenv(name)
	}
}
