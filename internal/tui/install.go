package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// InstallBinary copies the current executable into targetDir.
// If targetDir requires root (e.g. /usr/local/bin), the caller should
// invoke this via sudo or warn the user.
func InstallBinary(targetDir string) error {
	targetDir = ExpandPath(targetDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	dst := filepath.Join(targetDir, "gleann")
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}

	// Don't copy onto itself, but STILL make sure shared libraries are copied!
	if abs, _ := filepath.Abs(dst); abs == exe {
		copySharedLibs(exe, targetDir)
		return nil
	}

	src, err := os.Open(exe)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// Write to temp file first to avoid "text file busy" error on running binary.
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		os.Remove(tmp) // Clean up temp file on copy error
		return fmt.Errorf("copy binary: %w", err)
	}
	
	out.Close() // Close before rename
	
	// Atomic rename (works even if dst is currently running).
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp) // Clean up temp file on rename error
		return fmt.Errorf("install binary: %w", err)
	}

	// Also copy bundled shared libraries (e.g. libfaiss_c.so for gleann-full) if they exist.
	copySharedLibs(exe, targetDir)

	return nil
}

func copySharedLibs(exe, targetDir string) {
	// RPATH $ORIGIN requires them to be in the exact same directory as the executable.
	exeDir := filepath.Dir(exe)
	for _, lib := range sharedLibNames() {
		libSrc := filepath.Join(exeDir, lib)
		if _, err := os.Stat(libSrc); err == nil {
			libDst := filepath.Join(targetDir, lib)
			libTmp := libDst + ".tmp"
			
			if s, err := os.Open(libSrc); err == nil {
				if d, err := os.OpenFile(libTmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755); err == nil {
					_, _ = io.Copy(d, s)
					d.Close()
					// Atomic rename to avoid "text file busy"
					_ = os.Rename(libTmp, libDst)
				}
				s.Close()
			}
		}
	}
}

// sharedLibNames returns the platform-specific FAISS shared library file names.
func sharedLibNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"libfaiss_c.dylib", "libfaiss.dylib"}
	case "windows":
		return []string{"faiss_c.dll", "faiss.dll"}
	default: // linux, freebsd, ...
		return []string{"libfaiss_c.so", "libfaiss.so"}
	}
}

// installDirs returns the platform-specific candidate installation directories.
func installDirs() []string {
	switch runtime.GOOS {
	case "windows":
		home, _ := os.UserHomeDir()
		return []string{filepath.Join(home, ".local", "bin")}
	default: // linux, darwin, freebsd
		return []string{"~/.local/bin", "/usr/local/bin"}
	}
}

// InstallCompletions writes shell completion scripts for bash, zsh, and fish.
func InstallCompletions() []string {
	var installed []string

	home, _ := os.UserHomeDir()

	// Bash
	bashDir := filepath.Join(home, ".local", "share", "bash-completion", "completions")
	if err := os.MkdirAll(bashDir, 0o755); err == nil {
		path := filepath.Join(bashDir, "gleann")
		if err := os.WriteFile(path, []byte(bashCompletion()), 0o644); err == nil {
			installed = append(installed, "bash → "+path)
		}
	}

	// Zsh
	zshDir := filepath.Join(home, ".local", "share", "zsh", "site-functions")
	if err := os.MkdirAll(zshDir, 0o755); err == nil {
		path := filepath.Join(zshDir, "_gleann")
		if err := os.WriteFile(path, []byte(zshCompletion()), 0o644); err == nil {
			installed = append(installed, "zsh  → "+path)
		}
	}

	// Fish
	fishDir := filepath.Join(home, ".config", "fish", "completions")
	if err := os.MkdirAll(fishDir, 0o755); err == nil {
		path := filepath.Join(fishDir, "gleann.fish")
		if err := os.WriteFile(path, []byte(fishCompletion()), 0o644); err == nil {
			installed = append(installed, "fish → "+path)
		}
	}

	return installed
}

