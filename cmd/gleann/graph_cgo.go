//go:build treesitter

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tevfik/gleann/internal/graph/indexer"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

func init() {
	gleann.GraphDBOpener = func(dir string) (gleann.GraphDB, error) {
		return kgraph.Open(dir)
	}
}

// buildGraphIndex builds the AST graph index for the given directory
// and writes document graph nodes/edges for plugin-extracted documents.
// Only available when built with -tags treesitter (requires CGo + KuzuDB).
func buildGraphIndex(name, docsDir, indexDir string, pluginDocs []*PluginDoc) {
	fmt.Printf("🕸️  Building API Graph Index from %s...\n", docsDir)
	graphStart := time.Now()

	dbPath := filepath.Join(indexDir, name+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not initialize kuzu graph db: %v\n", err)
		return
	}
	defer db.Close()

	// 1. AST code indexing (existing pipeline).
	idx := indexer.New(db, "github.com/tevfik/gleann", docsDir)
	if err := idx.IndexDir(docsDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: graph indexing failed: %v\n", err)
	} else {
		fmt.Printf("✅ Code Graph Index built in %s\n", time.Since(graphStart).Round(time.Millisecond))
	}

	// 2. Document graph indexing (plugin-extracted documents).
	if len(pluginDocs) > 0 {
		docStart := time.Now()
		docIdx := indexer.NewDocIndexer(db, 512, 64)
		var docErrors int
		for _, pd := range pluginDocs {
			if err := docIdx.WriteGraph(pd.Result, pd.SourcePath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: doc graph indexing failed for %s: %v\n", pd.SourcePath, err)
				docErrors++
			}
		}
		indexed := len(pluginDocs) - docErrors
		fmt.Printf("📄 Document Graph: %d documents indexed in %s\n", indexed, time.Since(docStart).Round(time.Millisecond))
	}
}

// cmdGraph implements the `gleann graph deps/callers` command.
// Only available when built with -tags treesitter.
func cmdGraph(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann graph <deps|callers> <symbol_fqn>")
		os.Exit(1)
	}

	subCmd := args[0]
	symbol := args[1]
	config := getConfig(args)

	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required for graph queries")
		os.Exit(1)
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()

	switch subCmd {
	case "deps":
		callees, err := db.Callees(symbol)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🕸️  Dependencies for %s (%d):\n", symbol, len(callees))
		for _, c := range callees {
			fmt.Printf("  → [%s] %s\n", c.Kind, c.FQN)
		}
	case "callers":
		callers, err := db.Callers(symbol)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🕸️  Callers of %s (%d):\n", symbol, len(callers))
		for _, c := range callers {
			fmt.Printf("  ← [%s] %s\n", c.Kind, c.FQN)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown graph command: %s (use deps or callers)\n", subCmd)
		os.Exit(1)
	}
}
