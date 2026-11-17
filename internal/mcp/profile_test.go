package mcp

import (
	"reflect"
	"sort"
	"testing"
)

// The exported default-profile list that agent templates are built from must
// equal what a default-profile server really advertises in this build.
func TestMCP_DefaultProfileToolsMatchServer(t *testing.T) {
	newSrv := func(profile string) *Server {
		return NewServer(Config{
			IndexDir:          t.TempDir(),
			EmbeddingProvider: "ollama",
			EmbeddingModel:    "test",
			ToolsProfile:      profile,
		})
	}

	core := newSrv("core")
	defer core.Close()
	var advertised []string
	for name := range core.mcpServer.ListTools() {
		advertised = append(advertised, name)
	}
	sort.Strings(advertised)

	want := append([]string(nil), DefaultProfileTools()...)
	sort.Strings(want)
	if !reflect.DeepEqual(advertised, want) {
		t.Errorf("default profile advertises %v, DefaultProfileTools() says %v", advertised, want)
	}

	// Every tool the full profile registers and the core profile selects is listed.
	full := newSrv("full")
	defer full.Close()
	for name := range full.mcpServer.ListTools() {
		if core.isToolEnabled(name) {
			found := false
			for _, w := range want {
				found = found || w == name
			}
			if !found {
				t.Errorf("%q is in the core profile but not in DefaultProfileTools()", name)
			}
		}
	}
}
