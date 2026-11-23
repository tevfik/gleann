package main

import (
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/tui"
)

func cmdCompletion(args []string) {
	if !wantsHelpIn(args, flagSet{}) {
		rejectUnknownFlags("completion", args, flagSet{})
	}
	if len(args) == 0 || wantsHelpIn(args, flagSet{}) {
		out := os.Stderr
		if len(args) > 0 {
			out = os.Stdout
		}
		fmt.Fprintln(out, `Usage: gleann completion <shell>

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
		if len(args) == 0 {
			exitFunc(1)
		}
		return
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
		exitFunc(1)
	}
}
