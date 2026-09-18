package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMCPServerConfig_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "sub", "mcp.json")

	err := MergeMCPServerConfig(cfgPath, "gleann", "/usr/local/bin/gleann", []string{"mcp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid mcpServers map")
	}

	server, ok := servers["gleann"].(map[string]any)
	if !ok {
		t.Fatalf("missing gleann entry")
	}

	if server["command"] != "/usr/local/bin/gleann" {
		t.Errorf("expected /usr/local/bin/gleann, got %v", server["command"])
	}
}

func TestMergeMCPServerConfig_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp.json")

	initial := `{
  "customSetting": true,
  "mcpServers": {
    "other-server": {
      "command": "node",
      "args": ["server.js"]
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	err := MergeMCPServerConfig(cfgPath, "gleann", "gleann", []string{"mcp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if root["customSetting"] != true {
		t.Errorf("customSetting was overwritten or lost")
	}

	servers := root["mcpServers"].(map[string]any)
	if servers["other-server"] == nil {
		t.Errorf("other-server was removed")
	}
	if servers["gleann"] == nil {
		t.Errorf("gleann was not added")
	}
}

func TestMergeMCPServerConfig_CorruptedFileBackup(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "corrupted.json")

	badData := "not a valid json {{"
	if err := os.WriteFile(cfgPath, []byte(badData), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	err := MergeMCPServerConfig(cfgPath, "gleann", "gleann", []string{"mcp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify backup was created
	bakData, err := os.ReadFile(cfgPath + ".bak")
	if err != nil || string(bakData) != badData {
		t.Errorf("expected backup file with original corrupt data")
	}

	// Verify new file is valid json with gleann
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("expected valid JSON after recovery: %v", err)
	}
	if root["mcpServers"] == nil {
		t.Errorf("expected mcpServers to be initialized")
	}
}
