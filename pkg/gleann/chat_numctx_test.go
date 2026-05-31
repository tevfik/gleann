package gleann

import "testing"

// TestOllamaContextWindow_NoLimit verifies that --no-limit (MaxTokens=0)
// widens the Ollama context window to the documented default so that large
// RAG payloads are not silently truncated to the model's small default.
func TestOllamaContextWindow_NoLimit(t *testing.T) {
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
	if got := ollamaContextWindow(0); got != DefaultOllamaContextWindow {
		t.Errorf("ollamaContextWindow(0) = %d, want %d", got, DefaultOllamaContextWindow)
	}
}

// TestOllamaContextWindow_Budgeted verifies that the helper stays silent
// (returns 0 → option omitted) for the normal max-tokens path, so we keep
// backwards compatibility with the model's default num_ctx.
func TestOllamaContextWindow_Budgeted(t *testing.T) {
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
	if got := ollamaContextWindow(2048); got != 0 {
		t.Errorf("ollamaContextWindow(2048) = %d, want 0", got)
	}
}

func TestOllamaContextWindow_EnvOverride(t *testing.T) {
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "65536")
	if got := ollamaContextWindow(0); got != 65536 {
		t.Errorf("ollamaContextWindow(0) env-override = %d, want 65536", got)
	}
	if got := ollamaContextWindow(2048); got != 65536 {
		t.Errorf("ollamaContextWindow(2048) env-override = %d, want 65536", got)
	}
}

func TestOllamaContextWindow_EnvJunk(t *testing.T) {
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "not-a-number")
	if got := ollamaContextWindow(0); got != DefaultOllamaContextWindow {
		t.Errorf("ollamaContextWindow(0) with junk env = %d, want %d", got, DefaultOllamaContextWindow)
	}
}
