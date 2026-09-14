package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tevfik/gleann/internal/tui"
)

func cmdUninstall(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		printUninstallUsage()
		return
	}

	removeAll := hasFlag(args, "--all") || hasFlag(args, "--purge") || hasFlag(args, "--data")
	autoConfirm := hasFlag(args, "--yes") || hasFlag(args, "-y")

	if !autoConfirm {
		if removeAll {
			fmt.Println("⚠️  WARNING: This will permanently remove the gleann binary, shell completions,")
			fmt.Println("   and ALL data, configuration, indexes, and long-term memory in ~/.gleann.")
		} else {
			fmt.Println("This will remove the gleann binary and shell completions from your system.")
			fmt.Println("(Configuration and indexes in ~/.gleann will be kept. Use --all to remove everything.)")
		}
		fmt.Print("Proceed with uninstall? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Println("Uninstall cancelled.")
			return
		}
	}

	tui.RunUninstall(removeAll)
}

func printUninstallUsage() {
	fmt.Print(`Usage: gleann uninstall [flags]

Uninstall gleann binary, shell completions, and optionally configuration and data.

Flags:
  --all, --purge, --data   Remove everything: binary, completions, config & indexes (~/.gleann)
  -y, --yes                Skip confirmation prompt
  -h, --help               Show this help message

Examples:
  gleann uninstall          # Remove binary and shell completions (keeps data)
  gleann uninstall --all    # Complete removal including ~/.gleann
`)
}
