//go:build treesitter

package mcp

import "testing"

// A build with the code graph keeps advertising the graph tools of the profile.
func TestMCP_GraphBuildKeepsGraphTools(t *testing.T) {
	srv := NewServer(Config{
		IndexDir:          t.TempDir(),
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test",
		ToolsProfile:      "core",
	})
	defer srv.Close()
	tools := srv.mcpServer.ListTools()
	for _, n := range []string{"gleann_graph_neighbors", "gleann_impact", "gleann_def"} {
		if _, ok := tools[n]; !ok {
			t.Errorf("core profile in a graph build lost %q", n)
		}
	}
}
