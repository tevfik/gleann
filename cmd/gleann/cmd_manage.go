package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann remove <name1> [name2] ... or gleann remove \"prefix*\"")
		os.Exit(1)
	}

	config := getConfig(args)
	// Filter out flags from args to get only index names/patterns.
	var patterns []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			patterns = append(patterns, arg)
		}
	}

	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no index name or pattern provided")
		os.Exit(1)
	}

	// Get all existing indexes to match against patterns.
	allIndexes, err := gleann.ListIndexes(config.IndexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing indexes: %v\n", err)
		os.Exit(1)
	}

	// Identify indexes to remove.
	toBeRemoved := make(map[string]bool)
	for _, pattern := range patterns {
		matched := false
		for _, idx := range allIndexes {
			match, _ := filepath.Match(pattern, idx.Name)
			if match {
				toBeRemoved[idx.Name] = true
				matched = true
			}
		}
		// If it's not a wildcard and didn't match, assume it's a literal name.
		if !strings.ContainsAny(pattern, "*?[]") && !matched {
			toBeRemoved[pattern] = true
		}
	}

	if len(toBeRemoved) == 0 {
		fmt.Println("No matching indexes found.")
		return
	}

	// Confirmation for mass deletion.
	if len(toBeRemoved) > 3 || (len(patterns) == 1 && patterns[0] == "*") {
		fmt.Printf("⚠️  Are you sure you want to remove %d indexes? (y/N): ", len(toBeRemoved))
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	// Perform removal.
	successCount := 0
	for name := range toBeRemoved {
		if err := gleann.RemoveIndex(config.IndexDir, name); err != nil {
			fmt.Fprintf(os.Stderr, "error removing %q: %v\n", name, err)
		} else {
			fmt.Printf("🗑️  Index %q removed.\n", name)
			successCount++
		}
	}

	if successCount > 1 {
		fmt.Printf("✅ Successfully removed %d indexes.\n", successCount)
	}
}

func cmdTUI() {
	if err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}


