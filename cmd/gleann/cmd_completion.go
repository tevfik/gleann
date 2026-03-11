package main

import (
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/tui"
)

func cmdCompletion(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `Usage: gleann completion <shell>

Generate shell completion script.

Supported shells:
  bash    Bash completion script
  zsh     Zsh completion script
  fish    Fish completion script

Examples:
  # Bash: add to ~/.bashrc
  source <(gleann completion bash)

  # Zsh: add to ~/.zshrc
  source <(gleann completion zsh)

  # Fish: save to completions dir
  gleann completion fish > ~/.config/fish/completions/gleann.fish`)
		os.Exit(1)
	}

	switch args[0] {
	case "bash":
		fmt.Print(tui.BashCompletion())
	case "zsh":
		fmt.Print(tui.ZshCompletion())
	case "fish":
		fmt.Print(tui.FishCompletion())
	default:
		fmt.Fprintf(os.Stderr, "unsupported shell: %s (use bash, zsh, or fish)\n", args[0])
		os.Exit(1)
	}
}
