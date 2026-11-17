//go:build !treesitter

package mcp

import "strings"

// Stub implementations for builds without the treesitter (CGo) tag.
//
// Such a build has no code graph (searcher.GraphDB() is always nil), so the
// tools that read the graph can only fail. NewServer registers them before it
// calls registerGraphTools; here they are withdrawn again so the manifest does
// not advertise tools that cannot work. gleann_def is kept: it falls back to
// vector search when there is no graph.

// graphPool stub — the real implementation is in tools_graph.go.
type graphPool struct{}

// graphBuild reports whether this build has the code graph.
const graphBuild = false

func (s *Server) initGraphPool()         { _ = s.gPool }
func (s *Server) closeGraphPool()        {}
func (s *Server) evictGraph(name string) {}

// registerGraphTools withdraws the tools that need the code graph.
func (s *Server) registerGraphTools() {
	names := make([]string, 0, len(graphOnlyTools))
	for _, n := range graphOnlyTools {
		if s.cleanToolNames {
			n = strings.TrimPrefix(n, "gleann_")
		}
		names = append(names, n)
	}
	s.mcpServer.DeleteTools(names...)
}
