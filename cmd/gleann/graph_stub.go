//go:build !treesitter

package main

import (
	"fmt"
	"os"
)

// cmdGraph in a lite build (no CGo/tree-sitter). A format-2 index keeps its
// code graph in the index's own SQLite database (RFC 003 step 7), so the
// GraphDB-backed subcommands — deps, callers, explain, path, query, toc and
// build — work here too. The Kùzu-only analysis commands still need
// -tags treesitter.
func cmdGraph(args []string) {
	if len(args) < 1 {
		printGraphUsage()
		os.Exit(1)
	}

	subCmd := args[0]
	if fs, ok := graphFlagSets[subCmd]; ok {
		rejectUnknownFlags("graph "+subCmd, args[1:], fs)
	}
	config := getConfig(args)

	switch subCmd {
	case "build":
		cmdGraphBuild(args[1:], config)
		return
	case "toc":
		cmdGraphTOC(args[1:], config)
		return
	case "help", "--help":
		printGraphUsage()
		return
	case "viz", "report", "communities", "export", "wiki", "hook", "map", "risk":
		fmt.Fprintf(os.Stderr, "⚠️  graph %s requires CGo (build with -tags treesitter).\n", subCmd)
		fmt.Fprintln(os.Stderr, "   Use the gleann-full binary or rebuild with: go build -tags treesitter ./cmd/gleann")
		os.Exit(1)
	}

	// Subcommands that need a symbol argument.
	if len(args) < 2 {
		printGraphUsage()
		os.Exit(1)
	}
	symbol := args[1]

	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required for graph queries")
		os.Exit(1)
	}

	db, err := openGraphDBForCLI(config, indexName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db for index %s: %v\n", indexName, err)
		os.Exit(1)
	}
	defer db.Close()

	runGraphSymbolCommand(subCmd, args, symbol, db)
}