// RunInstall performs the install/uninstall steps selected during onboarding.
func RunInstall(result *OnboardResult) {
	if result.Uninstall {
		RunUninstall(result.UninstallData)
		return
	}

	if result.InstallPath == "" {
		return
	}

	targetDir := ExpandPath(result.InstallPath)
	needsSudo := !isWritable(targetDir)

	fmt.Println()
	if needsSudo && runtime.GOOS != "windows" {
		fmt.Printf("📦 Installing gleann to %s (requires sudo)...\n", result.InstallPath)
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Could not resolve executable: %v\n", err)
			return
		}
		exe, _ = filepath.EvalSymlinks(exe)
		dst := filepath.Join(targetDir, "gleann")
		cmd := exec.Command("sudo", "cp", exe, dst)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Install failed: %v\n", err)
			return
		}
		// Make executable
		chmodCmd := exec.Command("sudo", "chmod", "+x", dst)
		chmodCmd.Stdin = os.Stdin
		chmodCmd.Stdout = os.Stdout
		chmodCmd.Stderr = os.Stderr
		_ = chmodCmd.Run()

		// Copy shared libraries if they exist (requires sudo)
		exeDir := filepath.Dir(exe)
		for _, lib := range sharedLibNames() {
			libSrc := filepath.Join(exeDir, lib)
			if _, err := os.Stat(libSrc); err == nil {
				libDst := filepath.Join(targetDir, lib)
				copyLibCmd := exec.Command("sudo", "cp", libSrc, libDst)
				_ = copyLibCmd.Run()
			}
		}
	} else {
		fmt.Printf("📦 Installing gleann to %s...\n", result.InstallPath)
		if err := InstallBinary(result.InstallPath); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Install failed: %v\n", err)
			return
		}
	}
	fmt.Println("  ✓ Binary installed")

	if result.InstallCompletions {
		fmt.Println("📝 Installing shell completions...")
		shells := InstallCompletions()
		for _, s := range shells {
			fmt.Printf("  ✓ %s\n", s)
		}
		if len(shells) == 0 {
			fmt.Println("  (no completions installed)")
		}
	}

	// MCP server configuration hint (don't auto-configure editors).
	if result.MCPEnabled {
		fmt.Println("🔌 MCP Server enabled")
		fmt.Println("  ℹ️  To configure AI editors manually:")
		fmt.Println("     Claude Code: Add to ~/.claude.json")
		fmt.Println("     VS Code: Add to workspace .vscode/mcp.json")
		fmt.Println("     Run 'gleann mcp' to start server")
	}

	fmt.Println()
}

func isWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		// Dir doesn't exist; check parent.
		parent := filepath.Dir(dir)
		info, err = os.Stat(parent)
		if err != nil {
			return false
		}
	}
	// Quick check: try creating a temp file.
	if info.IsDir() {
		tmp := filepath.Join(dir, ".gleann_write_test")
		f, err := os.Create(tmp)
		if err != nil {
			return false
		}
		f.Close()
		os.Remove(tmp)
		return true
	}
	return false
}

// ── Uninstall ──────────────────────────────────────────────────

// RunUninstall removes the gleann binary, shell completions, and optionally all data.
func RunUninstall(removeData bool) {
	fmt.Println()

	// Remove binary from known install locations.
	removed := false
	for _, dir := range installDirs() {
		p := filepath.Join(ExpandPath(dir), "gleann")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if _, err := os.Stat(p); err == nil {
			needsSudo := !isWritable(ExpandPath(dir))

			// Uninstall binary and bundled shared libraries
			filesToRemove := []string{p}
			for _, lib := range sharedLibNames() {
				libPath := filepath.Join(ExpandPath(dir), lib)
				if _, err := os.Stat(libPath); err == nil {
					filesToRemove = append(filesToRemove, libPath)
				}
			}

			if needsSudo && runtime.GOOS != "windows" {
				fmt.Printf("🗑  Removing gleann and dependencies (requires sudo)...\n")
				args := append([]string{"rm", "-f"}, filesToRemove...)
				cmd := exec.Command("sudo", args...)
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "  ✗ Failed to remove: %v\n", err)
				} else {
					fmt.Printf("  ✓ Removed binaries\n")
					removed = true
				}
			} else {
				for _, ftr := range filesToRemove {
					if err := os.Remove(ftr); err != nil {
						fmt.Fprintf(os.Stderr, "  ✗ Failed to remove %s: %v\n", ftr, err)
					} else {
						fmt.Printf("  ✓ Removed %s\n", ftr)
						removed = true
					}
				}
			}
		}
	}
	if !removed {
		fmt.Println("  (no installed binary found in ~/.local/bin or /usr/local/bin)")
	}

	// Remove shell completions.
	fmt.Println("🗑  Removing shell completions...")
	completionFiles := RemoveCompletions()
	for _, f := range completionFiles {
		fmt.Printf("  ✓ Removed %s\n", f)
	}
	if len(completionFiles) == 0 {
		fmt.Println("  (no completion files found)")
	}

	// Optionally remove all data.
	if removeData {
		home, _ := os.UserHomeDir()
		gleannDir := filepath.Join(home, ".gleann")
		if _, err := os.Stat(gleannDir); err == nil {
			fmt.Printf("🗑  Removing all data in %s...\n", gleannDir)
			if err := os.RemoveAll(gleannDir); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ Failed to remove %s: %v\n", gleannDir, err)
			} else {
				fmt.Printf("  ✓ Removed %s\n", gleannDir)
			}
		} else {
			fmt.Println("  (no data directory found)")
		}
	}

	fmt.Println("\n  gleann has been uninstalled.")
	fmt.Println()
}

