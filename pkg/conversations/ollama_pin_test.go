package conversations

import (
	"os"
	"testing"

	"github.com/tevfik/gleann/internal/testenv"
)

// TestMain pins OLLAMA_HOST to a dead address, so no test falls through to
// localhost:11434 (a real Ollama on a developer machine).
func TestMain(m *testing.M) {
	testenv.PinOllamaHost()
	os.Exit(m.Run())
}

func TestOllamaHostPinned(t *testing.T) { testenv.AssertPinned(t) }
