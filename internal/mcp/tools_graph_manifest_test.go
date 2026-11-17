package mcp

import "testing"

// Without a format-2 index the code graph cannot exist (a format-1 index has
// none, RFC 004 A3): no build may advertise tools that can only fail.
func TestMCP_NoFormat2HidesGraphTools(t *testing.T) {
	t.Setenv("GLEANN_STORE_FORMAT", "")
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
				t.Errorf("profile %q clean=%v advertises graph-only tool %q without a format-2 graph", tc.profile, tc.clean, n)
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

// With format 2 the SQLite code graph serves the graph tools in every build,
// so they stay advertised.
func TestMCP_KeepsGraphToolsForFormat2(t *testing.T) {
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "ollama", EmbeddingModel: "test", ToolsProfile: "full"})
	defer srv.Close()
	tools := srv.mcpServer.ListTools()
	for _, n := range graphOnlyTools {
		if _, ok := tools[n]; !ok {
			t.Errorf("format 2 enabled but %q is not advertised", n)
		}
	}
}
