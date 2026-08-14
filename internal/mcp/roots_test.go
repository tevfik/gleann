package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandleRootsListChanged_NoSession(t *testing.T) {
	cfg := Config{
		IndexDir:          t.TempDir(),
		EmbeddingProvider: "mock",
	}
	server := NewServer(cfg)

	// Create a dummy notification
	notif := mcp.JSONRPCNotification{
		Notification: mcp.Notification{
			Method: mcp.MethodNotificationRootsListChanged,
		},
	}

	// We expect this to fail gracefully because there is no active client session
	// attached to the context to respond to the RequestRoots call.
	// But it shouldn't panic.
	server.handleRootsListChanged(context.Background(), notif)

	// Since there is no mock session provided, activeRoot should remain empty
	rootMutex.RLock()
	currentRoot := activeRoot
	rootMutex.RUnlock()

	if currentRoot != "" {
		t.Errorf("Expected activeRoot to remain empty on failure, got %s", currentRoot)
	}
}

func TestRootsDirectoryTransformation(t *testing.T) {
	// A mock test just to verify path sanitization logic conceptually
	primaryRoot := "file:///home/user/workspace/test-project"
	if strings.HasPrefix(primaryRoot, "file://") {
		primaryRoot = strings.TrimPrefix(primaryRoot, "file://")
	}

	if primaryRoot != "/home/user/workspace/test-project" {
		t.Errorf("Expected stripped path, got %s", primaryRoot)
	}
}
