//go:build !eif

package gleann

import (
	"context"
	"strings"
	"testing"
)

func TestChatEIF_NotSupported(t *testing.T) {
	chat := newTestChat(LLMEIF, "")
	_, err := chat.chat(context.Background(), []ChatMessage{{Role: "user", Content: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "without EIF runtime support") {
		t.Fatalf("expected error without EIF runtime support, got: %v", err)
	}

	err = chat.chatStream(context.Background(), []ChatMessage{{Role: "user", Content: "hello"}}, func(token string) {})
	if err == nil || !strings.Contains(err.Error(), "without EIF runtime support") {
		t.Fatalf("expected error without EIF runtime support, got: %v", err)
	}
}
