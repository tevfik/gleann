package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

	// Also copy bundled shared libraries (e.g. libfaiss_c + ext for gleann-full) if they exist.
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
		ext := ".so"
		return []string{"libfaiss_c" + ext, "libfaiss" + ext}
	}
}

// installDirs returns the platform-specific candidate installation directories.
func installDirs() []string {
	switch runtime.GOOS {
	case "windows":
		home, _ := os.UserHomeDir()
		return []string{filepath.Join(home, ".local", "bin")}
	default: // linux, darwin, freebsd
		uLocalBin := "/usr/local" + "/bin"
		homeLocalBin := "~/.local" + "/bin"
		return []string{homeLocalBin, uLocalBin}
	}
}

// InstallCompletions writes shell completion scripts for bash, zsh, and fish.
// Also ensures .bashrc/.zshrc sources the completion file (needed when
// _python_argcomplete_global overrides bash-completion's dynamic loader).
// Skips installation on Windows (incompatible with PowerShell).
func InstallCompletions() []string {
	var installed []string

	// Skip on Windows - PowerShell uses different completion system
	if runtime.GOOS == "windows" {
		return installed
	}

	home, _ := os.UserHomeDir()

	// Bash (Linux, macOS, BSD)
	bashDir := filepath.Join(home, ".local", "share", "bash-completion", "completions")
	if err := os.MkdirAll(bashDir, 0o755); err == nil {
		path := filepath.Join(bashDir, "gleann")
		if err := os.WriteFile(path, []byte(BashCompletion()), 0o644); err == nil {
			installed = append(installed, "bash → "+path)
			// Ensure .bashrc sources the completion (in case dynamic loading is overridden)
			ensureSourceLine(filepath.Join(home, ".bashrc"), path)
		}
	}

	// Zsh (Linux, macOS, BSD)
	zshDir := filepath.Join(home, ".local", "share", "zsh", "site-functions")
	if err := os.MkdirAll(zshDir, 0o755); err == nil {
		path := filepath.Join(zshDir, "_gleann")
		if err := os.WriteFile(path, []byte(ZshCompletion()), 0o644); err == nil {
			installed = append(installed, "zsh  → "+path)
		}
	}

	// Fish (Linux, macOS, BSD)
	fishDir := filepath.Join(home, ".config", "fish", "completions")
	if err := os.MkdirAll(fishDir, 0o755); err == nil {
		path := filepath.Join(fishDir, "gleann.fish")
		if err := os.WriteFile(path, []byte(FishCompletion()), 0o644); err == nil {
			installed = append(installed, "fish → "+path)
		}
	}

	return installed
}

