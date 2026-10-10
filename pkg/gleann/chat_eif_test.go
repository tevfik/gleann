//go:build eif

package gleann

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChatEIF_ResolveAndDispatch(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("skipping test: no user home dir")
	}
	modelPath := filepath.Join(home, ".gleann", "models", "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf")
	if _, err := os.Stat(modelPath); err != nil {
		t.Skip("skipping test: model not found at", modelPath)
	}

	cfg := DefaultChatConfig()
	cfg.Provider = LLMEIF
	cfg.Model = modelPath
	cfg.MaxTokens = 8

	chat := NewChat(&chatMockSearcher{}, cfg)
	messages := []ChatMessage{
		{Role: "user", Content: "Hi"},
	}

	ans, err := chat.chat(context.Background(), messages)
	if err != nil {
		t.Fatalf("chatEIF failed: %v", err)
	}
	t.Logf("Generated answer: %q", ans)
}
