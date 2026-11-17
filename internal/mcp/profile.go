package mcp

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
	"memory_context",
	"memory_search",
	"memory_forget",
}

// graphOnlyTools lists the tools registered in NewServer that need the code
// graph. A build without the graph withdraws them (see tools_graph_stub.go).
var graphOnlyTools = []string{
	"gleann_graph_neighbors",
	"gleann_impact",
	"gleann_document_links",
	"gleann_graph_stats",
	"gleann_symbols_in_file",
	"gleann_document_toc",
}

// DefaultProfileTools returns the names of the tools an agent sees from
// `gleann mcp` with the default profile in this build. In a build without the
// code graph the graph tools are not part of it.
func DefaultProfileTools() []string {
	out := make([]string, 0, len(coreProfileTools))
	for _, n := range coreProfileTools {
		if !graphBuild && isGraphOnly(n) {
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