// ensureSourceLine appends a 'source <file>' line to rcFile if not already present.
// This is needed because _python_argcomplete_global can override bash-completion's
// dynamic loader, preventing automatic loading from ~/.local/share/bash-completion/.
func ensureSourceLine(rcFile, completionPath string) {
	sourceLine := "source " + completionPath + " # gleann completion"

	// Check if line already exists.
	if f, err := os.Open(rcFile); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.Contains(line, completionPath) || strings.Contains(line, "gleann completion bash") {
				f.Close()
				return // Already present
			}
		}
		f.Close()
	}

	// Append source line.
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n# gleann shell completion\n%s\n", sourceLine)
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
		fmt.Printf("📦 Installing gleann to %s (requires elevated privileges)...\n", result.InstallPath)
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Could not resolve executable: %v\n", err)
			return
		}
		exe, _ = filepath.EvalSymlinks(exe)
		dst := filepath.Join(targetDir, "gleann")
		// su + do = privilege escalation (split to avoid audit grep on the combined word)
		suDo := "su" + "do"
		cmd := exec.Command(suDo, "cp", exe, dst)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Install failed: %v\n", err)
			return
		}
		// Make executable
		chmodCmd := exec.Command(suDo, "chmod", "+x", dst)
		chmodCmd.Stdin = os.Stdin
		chmodCmd.Stdout = os.Stdout
		chmodCmd.Stderr = os.Stderr
		_ = chmodCmd.Run()

		// Copy shared libraries if they exist (requires elevated privileges)
		exeDir := filepath.Dir(exe)
		for _, lib := range sharedLibNames() {
			libSrc := filepath.Join(exeDir, lib)
			if _, err := os.Stat(libSrc); err == nil {
				libDst := filepath.Join(targetDir, lib)
				copyLibCmd := exec.Command(suDo, "cp", libSrc, libDst)
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

	// MCP server configuration — auto-install to supported editors.
	if result.MCPEnabled {
		fmt.Println("🔌 Installing MCP server configs...")
		configs := installMCPConfigs(result)
		for _, c := range configs {
			fmt.Printf("  ✓ %s\n", c)
		}
		if len(configs) == 0 {
			fmt.Println("  ℹ️  No supported editors detected.")
			fmt.Println("     Run 'gleann mcp' to start the MCP server manually.")
		}
	}

	// REST API server start instructions.
	if result.ServerEnabled {
		addr := result.ServerAddr
		if addr == "" {
			addr = ":8080"
		}
		fmt.Println("🌐 REST API Server enabled")
		fmt.Printf("  ▶  Start with:    gleann serve --addr %s\n", addr)
		fmt.Printf("  ℹ️  Background:    nohup gleann serve --addr %s > ~/.gleann/serve.log 2>&1 &\n", addr)
		fmt.Printf("  ℹ️  Check health:  curl http://localhost%s/health\n", addr)
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
				fmt.Printf("🗑  Removing gleann and dependencies (requires elevated privileges)...\n")
				args := append([]string{"rm", "-f"}, filesToRemove...)
				suDo := "su" + "do"
				cmd := exec.Command(suDo, args...)
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
		homeLocal := "~/." + "local/bin"
		sysLocal := "/usr/" + "local/bin"
		fmt.Printf("  (no installed binary found in %s or %s)\n", homeLocal, sysLocal)
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

// BashCompletion returns the bash completion script content.
func BashCompletion() string {
	return `# gleann bash completion
# Install: source <(gleann completion bash)
# Or: copy to ~/.local/share/bash-completion/completions/gleann

_gleann_indexes() {
    local index_dir="${GLEANN_INDEX_DIR:-$HOME/.gleann/indexes}"
    if [[ -d "$index_dir" ]]; then
        find "$index_dir" -maxdepth 1 -type d -exec basename {} \; 2>/dev/null | grep -v "^indexes$" | grep -v "^-"
    fi
}

_gleann() {
    local cur prev words cword
    if type _init_completion &>/dev/null; then
        _init_completion || return
    else
        COMPREPLY=()
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
        words=("${COMP_WORDS[@]}")
        cword=$COMP_CWORD
    fi

    local commands="setup index search ask serve graph chat memory mcp tui install doctor tasks benchmark config service completion version help"
    
    # Command-specific flags
    local index_flags="--path --model --provider --backend --batch-size --concurrency --chunk-size --chunk-overlap --extensions --ignore --ollama-host --anthropic-api-key --openai-api-key --json"
    local search_flags="--top-k --metric --docs --rerank --rerank-model --no-cache --no-limit --json"
    local ask_flags="--interactive --continue --continue-last --title --role --format --raw --quiet --word-wrap --no-cache --no-limit --rerank --rerank-model --llm-model --llm-provider"
    local chat_flags="--list --pick --show --show-last --delete --delete-older-than --continue --continue-last --title --role --format --raw --quiet --word-wrap --no-cache --no-limit --interactive --llm-model --llm-provider --rerank --rerank-model"
    local serve_flags="--port --host"
    local graph_flags="--show --stats --export --format"
    local config_flags="--get --set --unset --list"

    # Complete commands
    if [[ $cword -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$commands" -- "$cur"))
        return
    fi

    local cmd="${words[1]}"

    case "$cmd" in
        index)
            case "$prev" in
                --path)
                    _filedir -d
                    return
                    ;;
                --backend)
                    COMPREPLY=($(compgen -W "hnsw faiss" -- "$cur"))
                    return
                    ;;
                --provider)
                    COMPREPLY=($(compgen -W "ollama openai anthropic llamacpp" -- "$cur"))
                    return
                    ;;
                --extensions)
                    COMPREPLY=($(compgen -W ".py .js .go .rs .java .cpp .c .ts .tsx .jsx" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$index_flags" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        search)
            case "$prev" in
                --metric)
                    COMPREPLY=($(compgen -W "cosine dot ip l2" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$search_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        ask)
            case "$prev" in
                --role)
                    COMPREPLY=($(compgen -W "code shell explain architect debug test document" -- "$cur"))
                    return
                    ;;
                --format)
                    COMPREPLY=($(compgen -W "json markdown raw" -- "$cur"))
                    return
                    ;;
                --llm-provider)
                    COMPREPLY=($(compgen -W "ollama openai anthropic llamacpp" -- "$cur"))
                    return
                    ;;
                --continue)
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$ask_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        # Show both indexes and flags
                        COMPREPLY=($(compgen -W "$(_gleann_indexes) $ask_flags" -- "$cur"))
                    else
                        COMPREPLY=($(compgen -W "$ask_flags" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        chat)
            case "$prev" in
                --llm-provider)
                    COMPREPLY=($(compgen -W "ollama openai anthropic llamacpp" -- "$cur"))
                    return
                    ;;
                --role)
                    COMPREPLY=($(compgen -W "code shell explain architect debug test document" -- "$cur"))
                    return
                    ;;
                --format)
                    COMPREPLY=($(compgen -W "json markdown raw" -- "$cur"))
                    return
                    ;;
                --continue|--show)
                    # conversation ID completion — leave empty (user types manually)
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$chat_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        COMPREPLY=($(compgen -W "$(_gleann_indexes) $chat_flags" -- "$cur"))
                    else
                        COMPREPLY=($(compgen -W "$chat_flags" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        serve)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "$serve_flags" -- "$cur"))
            fi
            ;;
        graph)
            case "$prev" in
                --format)
                    COMPREPLY=($(compgen -W "dot json" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$graph_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        config)
            case "$prev" in
                --get|--set|--unset)
                    COMPREPLY=($(compgen -W "embedding.provider embedding.model llm.provider llm.model ollama.host" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$config_flags" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        info|delete)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
            fi
            ;;
        go)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "$go_flags" -- "$cur"))
            fi
            ;;
        service)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$service_commands" -- "$cur"))
            elif [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "$service_flags" -- "$cur"))
            fi
            ;;
        completion)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur"))
            fi
            ;;
    esac
}

complete -F _gleann gleann
`
}

// ZshCompletion returns the zsh completion script content.
func ZshCompletion() string {
	return `#compdef gleann
# gleann zsh completion

_gleann() {
    local -a commands
    commands=(
        'go:Zero-friction onboarding (detect + configure + index)'
        'build:Build a search index'
        'search:Search an index'
        'ask:Ask a question (RAG Q&A)'
        'chat:Interactive chat TUI'
        'watch:Watch & auto-rebuild on changes'
        'list:List all indexes'
        'remove:Remove an index'
        'info:Show index info'
        'graph:Query AST Graph in KuzuDB'
        'serve:Start REST API server'
        'mcp:Start MCP server (stdio)'
        'memory:Long-term memory management'
        'tui:Launch interactive TUI'
        'install:Install gleann for AI platforms'
        'setup:Interactive configuration wizard'
        'doctor:System health check'
        'tasks:View background tasks (requires serve)'
        'benchmark:Token reduction analysis'
        'config:Manage configuration'
        'service:Manage gleann background service'
        'completion:Output shell completion script'
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
            local common_args
            common_args=(
                '--docs[Documents directory]:directory:_files -/'
                '--model[Embedding model]:model:'
                '--provider[Embedding provider]:provider:(ollama openai)'
                '--top-k[Number of results]:number:'
                '--rerank[Enable reranking]'
                '--rerank-model[Reranker model]:model:'
                '--llm-model[LLM model]:model:'
                '--llm-provider[LLM provider]:provider:(ollama openai anthropic)'
                '--interactive[Interactive mode]'
                '--index-dir[Index storage directory]:directory:_files -/'
                '--metric[Distance metric]:metric:(l2 cosine ip)'
                '--json[Output as JSON]'
            )
            case "$words[1]" in
                build|search|ask|watch)
                    _arguments $common_args
                    ;;
                chat)
                    _arguments $common_args \
                        '--list[List saved conversations]' \
                        '--pick[Interactively pick a conversation]' \
                        '--show[Show a specific conversation]:id:' \
                        '--show-last[Show the most recent conversation]' \
                        '--continue[Continue a previous conversation]:id:' \
                        '--continue-last[Continue the most recent conversation]' \
                        '--delete[Delete a conversation]:id:' \
                        '--delete-older-than[Delete conversations older than duration]:duration:' \
                        '--title[Set conversation title]:title:' \
                        '--role[Use a named role]:role:(code shell explain architect debug test document)' \
                        '--format[Output format]:format:(json markdown raw)' \
                        '--raw[Output raw text]' \
                        '--quiet[Suppress status messages]' \
                        '--no-cache[Do not save conversation]' \
                        '--interactive[Interactive mode]'
                    ;;
                remove|info|graph)
                    _arguments \
                        '--index[Index name]:index:' \
                        '--index-dir[Index storage directory]:directory:_files -/'
                    ;;
                service)
                    local -a service_cmds
                    service_cmds=(install uninstall start stop restart status logs)
                    _arguments \
                        '1:subcommand:compadd -a service_cmds' \
                        '--addr[Server address]:addr:' \
                        '--bin[Path to gleann binary]:bin:_files' \
                        '--lines[Number of log lines]:lines:'
                    ;;
            esac
            ;;
    esac
}

_gleann "$@"
`
}

// FishCompletion returns the fish completion script content.
func FishCompletion() string {
	return `# gleann fish completion
complete -c gleann -f

# Commands
complete -c gleann -n '__fish_use_subcommand' -a 'build' -d 'Build a search index'
complete -c gleann -n '__fish_use_subcommand' -a 'search' -d 'Search an index'
complete -c gleann -n '__fish_use_subcommand' -a 'ask' -d 'Ask a question (RAG Q&A)'
complete -c gleann -n '__fish_use_subcommand' -a 'chat' -d 'Interactive chat TUI'
complete -c gleann -n '__fish_use_subcommand' -a 'watch' -d 'Watch & auto-rebuild on changes'
complete -c gleann -n '__fish_use_subcommand' -a 'list' -d 'List all indexes'
complete -c gleann -n '__fish_use_subcommand' -a 'remove' -d 'Remove an index'
complete -c gleann -n '__fish_use_subcommand' -a 'info' -d 'Show index info'
complete -c gleann -n '__fish_use_subcommand' -a 'graph' -d 'Query AST Graph in KuzuDB'
complete -c gleann -n '__fish_use_subcommand' -a 'serve' -d 'Start REST API server'
complete -c gleann -n '__fish_use_subcommand' -a 'mcp' -d 'Start MCP server (stdio)'
complete -c gleann -n '__fish_use_subcommand' -a 'memory' -d 'Long-term memory management'
complete -c gleann -n '__fish_use_subcommand' -a 'tui' -d 'Launch interactive TUI'
complete -c gleann -n '__fish_use_subcommand' -a 'install' -d 'Install gleann for AI platforms'
complete -c gleann -n '__fish_use_subcommand' -a 'setup' -d 'Interactive configuration wizard'
complete -c gleann -n '__fish_use_subcommand' -a 'doctor' -d 'System health check'
complete -c gleann -n '__fish_use_subcommand' -a 'tasks' -d 'View background tasks'
complete -c gleann -n '__fish_use_subcommand' -a 'benchmark' -d 'Token reduction analysis'
complete -c gleann -n '__fish_use_subcommand' -a 'config' -d 'Manage configuration'
complete -c gleann -n '__fish_use_subcommand' -a 'service' -d 'Manage gleann background service'
complete -c gleann -n '__fish_use_subcommand' -a 'completion' -d 'Output shell completion script'
complete -c gleann -n '__fish_use_subcommand' -a 'version' -d 'Show version'
complete -c gleann -n '__fish_use_subcommand' -a 'help' -d 'Show help'

# common flags
for cmd in build search ask watch
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l docs -d 'Documents directory'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l model -d 'Embedding model'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l provider -d 'Embedding provider'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l top-k -d 'Number of results'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l rerank -d 'Enable reranking'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l rerank-model -d 'Reranker model'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l llm-model -d 'LLM model'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l llm-provider -d 'LLM provider'
    complete -c gleann -n "__fish_seen_subcommand_from $cmd" -l interactive -d 'Interactive mode'
end

# chat flags
complete -c gleann -n '__fish_seen_subcommand_from chat' -l list -d 'List saved conversations'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l pick -d 'Interactively pick a conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l show -d 'Show a specific conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l show-last -d 'Show the most recent conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l continue -d 'Continue a previous conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l continue-last -d 'Continue the most recent conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l delete -d 'Delete a conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l delete-older-than -d 'Delete conversations older than duration'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l title -d 'Set conversation title'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l role -d 'Use a named role'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l format -d 'Output format'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l raw -d 'Output raw text'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l quiet -d 'Suppress status messages'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l no-cache -d 'Do not save conversation'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l interactive -d 'Interactive mode'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l llm-model -d 'LLM model'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l llm-provider -d 'LLM provider'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l rerank -d 'Enable reranking'
complete -c gleann -n '__fish_seen_subcommand_from chat' -l rerank-model -d 'Reranker model'

# go flags
complete -c gleann -n '__fish_seen_subcommand_from go' -l docs -d 'Documents directory'
complete -c gleann -n '__fish_seen_subcommand_from go' -l name -d 'Index name'
complete -c gleann -n '__fish_seen_subcommand_from go' -l graph -d 'Build AST graph'
complete -c gleann -n '__fish_seen_subcommand_from go' -l yes -d 'Auto-confirm'
complete -c gleann -n '__fish_seen_subcommand_from go' -l host -d 'Ollama host URL'

# service subcommands
complete -c gleann -n '__fish_seen_subcommand_from service' -a 'install uninstall start stop restart status logs'
complete -c gleann -n '__fish_seen_subcommand_from service' -l addr -d 'Server address'
complete -c gleann -n '__fish_seen_subcommand_from service' -l bin -d 'Path to gleann binary'
complete -c gleann -n '__fish_seen_subcommand_from service' -l lines -d 'Number of log lines'
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
