package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain ensures all TUI tests run with GLEANN_TEST_MODE=true so that
// NewChatModel() skips real Ollama HTTP calls and bbolt DB access,
// preventing test timeouts caused by network latency and file locks.
//
// It also points HOME (and USERPROFILE on Windows) at a throw-away directory
// for the whole package. Several chat tests exercise slash commands and the
// settings panel, which persist via UpdateConfig to ~/.gleann/config.json;
// without this they overwrote the developer's real config (e.g. "/penalty 1.4"
// in TestChatModelRepeatPenaltySlashCommand). Tests that call t.Setenv("HOME")
// still get their own per-test directory.
func TestMain(m *testing.M) {
	os.Setenv("GLEANN_TEST_MODE", "true")
	home, err := os.MkdirTemp("", "gleann-tui-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp HOME: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.Unsetenv("GLEANN_TEST_MODE")
	_ = os.RemoveAll(home)
	os.Exit(code)
}
