//go:build !treesitter

// Stub implementations of the Memory Engine MCP tools for builds that do not
// include the treesitter (CGO) tag.  The tools are still registered so the
// MCP manifest is stable, but they return a clear "not available" error.

package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// ── Stub memory pool ──────────────────────────────────────────────────────────

type mcpMemoryPool struct{}

func newMCPMemoryPool(_ string) *mcpMemoryPool { return &mcpMemoryPool{} }
func (p *mcpMemoryPool) closeAll()             {}

// ── Stub tool builders & handlers ─────────────────────────────────────────────

const notAvailableMsg = "Memory Engine not available in this build — rebuild with CGO_ENABLED=1 and -tags treesitter"

func (s *Server) buildInjectKGTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "inject_knowledge_graph",
		Description: "Not available (requires CGO/treesitter build)",
	}
}

func (s *Server) handleInjectKG(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return mcpsdk.NewToolResultError(notAvailableMsg), nil
}

func (s *Server) buildDeleteEntityTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "delete_graph_entity",
		Description: "Not available (requires CGO/treesitter build)",
	}
}

func (s *Server) handleDeleteEntity(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return mcpsdk.NewToolResultError(notAvailableMsg), nil
}

func (s *Server) buildTraverseKGTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "traverse_knowledge_graph",
		Description: "Not available (requires CGO/treesitter build)",
	}
}

func (s *Server) handleTraverseKG(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return mcpsdk.NewToolResultError(notAvailableMsg), nil
}
