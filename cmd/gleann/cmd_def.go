package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

func printDefUsage() {
	fmt.Println(`Usage: gleann def <symbol> [options]

Find where a symbol is defined (Go-to-Definition, no whole-file dumps).

Arguments:
  <symbol>               Symbol name or identifier (e.g. TokenManager, calculateMetrics)

Options:
  --index <name>         Index name to search (defaults to first available index)
  -k <N>                 Maximum number of definitions to return (default: 5)
  --kind <kind>          Filter by symbol kind (function, method, struct, class, interface, type)
  --json                 Output raw JSON response
  -h, --help             Show this help message

Examples:
  gleann def TokenManager
  gleann def calculateMetrics --kind function
  gleann def Engine --json`)
}

func cmdDef(args []string) {
	if len(args) < 1 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		printDefUsage()
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			return
		}
		os.Exit(1)
	}

	var symbol string
	indexName := getFlag(args, "--index")
	kind := getFlag(args, "--kind")
	asJSON := hasFlag(args, "--json")
	topK := 5

	if kStr := getFlag(args, "-k"); kStr != "" {
		if kVal, err := strconv.Atoi(kStr); err == nil && kVal > 0 {
			topK = kVal
		}
	}

	// First positional non-flag argument is the symbol
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			if symbol == "" {
				symbol = a
			}
		}
	}

	if symbol == "" {
		fmt.Fprintln(os.Stderr, "error: symbol argument is required")
		printDefUsage()
		os.Exit(1)
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	// Auto-resolve index if not specified
	if indexName == "" {
		indexes, err := gleann.ListIndexes(config.IndexDir)
		if err == nil && len(indexes) > 0 {
			indexName = indexes[0].Name
		} else {
			fmt.Fprintln(os.Stderr, "error: no index found. Build an index first with: gleann index build <name> --docs <dir>")
			os.Exit(1)
		}
	}

	embedder := newEmbedder(config)
	searcher := gleann.NewSearcher(config, embedder)
	ctx := context.Background()

	if err := searcher.Load(ctx, indexName); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index %q: %v\n", indexName, err)
		os.Exit(1)
	}
	defer searcher.Close()

	opts := gleann.DefinitionOptions{
		TopK:    topK,
		Kind:    kind,
		RootDir: config.IndexDir,
	}

	defs, err := gleann.FindDefinitions(ctx, searcher, symbol, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error finding definitions: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(defs)
		return
	}

	if len(defs) == 0 {
		fmt.Printf("No definition found for symbol %q in index %q.\n", symbol, indexName)
		return
	}

	fmt.Print(gleann.FormatDefinitions(defs))
}
