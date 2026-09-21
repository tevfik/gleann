package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetAgentsMDContent(t *testing.T) {
	// Generic template
	generic := getAgentsMDContent("")
	if !strings.Contains(generic, "## gleann: Code Intelligence, Search & Long-term Memory") {
		t.Errorf("expected header in generic AGENTS.md, got:\n%s", generic)
	}
	if !strings.Contains(generic, "gleann search <name> <query>") {
		t.Errorf("expected <name> placeholder in generic AGENTS.md")
	}

	// Index-specific template
	custom := getAgentsMDContent("px4")
	if !strings.Contains(custom, "index name **`px4`**") {
		t.Errorf("expected px4 index name in AGENTS.md, got:\n%s", custom)
	}
	if !strings.Contains(custom, "gleann search px4 <query>") {
		t.Errorf("expected px4 search command in AGENTS.md")
	}
	if !strings.Contains(custom, "index automatically defaults to `px4`") {
		t.Errorf("expected default index explanation in AGENTS.md")
	}
}

func TestCmdAgents_Stdout(t *testing.T) {
	// Should not crash
	cmdAgents([]string{"--help"})
}

func TestCmdAgents_Generate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gleann-agents-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Run cmdAgents to write to tmpDir
	cmdAgents([]string{"dump", "--dir", tmpDir, "--index", "my-repo"})

	agentsFile := filepath.Join(tmpDir, "AGENTS.md")
	data, err := os.ReadFile(agentsFile)
	if err != nil {
		t.Fatalf("expected AGENTS.md to be created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "index name **`my-repo`**") {
		t.Errorf("expected custom index name in generated AGENTS.md: %s", content)
	}
	if !strings.Contains(content, "gleann_sync") {
		t.Errorf("expected gleann_sync mention in generated AGENTS.md")
	}

	// Running again without --force should not duplicate if sentinel present
	initialLen := len(content)
	cmdAgents([]string{"dump", "--dir", tmpDir, "--index", "my-repo"})
	data2, _ := os.ReadFile(agentsFile)
	if len(data2) != initialLen {
		t.Errorf("expected idempotent appendOrCreateFile, initial %d, got %d", initialLen, len(data2))
	}
}
