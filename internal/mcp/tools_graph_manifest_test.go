package mcp

import "testing"

// Every index can hold a code graph (SQLite, every build; RFC 004), so the
// graph tools are always advertised, also with an empty index directory.
func TestMCP_GraphToolsAlwaysAdvertised(t *testing.T) {
	graphTools := []string{"gleann_communities", "gleann_risk_analysis", "gleann_repo_map", "gleann_navigate_symbol"}
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "ollama", EmbeddingModel: "test", ToolsProfile: "full"})
	defer srv.Close()
	tools := srv.mcpServer.ListTools()
	for _, n := range graphTools {
		if _, ok := tools[n]; !ok {
			t.Errorf("%q is not advertised", n)
		}
	}
}
