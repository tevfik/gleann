package tui

import (
	"testing"
)

func TestPickPreferred(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3:8b"},
		{Name: "gemma3:4b"},
		{Name: "qwen2.5:7b"},
	}

	tests := []struct {
		name      string
		preferred []string
		want      string
	}{
		{"first match", []string{"gemma3", "qwen2.5"}, "gemma3:4b"},
		{"second match", []string{"phi-4", "qwen2.5"}, "qwen2.5:7b"},
		{"no match fallback", []string{"mistral", "deepseek"}, "llama3:8b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickPreferred(models, tt.preferred)
			if got != tt.want {
				t.Errorf("pickPreferred() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsSetupNeeded(t *testing.T) {
	// With no config file saved, setup should be needed.
	// This test relies on no ~/.gleann/config.json existing in CI,
	// or the test environment not having a completed config.
	// We just verify it doesn't panic.
	_ = IsSetupNeeded()
}

func TestOllamaReachableUnreachable(t *testing.T) {
	// Non-existent host should return false quickly.
	if ollamaReachable("http://127.0.0.1:19999") {
		t.Error("expected unreachable host to return false")
	}
}

func TestCheckSetup(t *testing.T) {
	// Just verify it doesn't panic.
	_ = CheckSetup()
}
