//go:build !treesitter

package mcp

import "testing"

// A build without the code graph must not advertise tools that can only fail.
func TestMCP_NoGraphBuildHidesGraphTools(t *testing.T) {
	for _, tc := range []struct {
		profile string
		clean   bool
	}{{"core", false}, {"full", false}, {"core", true}, {"full", true}} {
		srv := NewServer(Config{
			IndexDir:          t.TempDir(),
			EmbeddingProvider: "ollama",
			EmbeddingModel:    "test",
			ToolsProfile:      tc.profile,
			CleanToolNames:    tc.clean,
		})
		tools := srv.mcpServer.ListTools()
		for _, n := range graphOnlyTools {
			if tc.clean {
				n = n[len("gleann_"):]
			}
			if _, ok := tools[n]; ok {
				t.Errorf("profile %q clean=%v advertises graph-only tool %q in a build without graph", tc.profile, tc.clean, n)
			}
		}
		def := "gleann_def"
		if tc.clean {
			def = "def"
		}
		if _, ok := tools[def]; !ok {
			t.Errorf("profile %q clean=%v lost %q, which works without the graph", tc.profile, tc.clean, def)
		}
		if _, ok := tools["gleann_search"]; !ok && !tc.clean {
			t.Errorf("profile %q lost gleann_search", tc.profile)
		}
		srv.Close()
	}
}