// RemoveCompletions deletes shell completion files installed by gleann.
func RemoveCompletions() []string {
	var removed []string
	home, _ := os.UserHomeDir()

	paths := []string{
		filepath.Join(home, ".local", "share", "bash-completion", "completions", "gleann"),
		filepath.Join(home, ".local", "share", "zsh", "site-functions", "_gleann"),
		filepath.Join(home, ".config", "fish", "completions", "gleann.fish"),
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err == nil {
				removed = append(removed, p)
			}
		}
	}
	return removed
}

// ── Completion scripts ─────────────────────────────────────────

func bashCompletion() string {
	return `# gleann bash completion
_gleann() {
    local cur prev commands
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    commands="index search chat setup serve version help"

    case "$prev" in
        gleann)
            COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
            return 0
            ;;
        index)
            COMPREPLY=( $(compgen -W "list" -- "$cur") )
            return 0
            ;;
        search|chat)
            COMPREPLY=( $(compgen -W "--index --top-k --reranker --rerank-model --rerank-top-n" -- "$cur") )
            return 0
            ;;
    esac
}
complete -F _gleann gleann
`
}

func zshCompletion() string {
	return `#compdef gleann
# gleann zsh completion

_gleann() {
    local -a commands
    commands=(
        'index:Build a search index'
        'search:Search an index'
        'chat:Interactive chat with RAG'
        'setup:Run configuration wizard'
        'serve:Start HTTP server'
        'version:Show version'
        'help:Show help'
    )

    _arguments -C \
        '1:command:->command' \
        '*::arg:->args'

    case "$state" in
        command)
            _describe 'command' commands
            ;;
        args)
            case "$words[1]" in
                search|chat)
                    _arguments \
                        '--index[Index name]:index:' \
                        '--top-k[Number of results]:number:' \
                        '--reranker[Reranker provider]:provider:(ollama jina cohere voyage)' \
                        '--rerank-model[Reranker model]:model:' \
                        '--rerank-top-n[Rerank top N]:number:'
                    ;;
                index)
                    _arguments '1:subcommand:(list)'
                    ;;
            esac
            ;;
    esac
}

_gleann "$@"
`
}

