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
	"memory_failure_add",
	"memory_context",
	"memory_search",
	"memory_forget",
}

// DefaultProfileTools returns the names of the tools an agent sees from
// `gleann mcp` with the default profile. The graph tools are always part of
// it: every index can hold a code graph (RFC 004).
func DefaultProfileTools() []string {
	return append([]string(nil), coreProfileTools...)
}
