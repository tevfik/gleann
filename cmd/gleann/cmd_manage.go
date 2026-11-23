package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdRemove(args []string) {
	if len(args) < 1 || wantsHelpIn(args, indexFlagSets["remove"]) {
		if wantsHelpIn(args, indexFlagSets["remove"]) {
			fmt.Println(`Usage: gleann index remove <name1> [name2] ... or gleann index remove "<prefix>*"

Remove one or more indexes and all associated vector and graph storage.

Options:
  -h, --help              Show this help message`)
			return
		}
		fmt.Fprintln(os.Stderr, "usage: gleann remove <name1> [name2] ... or gleann remove \"prefix*\"")
		exitFunc(1)
	}

	config := getConfig(args)
	applySavedConfig(&config, args)
	// Collect only index names/patterns: skip flag tokens and the values of
	// value-taking flags (the "/x" of "--index-dir /x" is not an index name),
	// and treat everything after "--" as names.
	known := indexFlagSets["remove"]
	var patterns []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			patterns = append(patterns, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			name, _, hasEq := strings.Cut(arg, "=")
			if takesValue, ok := known[name]; ok && takesValue && !hasEq {
				i++ // skip the flag's value
			}
			continue
		}
		patterns = append(patterns, arg)
	}

	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no index name or pattern provided")
		exitFunc(1)
	}

	// Get all existing indexes to match against patterns.
	allIndexes, err := gleann.ListIndexes(config.IndexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing indexes: %v\n", err)
		exitFunc(1)
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
	// bubbletea needs an interactive terminal on BOTH stdin and stdout.
	// Without this guard the program panics or produces unreadable output
	// in non-TTY contexts (cron, CI, `gleann tui | cat`, `TERM=` exports).
	if !isInputTTY() || !isOutputTTY() {
		fmt.Fprintln(os.Stderr, "gleann tui requires an interactive terminal (stdin and stdout must be a TTY).")
		fmt.Fprintln(os.Stderr, "If you are running in CI or over a pipe, use these non-interactive commands instead:")
		fmt.Fprintln(os.Stderr, "  gleann setup --auto              # zero-prompt setup")
		fmt.Fprintln(os.Stderr, "  gleann ask <index> \"question\"   # one-shot Q&A")
		fmt.Fprintln(os.Stderr, "  gleann serve                     # REST API + Swagger UI")
		exitFunc(1)
	}
	if err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

// cmdTag manages index tags: gleann index tag <name> [--add <tags>] [--remove <tags>] [--set <tags>] [--clear]
func cmdTag(args []string) {
	if len(args) < 1 || wantsHelpIn(args, indexFlagSets["tag"]) {
		fmt.Fprintln(os.Stderr, "usage: gleann index tag <name> [--add <tags>] [--remove <tags>] [--set <tags>] [--clear]")
		if wantsHelpIn(args, indexFlagSets["tag"]) {
			return
		}
		exitFunc(1)
	}

	name := args[0]
	config := getConfig(args)
	applySavedConfig(&config, args)

	addTagsStr := getFlag(args, "--add")
	remTagsStr := getFlag(args, "--remove")
	setTagsStr := getFlag(args, "--set")
	clearTags := hasFlag(args, "--clear")

	if addTagsStr == "" && remTagsStr == "" && setTagsStr == "" && !clearTags {
		meta, err := gleann.GetIndexMeta(config.IndexDir, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		if len(meta.Tags) == 0 {
			fmt.Printf("Index %q has no tags.\n", name)
		} else {
			fmt.Printf("🏷️  Tags for %q: @%s\n", name, strings.Join(meta.Tags, ", @"))
		}
		return
	}

	err := gleann.UpdateIndexMeta(config.IndexDir, name, func(m *gleann.IndexMeta) {
		if clearTags {
			m.Tags = []string{}
			return
		}

		if setTagsStr != "" {
			var newTags []string
			seen := make(map[string]bool)
			for _, t := range strings.Split(setTagsStr, ",") {
				t = strings.TrimPrefix(strings.TrimSpace(t), "@")
				if t != "" && !seen[strings.ToLower(t)] {
					seen[strings.ToLower(t)] = true
					newTags = append(newTags, t)
				}
			}
			sort.Strings(newTags)
			m.Tags = newTags
			return
		}

		tagSet := make(map[string]bool)
		for _, t := range m.Tags {
			tagSet[strings.ToLower(strings.TrimSpace(t))] = true
		}

		if addTagsStr != "" {
			for _, t := range strings.Split(addTagsStr, ",") {
				t = strings.TrimPrefix(strings.TrimSpace(t), "@")
				if t != "" {
					tagSet[strings.ToLower(t)] = true
				}
			}
		}

		if remTagsStr != "" {
			for _, t := range strings.Split(remTagsStr, ",") {
				t = strings.TrimPrefix(strings.TrimSpace(t), "@")
				delete(tagSet, strings.ToLower(t))
			}
		}

		var newTags []string
		for t := range tagSet {
			newTags = append(newTags, t)
		}
		sort.Strings(newTags)
		m.Tags = newTags
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "error updating tags for %q: %v\n", name, err)
		exitFunc(1)
	}

	meta, _ := gleann.GetIndexMeta(config.IndexDir, name)
	if len(meta.Tags) == 0 {
		fmt.Printf("✅ Updated tags for %q: (none)\n", name)
	} else {
		fmt.Printf("✅ Updated tags for %q: @%s\n", name, strings.Join(meta.Tags, ", @"))
	}
}

// cmdSet updates index properties: gleann index set <name> [--public | --private] [--mcp=true|false] [--desc "description"]
func cmdSet(args []string) {
	if len(args) < 1 || wantsHelpIn(args, indexFlagSets["set"]) {
		fmt.Fprintln(os.Stderr, "usage: gleann index set <name> [--public | --private] [--mcp=true|false] [--desc <text>]")
		if wantsHelpIn(args, indexFlagSets["set"]) {
			return
		}
		exitFunc(1)
	}

	name := args[0]
	config := getConfig(args)
	applySavedConfig(&config, args)

	isPublic := hasFlag(args, "--public")
	isPrivate := hasFlag(args, "--private")
	mcpStr := getFlag(args, "--mcp")
	descStr := getFlag(args, "--desc")

	if !isPublic && !isPrivate && mcpStr == "" && descStr == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann index set <name> [--public | --private] [--mcp=true|false] [--desc <text>]")
		exitFunc(1)
	}

	err := gleann.UpdateIndexMeta(config.IndexDir, name, func(m *gleann.IndexMeta) {
		if isPublic {
			val := true
			m.MCPExposed = &val
		} else if isPrivate {
			val := false
			m.MCPExposed = &val
		} else if mcpStr != "" {
			val := strings.ToLower(mcpStr) == "true" || mcpStr == "1" || mcpStr == "yes"
			m.MCPExposed = &val
		}
		if descStr != "" {
			m.Description = descStr
		}
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "error updating index %q: %v\n", name, err)
		exitFunc(1)
	}

	meta, _ := gleann.GetIndexMeta(config.IndexDir, name)
	status := "🟢 Public (MCP Exposed)"
	if !meta.IsMCPExposed() {
		status = "🔒 Private (Hidden from MCP)"
	}
	descOut := meta.Description
	if descOut == "" {
		descOut = "(none)"
	}
	fmt.Printf("✅ Index %q updated:\n   Access Status: %s\n   Description:   %s\n", name, status, descOut)
}
