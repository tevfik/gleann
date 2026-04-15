package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// cmdQuickstart is a zero-friction entry point: detect the current directory,
// pick a sensible index name, verify the embedding model, build the index,
// and print MCP connection instructions.
//
// Usage:
//
//	gleann quickstart [--name <name>] [--docs <dir>] [--graph]
func cmdQuickstart(args []string) {
	fmt.Println("gleann quickstart — get started in 60 seconds")
	fmt.Println()

	// ── 1. Resolve docs directory ──────────────────────────────────────
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: cannot determine working directory")
			os.Exit(1)
		}
		docsDir = cwd
	}
	docsDir = tui.ExpandPath(docsDir)
	absDir, err := filepath.Abs(docsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// ── 2. Choose index name ───────────────────────────────────────────
	indexName := getFlag(args, "--name")
	if indexName == "" {
		indexName = filepath.Base(absDir)
		// Sanitise: lowercase, replace spaces/dots with hyphens.
		indexName = strings.ToLower(indexName)
		indexName = strings.NewReplacer(" ", "-", ".", "-").Replace(indexName)
	}
	buildGraph := hasFlag(args, "--graph")

	fmt.Printf("  Directory : %s\n", absDir)
	fmt.Printf("  Index name: %s\n", indexName)
	fmt.Printf("  Graph     : %v\n", buildGraph)
	fmt.Println()

	// ── 3. Load or prompt for config ───────────────────────────────────
	cfg := tui.LoadSavedConfig()
	if cfg == nil || !cfg.Completed {
		fmt.Println("No gleann config found.  Run 'gleann setup' first, then retry.")
		fmt.Println()
		fmt.Println("Or provide embedding options directly:")
		fmt.Println("  gleann quickstart --model nomic-embed-text")
		os.Exit(1)
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	fmt.Printf("  Embedding : %s/%s\n", config.EmbeddingProvider, config.EmbeddingModel)
	fmt.Println()

	// ── 4. Check whether the index already exists ──────────────────────
	existing, _ := gleann.ListIndexes(config.IndexDir)
	for _, m := range existing {
		if m.Name == indexName {
			fmt.Printf("Index %q already exists (%d passages).\n", indexName, m.NumPassages)
			fmt.Print("Rebuild from scratch? [y/N] ")
			if !confirmYes() {
				printNextSteps(indexName)
				return
			}
			fmt.Println()
			if err := gleann.RemoveIndex(config.IndexDir, indexName); err != nil {
				fmt.Fprintf(os.Stderr, "error removing index: %v\n", err)
				os.Exit(1)
			}
			break
		}
	}

	// ── 5. Read documents ──────────────────────────────────────────────
	fmt.Printf("Reading documents from %s…\n", absDir)

	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vault tracker unavailable: %v\n", err)
	} else {
		defer tracker.Close()
	}

	items, pluginDocs, err := readDocuments(absDir,
		config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap,
		tracker, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		os.Exit(1)
	}
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "error: no documents found in "+absDir)
		os.Exit(1)
	}
	fmt.Printf("  Found %d text chunks\n", len(items))

	// ── 6. Build index ─────────────────────────────────────────────────
	fmt.Printf("Building index %q with %s…\n", indexName, config.EmbeddingModel)

	embedder := embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(config.EmbeddingProvider),
		Model:       config.EmbeddingModel,
		BaseURL:     config.OllamaHost,
		BatchSize:   config.BatchSize,
		Concurrency: config.Concurrency,
	})
	cachedEmbedder := embedding.NewCachedComputer(embedder, embedding.CacheOptions{})

	builder, err := gleann.NewBuilder(config, cachedEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	start := time.Now()
	ctx := context.Background()
	if err := builder.Build(ctx, indexName, items); err != nil {
		fmt.Fprintf(os.Stderr, "error building index: %v\n", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)
	fmt.Printf("  ✅ Done — %d passages indexed in %s\n", len(items), elapsed.Round(time.Millisecond))

	if hits, total := cachedEmbedder.Stats(); total > 0 {
		fmt.Printf("  💾 Embedding cache: %d/%d hits (%.0f%%)\n", hits, total, cachedEmbedder.HitRate())
	}

	if buildGraph {
		buildGraphIndex(indexName, absDir, config.IndexDir, pluginDocs, nil)
	}

	fmt.Println()
	printNextSteps(indexName)
}

// confirmYes reads a y/n answer from stdin; returns true for "y" or "yes".
func confirmYes() bool {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return ans == "y" || ans == "yes"
}

// printNextSteps prints the post-build instructions.
func printNextSteps(indexName string) {
	fmt.Println("━━━ Next steps ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println("1. Start the MCP server:")
	fmt.Println("     gleann mcp")
	fmt.Println()
	fmt.Println("2. In Claude Code / Cursor / OpenCode, try:")
	fmt.Printf("     gleann_search_ids  index=%q  query=\"your question\"\n", indexName)
	fmt.Printf("     gleann_fetch       index=%q  ids=[<id>, …]\n", indexName)
	fmt.Println()
	fmt.Println("3. Track your work session:")
	fmt.Println("     gleann_session_start  name=\"my-work-session\"")
	fmt.Println()
	fmt.Println("Run 'gleann install' to auto-configure your IDE.")
	fmt.Println()
}
