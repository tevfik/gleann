package main

import (
	"strings"

	"github.com/tevfik/gleann/internal/mcp"
)

// The agent templates (AGENTS.md, SKILL.md, ...) must only name MCP tools an
// agent sees with the default profile. They are derived from
// mcp.DefaultProfileTools so they cannot drift from the profile.

// hasMCPTool reports whether the default MCP profile of this build has the tool.
func hasMCPTool(name string) bool {
	for _, n := range mcp.DefaultProfileTools() {
		if n == name {
			return true
		}
	}
	return false
}

// mcpToolList renders the default profile as "`a` · `b` · ...".
func mcpToolList() string {
	names := mcp.DefaultProfileTools()
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return strings.Join(quoted, " · ")
}

// mcpFullProfileNote explains how to get the tools the default profile hides.
const mcpFullProfileNote = "`gleann mcp` exposes only these tools by default (the `core` profile). " +
	"For more (RAG Q&A, multi-index search, whole-document reading, knowledge-graph editing, memory listing), " +
	"start the server with `gleann mcp --tools full` or set `GLEANN_TOOLS=full`; " +
	"`--tools a,b,c` selects individual tools."

// mcpMemoryToolLines renders the memory tool reference used by SKILL.md.
func mcpMemoryToolLines() string {
	var sb strings.Builder
	if hasMCPTool("memory_context") {
		sb.WriteString("memory_context              → compiled <memory_context> window for LLM injection\n")
	}
	if hasMCPTool("memory_remember") {
		sb.WriteString("memory_remember content=... tier=long|medium|short label=... tags=[...]\n")
	}
	if hasMCPTool("memory_forget") {
		sb.WriteString("memory_forget   id_or_query=...\n")
	}
	if hasMCPTool("memory_search") {
		sb.WriteString("memory_search   query=...\n")
	}
	return sb.String()
}
