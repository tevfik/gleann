package mcp

import (
	"os"
	"strings"
)

// coreProfileTools is the tool set of the default "core" profile, in the order
// documentation and agent templates list it. isToolEnabled must agree with it;
// TestMCP_DefaultProfileToolsMatchServer fails when they drift apart.
var coreProfileTools = []string{
	"gleann_search",
	"gleann_read",
	"gleann_def",
	"gleann_graph_neighbors",
	"gleann_impact",
	"gleann_sync",
	"memory_remember",
	"memory_failure_add",
	"memory_context",
	"memory_search",
	"memory_forget",
}

// graphOnlyTools lists the tools registered in NewServer that need the code
// graph. The server withdraws them unless a format-2 (SQLite) graph can exist
// (see withdrawGraphTools).
var graphOnlyTools = []string{
	"gleann_graph_neighbors",
	"gleann_impact",
	"gleann_document_links",
	"gleann_graph_stats",
	"gleann_symbols_in_file",
	"gleann_document_toc",
	"gleann_communities",
	"gleann_risk_analysis",
	"gleann_repo_map",
	"gleann_navigate_symbol",
}

// DefaultProfileTools returns the names of the tools an agent sees from
// `gleann mcp` with the default profile. The graph tools are part of it only
// when new indexes are built in format 2 (GLEANN_STORE_FORMAT=2), whose SQLite
// graph serves them; the server additionally keeps them when an existing
// index is format 2. A format-1 index has no code graph (RFC 004 A3).
func DefaultProfileTools() []string {
	withGraph := strings.TrimSpace(os.Getenv("GLEANN_STORE_FORMAT")) == "2"
	out := make([]string, 0, len(coreProfileTools))
	for _, n := range coreProfileTools {
		if !withGraph && isGraphOnly(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func isGraphOnly(name string) bool {
	for _, g := range graphOnlyTools {
		if g == name {
			return true
		}
	}
	return false
}
