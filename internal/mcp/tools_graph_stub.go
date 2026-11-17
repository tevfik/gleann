//go:build !treesitter

package mcp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Stub implementations for builds without the treesitter (CGo) tag.
//
// A format-2 index keeps its code graph in the index's own SQLite database
// (RFC 003 step 7), so the GraphDB-backed tools work in this build too. When
// no format-2 index exists and new indexes are not built in format 2, they
// can only fail; registerGraphTools then withdraws them so the manifest does
// not advertise tools that cannot work. gleann_def is always kept: it falls
// back to vector search when there is no graph. The Kùzu-specific analysis
// tools (communities, risk, repo map) need -tags treesitter.

// graphPool stub — the real implementation is in tools_graph.go.
type graphPool struct{}

// graphBuild reports whether this build has the Kùzu code graph.
const graphBuild = false

func (s *Server) initGraphPool()         { _ = s.gPool }
func (s *Server) closeGraphPool()        {}
func (s *Server) evictGraph(name string) {}

// registerGraphTools withdraws the graph tools unless a format-2 graph can
// exist in this index directory.
func (s *Server) registerGraphTools() {
	if sqliteGraphAvailable(s.config.IndexDir) {
		return
	}
	names := make([]string, 0, len(graphOnlyTools))
	for _, n := range graphOnlyTools {
		if s.cleanToolNames {
			n = strings.TrimPrefix(n, "gleann_")
		}
		names = append(names, n)
	}
	s.mcpServer.DeleteTools(names...)
}

// sqliteGraphAvailable reports whether new indexes are built in format 2
// (GLEANN_STORE_FORMAT=2) or an existing index in indexDir is format 2.
func sqliteGraphAvailable(indexDir string) bool {
	if strings.TrimSpace(os.Getenv("GLEANN_STORE_FORMAT")) == "2" {
		return true
	}
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && gleann.StoreFormatOf(filepath.Join(indexDir, e.Name(), e.Name())) == 2 {
			return true
		}
	}
	return false
}
