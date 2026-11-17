package mcp

import (
	"strings"
	"testing"
)

func h2GraphHandlers(s *Server) []struct {
	name string
	h    toolHandler
	args map[string]any
} {
	return []struct {
		name string
		h    toolHandler
		args map[string]any
	}{
		{"neighbors", s.handleGraphNeighbors, map[string]any{"node_fqn": "pkg.Hub"}},
		{"document links", s.handleDocumentLinks, map[string]any{"doc_path": "docs/guide.md"}},
		{"document toc", s.handleDocumentTOC, map[string]any{}},
		{"impact", s.handleImpact, map[string]any{"symbol": "pkg.Hub"}},
		{"stats", s.handleGraphStats, map[string]any{}},
		{"symbols in file", s.handleSymbolsInFile, map[string]any{"file_path": "core/hub.go"}},
	}
}

func TestH2GraphHandlers_IndexWithoutGraph(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	_, release, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	hasGraph := srv.searchers["idx"].GraphDB() != nil
	release()
	if hasGraph {
		t.Skip("this build opens a graph for every index")
	}
	for _, tc := range h2GraphHandlers(srv) {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["index"] = "idx"
			res := h2Call(t, tc.h, tc.args)
			if !res.IsError || !strings.Contains(strings.ToLower(extractText(res)), "graph database not") {
				t.Fatalf("got error=%v %q", res.IsError, extractText(res))
			}
		})
	}
}
