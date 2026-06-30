package tui

import (
	"os"
	"testing"
)

// TestMain ensures all TUI tests run with GLEANN_TEST_MODE=true so that
// NewChatModel() skips real Ollama HTTP calls and bbolt DB access,
// preventing test timeouts caused by network latency and file locks.
func TestMain(m *testing.M) {
	os.Setenv("GLEANN_TEST_MODE", "true")
	code := m.Run()
	os.Unsetenv("GLEANN_TEST_MODE")
	os.Exit(code)
}
