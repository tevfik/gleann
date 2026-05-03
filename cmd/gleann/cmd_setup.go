package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/autosetup"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// cmdSetup is the unified setup command for Gleann.
// It handles the interactive TUI wizard by default.
// Use `gleann setup --auto` for zero-friction onboarding (detect → confirm → index).
func cmdSetup(args []string) {
	// ── Check if user wants zero-friction onboarding ───────────────────
	if hasFlag(args, "--auto") || hasFlag(args, "--quick") {
		runAutoSetup(args)
		return
	}

	// ── Backward compatibility checks ──────────────────────────────────
	if hasFlag(args, "--check") {
		if tui.CheckSetup() {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if hasFlag(args, "--bootstrap") {
		host := getFlag(args, "--host")
		if _, err := tui.Bootstrap(host); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// ── Interactive TUI wizard (default) ───────────────────────────────
	for {
		result, openPlugins, err := tui.RunOnboardWithPlugins()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if result == nil {
			fmt.Println("Setup cancelled.")
			return
		}
		if openPlugins {
			// Save config so far, then open plugin manager.
			if result.Completed {
				_ = tui.SaveConfig(*result)
			}
			tui.RunPlugins()
			continue // return to setup after plugins
		}
		if result.Uninstall {
			tui.RunInstall(result)
			return
		}
		if err := tui.SaveConfig(*result); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
		}
		fmt.Println("✅ Configuration saved to ~/.gleann/config.json")
		tui.RunInstall(result)
		return
	}
}

// runAutoSetup is the zero-friction unified onboarding logic.
func runAutoSetup(args []string) {
	fmt.Println("gleann setup --auto — zero to working in 90 seconds")
	fmt.Println()

	autoConfirm := hasFlag(args, "--yes") || hasFlag(args, "-y")

	// ── Step 1: Detect configuration ───────────────────────────────────
	ollamaHost := getFlag(args, "--host")
	dc := autosetup.DetectAll(ollamaHost, true) // prefer smaller models

	if !dc.OllamaRunning {
		fmt.Println("⚠  Ollama is not running. Start it with: ollama serve")
		fmt.Println("   Then retry: gleann setup --auto")
		fmt.Println()
		fmt.Println("   Don't have Ollama? Install: https://ollama.com/download")
		os.Exit(1)
	}

	// ── Step 2: Show detected config and ask for confirmation ──────────
	fmt.Print(autosetup.FormatDetectedConfig(dc))
	fmt.Println()

	if !autoConfirm {
		fmt.Print("→ ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "e", "a":
			fmt.Println("Run 'gleann setup' for the full advanced wizard.")
			os.Exit(0)
		case "", "y", "yes":
			// Continue
		default:
			fmt.Println("Cancelled.")
			os.Exit(0)
		}
	}

	// ── Step 3: Save configuration ─────────────────────────────────────
	if err := autosetup.ApplyDetectedConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Configuration saved")

	// ── Step 4: Ensure models are available ────────────────────────────
	fmt.Println("\nChecking models...")
	pulled, err := autosetup.EnsureModels(dc.OllamaHost, false, dc.EmbeddingModel, dc.LLMModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Println("You can manually install models with: ollama pull <model-name>")
		os.Exit(1)
	}
	if len(pulled) > 0 {
		fmt.Printf("✓ Pulled %d model(s)\n", len(pulled))
	} else {
		fmt.Println("✓ All models available")
	}

	// ── Step 5: Build index for current directory ──────────────────────
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

	indexName := getFlag(args, "--name")
	if indexName == "" {
		indexName = filepath.Base(absDir)
		indexName = strings.ToLower(indexName)
		indexName = strings.NewReplacer(" ", "-", ".", "-").Replace(indexName)
	}
	buildGraph := hasFlag(args, "--graph")

	fmt.Printf("\nBuilding index %q from %s...\n", indexName, absDir)

	config := getConfig(args)
	applySavedConfig(&config, args)

	existing, _ := gleann.ListIndexes(config.IndexDir)
	for _, m := range existing {
		if m.Name == indexName {
			fmt.Printf("Index %q already exists (%d passages). Skipping build.\n", indexName, m.NumPassages)
			printNextSteps(indexName)
			return
		}
	}

	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vault tracker unavailable: %v\n", err)
	} else {
		defer tracker.Close()
	}

	items, pluginDocs, err := readDocuments(absDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		os.Exit(1)
	}
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "error: no documents found in "+absDir)
		os.Exit(1)
	}

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

	if buildGraph {
		buildGraphIndex(indexName, absDir, config.IndexDir, pluginDocs, nil)
	}

	fmt.Println()
	printNextSteps(indexName)
}

func printNextSteps(indexName string) {
	fmt.Println("━━━ What's next? ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println("  Connect to an AI editor:")
	fmt.Println("    gleann install            # Auto-detect & configure your IDE")
	fmt.Println()
	fmt.Println("  Start the server (background):")
	fmt.Println("    gleann service start      # Runs on :8080")
	fmt.Println("    gleann service install    # Auto-start on login")
	fmt.Println()
	fmt.Println("  Use the MCP server (for Claude Code, Cursor, etc.):")
	fmt.Println("    gleann mcp                # Starts MCP on stdio")
	fmt.Println()
	fmt.Println("  Interactive chat:")
	fmt.Printf("    gleann chat %s\n", indexName)
	fmt.Println()
	fmt.Println("  Customize settings:")
	fmt.Println("    gleann setup              # Full configuration wizard")
	fmt.Println("    gleann doctor             # Health check")
}
