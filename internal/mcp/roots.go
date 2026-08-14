package mcp

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	activeRoot string
	rootMutex  sync.RWMutex
)

// registerRootsHandler sets up the notification handler for MCP roots changing.
func (s *Server) registerRootsHandler() {
	s.mcpServer.AddNotificationHandler(mcp.MethodNotificationRootsListChanged, s.handleRootsListChanged)
}

func (s *Server) handleRootsListChanged(ctx context.Context, notification mcp.JSONRPCNotification) {
	req := mcp.ListRootsRequest{}
	res, err := s.mcpServer.RequestRoots(ctx, req)
	if err != nil {
		log.Printf("Failed to request roots from client: %v", err)
		return
	}

	if res == nil || len(res.Roots) == 0 {
		log.Println("No roots provided by client.")
		return
	}

	// Assuming the first root is the primary workspace folder
	primaryRoot := res.Roots[0].URI

	// Convert file:///home/user/project to /home/user/project
	if strings.HasPrefix(primaryRoot, "file://") {
		primaryRoot = strings.TrimPrefix(primaryRoot, "file://")
	}

	rootMutex.Lock()
	activeRoot = primaryRoot
	// Dynamically update the index directory to point to the .gleann folder inside the workspace root
	s.config.IndexDir = filepath.Join(primaryRoot, ".gleann")
	rootMutex.Unlock()

	log.Printf("Roots changed. Gleann active workspace updated to: %s. IndexDir is now: %s", primaryRoot, s.config.IndexDir)

	// Clear the cached searchers since the index directory has changed
	for k, searcher := range s.searchers {
		searcher.Close()
		delete(s.searchers, k)
	}
	s.searcherLRU = []string{}
}
