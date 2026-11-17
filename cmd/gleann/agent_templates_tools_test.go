package main

import (
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/mcp"
)

// The templates may only name MCP tools of the default profile, and must say
// how to get the rest.
func TestAgentTemplatesListOnlyDefaultProfileTools(t *testing.T) {
	notDefault := []string{
		"gleann_ask", "gleann_search_multi", "gleann_read_full_document",
		"gleann_document_toc", "memory_list", "inject_knowledge_graph",
		"traverse_knowledge_graph", "delete_graph_entity",
	}
	for name, content := range map[string]string{
		"AGENTS.md": getAgentsMDContent("px4"),
		"SKILL.md":  getSkillMDContent("px4"),
	} {
		for _, tool := range notDefault {
			if strings.Contains(content, "`"+tool+"`") || strings.Contains(content, "\n"+tool+" ") {
				t.Errorf("%s names %q, which the default profile does not offer", name, tool)
			}
		}
		for _, tool := range mcp.DefaultProfileTools() {
			if !strings.Contains(content, tool) {
				t.Errorf("%s does not mention default-profile tool %q", name, tool)
			}
		}
		if !strings.Contains(content, "--tools full") {
			t.Errorf("%s does not explain how to enable the full profile", name)
		}
		if strings.Contains(content, "@@") {
			t.Errorf("%s has an unexpanded placeholder", name)
		}
	}
}
