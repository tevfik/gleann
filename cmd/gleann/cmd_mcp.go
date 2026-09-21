package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/mcp"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdMCP(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			cmdMCPInstall(args[1:])
			return
		case "help", "--help", "-h":
			printMCPUsage()
			return
		}
	}

	fs := flag.NewFlagSet("gleann mcp", flag.ExitOnError)
	cleanNames := fs.Bool("clean-names", false, "Strip 'gleann_' prefix from tool names for clients that namespace automatically (e.g. OpenCode)")
	_ = fs.Parse(args)

	isClean := *cleanNames || os.Getenv("GLEANN_MCP_CLEAN_NAMES") == "1" || os.Getenv("GLEANN_MCP_STRIP_PREFIX") == "1"
	runMCPServer(isClean)
}

func printMCPUsage() {
	fmt.Println(`Usage:
  gleann mcp [options]                     Start MCP server over stdio
  gleann mcp install [options]             Auto-configure MCP for AI agents & editors

Options for mcp:
  --clean-names     Strip 'gleann_' prefix from tool names for clients that namespace (OpenCode, etc.)

Options for install:
  --target <name>   Target platform: all, claude-code, cursor, gemini, antigravity, vscode, opencode
                    (default: all)
  --bin <path>      Explicit path to gleann binary (default: auto-detected)
  --name <name>     MCP server name in configuration (default: gleann)`)
}

func runMCPServer(cleanNames bool) {
	savedCfg := tui.LoadSavedConfig()

	cfg := mcp.Config{
		EmbeddingProvider: DefaultProvider,
		EmbeddingModel:    DefaultEmbeddingModel,
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           version,
		CleanToolNames:    cleanNames,
	}

	homeDir, _ := os.UserHomeDir()
	cfg.IndexDir = filepath.Join(homeDir, ".gleann", "indexes")

	if savedCfg != nil {
		if savedCfg.EmbeddingProvider != "" {
			cfg.EmbeddingProvider = savedCfg.EmbeddingProvider
		}
		if savedCfg.EmbeddingModel != "" {
			cfg.EmbeddingModel = savedCfg.EmbeddingModel
		}
		if savedCfg.OllamaHost != "" {
			cfg.OllamaHost = savedCfg.OllamaHost
		}
		if savedCfg.OpenAIKey != "" {
			cfg.OpenAIAPIKey = savedCfg.OpenAIKey
		}
		if savedCfg.OpenAIBaseURL != "" {
			cfg.OpenAIBaseURL = savedCfg.OpenAIBaseURL
		}
		if savedCfg.IndexDir != "" {
			cfg.IndexDir = tui.ExpandPath(savedCfg.IndexDir)
		}
	}

	server := mcp.NewServer(cfg)
	defer server.Close()
	server.Run()
}

func cmdMCPInstall(args []string) {
	fs := flag.NewFlagSet("gleann mcp install", flag.ExitOnError)
	target := fs.String("target", "all", "Target platform (claude-code, cursor, gemini, antigravity, vscode, all)")
	binPath := fs.String("bin", "", "Path to gleann binary")
	serverName := fs.String("name", "gleann", "Server name in MCP config")
	_ = fs.Parse(args)

	gleannBin := *binPath
	if gleannBin == "" {
		gleannBin = resolveInstalledGleannBin()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining user home directory: %v\n", err)
		os.Exit(1)
	}

	t := strings.ToLower(strings.TrimSpace(*target))
	configured := 0

	installTarget := func(desc, path string) {
		if err := MergeMCPServerConfig(path, *serverName, gleannBin, []string{"mcp"}); err != nil {
			fmt.Printf("  ✗ %s (%s): %v\n", desc, path, err)
		} else {
			fmt.Printf("  ✓ %s: %s\n", desc, path)
			configured++
		}
	}

	fmt.Printf("Configuring gleann MCP server (binary: %s)...\n", gleannBin)

	// Claude Code / Claude Desktop
	if t == "all" || t == "claude" || t == "claude-code" {
		installTarget("Claude Code", filepath.Join(home, ".claude.json"))
		claudeDesktopDir := filepath.Join(home, ".config", "Claude")
		if info, err := os.Stat(claudeDesktopDir); err == nil && info.IsDir() {
			installTarget("Claude Desktop", filepath.Join(claudeDesktopDir, "claude_desktop_config.json"))
		}
	}

	// Cursor
	if t == "all" || t == "cursor" {
		installTarget("Cursor (User)", filepath.Join(home, ".cursor", "mcp.json"))
		if _, err := os.Stat(".cursor"); err == nil {
			installTarget("Cursor (Workspace)", filepath.Join(".cursor", "mcp.json"))
		}
	}

	// Gemini / Antigravity
	if t == "all" || t == "gemini" || t == "antigravity" {
		installTarget("Google Antigravity / Gemini", filepath.Join(home, ".gemini", "config", "mcp_config.json"))
		antiIdeDir := filepath.Join(home, ".gemini", "antigravity-ide")
		if info, err := os.Stat(antiIdeDir); err == nil && info.IsDir() {
			installTarget("Antigravity IDE", filepath.Join(antiIdeDir, "mcp_config.json"))
		}
	}

	// VS Code / Cline / Roo Code
	if t == "all" || t == "vscode" || t == "cline" || t == "roo" {
		installTarget("VS Code (Workspace)", filepath.Join(".vscode", "mcp.json"))
		rooStorageDir := filepath.Join(home, ".config", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings")
		if info, err := os.Stat(rooStorageDir); err == nil && info.IsDir() {
			installTarget("Roo Code", filepath.Join(rooStorageDir, "cline_mcp_settings.json"))
		}
	}

	// OpenCode
	if t == "all" || t == "opencode" {
		openCodeDir := filepath.Join(home, ".config", "opencode")
		_ = os.MkdirAll(openCodeDir, 0o755)
		openCodeConfigPath := filepath.Join(openCodeDir, "opencode.json")
		if err := patchOpenCodeJSON(openCodeConfigPath); err != nil {
			fmt.Printf("  ✗ OpenCode (%s): %v\n", openCodeConfigPath, err)
		} else {
			fmt.Printf("  ✓ OpenCode: %s\n", openCodeConfigPath)
			configured++
		}
		globalAgentsPath := filepath.Join(openCodeDir, "AGENTS.md")
		if err := appendOrCreateFile(globalAgentsPath, agentsMDSection, "gleann: Code Intelligence"); err == nil {
			fmt.Printf("  ✓ OpenCode (Global AGENTS.md): %s\n", globalAgentsPath)
		}
	}

	fmt.Printf("\nDone! Configured %d MCP target(s).\n", configured)
	fmt.Printf("AI agents can now connect to gleann for AST code graphs, long-term memory, and hybrid search.\n")
}

// MergeMCPServerConfig reads an MCP config file, merges the gleann server entry into "mcpServers", and writes it back.
func MergeMCPServerConfig(filePath, serverName, binPath string, args []string) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	var root map[string]any
	data, err := os.ReadFile(filePath)
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			// If file exists but is invalid JSON, preserve a backup
			_ = os.WriteFile(filePath+".bak", data, 0o644)
			root = make(map[string]any)
		}
	} else {
		root = make(map[string]any)
	}

	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok || mcpServers == nil {
		mcpServers = make(map[string]any)
		root["mcpServers"] = mcpServers
	}

	serverEntry := map[string]any{
		"command": binPath,
		"args":    args,
	}
	mcpServers[serverName] = serverEntry

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	out = append(out, '\n')

	return os.WriteFile(filePath, out, 0o644)
}