func fishCompletion() string {
	return `# gleann fish completion
complete -c gleann -f

# Commands
complete -c gleann -n '__fish_use_subcommand' -a 'index' -d 'Build a search index'
complete -c gleann -n '__fish_use_subcommand' -a 'search' -d 'Search an index'
complete -c gleann -n '__fish_use_subcommand' -a 'chat' -d 'Interactive chat with RAG'
complete -c gleann -n '__fish_use_subcommand' -a 'setup' -d 'Run configuration wizard'
complete -c gleann -n '__fish_use_subcommand' -a 'serve' -d 'Start HTTP server'
complete -c gleann -n '__fish_use_subcommand' -a 'version' -d 'Show version'
complete -c gleann -n '__fish_use_subcommand' -a 'help' -d 'Show help'

# index subcommands
complete -c gleann -n '__fish_seen_subcommand_from index' -a 'list' -d 'List indexes'

# search/chat flags
complete -c gleann -n '__fish_seen_subcommand_from search chat' -l index -d 'Index name'
complete -c gleann -n '__fish_seen_subcommand_from search chat' -l top-k -d 'Number of results'
complete -c gleann -n '__fish_seen_subcommand_from search chat' -l reranker -d 'Reranker provider'
complete -c gleann -n '__fish_seen_subcommand_from search chat' -l rerank-model -d 'Reranker model'
complete -c gleann -n '__fish_seen_subcommand_from search chat' -l rerank-top-n -d 'Rerank top N'
`
}

// ── MCP Configuration ──────────────────────────────────────────

// installMCPConfigs generates MCP server configuration files for AI editors.
// Returns a list of descriptions of installed configs.
func installMCPConfigs(result *OnboardResult) []string {
	var installed []string

	// Resolve gleann binary path.
	gleannBin := resolveGleannBin(result)

	// Claude Code: ~/.claude.json (or ~/.config/claude/claude.json)
	if path := installClaudeCodeMCP(gleannBin); path != "" {
		installed = append(installed, "Claude Code → "+path)
	}

	// VS Code / Cursor: .vscode/mcp.json in workspace (skip, user manages)
	// Instead write global settings hint.

	// Claude Desktop: ~/Library/Application Support/Claude/claude_desktop_config.json (macOS)
	//                 %APPDATA%/Claude/claude_desktop_config.json (Windows)
	if path := installClaudeDesktopMCP(gleannBin); path != "" {
		installed = append(installed, "Claude Desktop → "+path)
	}

	return installed
}

// resolveGleannBin finds the best path for the gleann binary.
func resolveGleannBin(result *OnboardResult) string {
	// If installed to a path, use that.
	if result.InstallPath != "" {
		dir := ExpandPath(result.InstallPath)
		bin := filepath.Join(dir, "gleann")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}

	// Check $PATH.
	if p, err := exec.LookPath("gleann"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}

	// Fall back to current executable.
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.EvalSymlinks(exe); err == nil {
			return abs
		}
		return exe
	}

	return "gleann"
}

// installClaudeCodeMCP writes gleann as an MCP server to Claude Code config.
func installClaudeCodeMCP(gleannBin string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	configPath := filepath.Join(home, ".claude.json")

	// Read existing config or create new.
	config := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		if err := jsonUnmarshal(data, &config); err != nil {
			// Corrupted file, backup and overwrite.
			_ = os.Rename(configPath, configPath+".bak")
			config = make(map[string]any)
		}
	}

	// Get or create mcpServers.
	mcpServers, ok := config["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	mcpServers["gleann"] = map[string]any{
		"command": gleannBin,
		"args":    []string{"mcp"},
	}

	config["mcpServers"] = mcpServers

	data, err := jsonMarshalIndent(config, "", "  ")
	if err != nil {
		return ""
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return ""
	}

	return configPath
}

// installClaudeDesktopMCP writes gleann as an MCP server to Claude Desktop config.
func installClaudeDesktopMCP(gleannBin string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	var configPath string
	switch runtime.GOOS {
	case "darwin":
		configPath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return ""
		}
		configPath = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	case "linux":
		configPath = filepath.Join(home, ".config", "claude", "claude_desktop_config.json")
	default:
		return ""
	}

	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return ""
	}

	config := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		if err := jsonUnmarshal(data, &config); err != nil {
			_ = os.Rename(configPath, configPath+".bak")
			config = make(map[string]any)
		}
	}

	mcpServers, ok := config["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	mcpServers["gleann"] = map[string]any{
		"command": gleannBin,
		"args":    []string{"mcp"},
	}

	config["mcpServers"] = mcpServers

	data, err := jsonMarshalIndent(config, "", "  ")
	if err != nil {
		return ""
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return ""
	}

	return configPath
}

// jsonUnmarshal is a helper to avoid adding encoding/json import collision.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// jsonMarshalIndent is a helper for json.MarshalIndent.
func jsonMarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}
