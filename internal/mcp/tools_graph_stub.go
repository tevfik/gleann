//go:build !treesitter

package mcp

// Stub implementations for builds without the treesitter (CGo) tag.
// Tools are still registered so the MCP manifest is stable.

// graphPool stub — the real implementation is in tools_graph.go.
type graphPool struct{}

func (s *Server) initGraphPool()      {}
func (s *Server) closeGraphPool()     {}
func (s *Server) registerGraphTools() {}
