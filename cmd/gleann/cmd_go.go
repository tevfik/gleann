package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/autosetup"
)

// cmdGo is the zero-friction unified onboarding command.
// It performs: detect → confirm → ensure models → index → install hints.
//
// Usage: gleann go [--docs <dir>] [--name <name>] [--graph] [--yes]
func cmdGo(args []string) {
	fmt.Println("gleann go — zero to working in 90 seconds")
	fmt.Println()

	autoConfirm := hasFlag(args, "--yes") || hasFlag(args, "-y")

	// ── Step 1: Detect configuration ───────────────────────────────────
	ollamaHost := getFlag(args, "--host")
	dc := autosetup.DetectAll(ollamaHost, true) // quick-start: prefer smaller models

	if !dc.OllamaRunning {
		fmt.Println("⚠  Ollama is not running. Start it with: ollama serve")
		fmt.Println("   Then retry: gleann go")
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
		case "e":
			fmt.Println("Run 'gleann setup' to edit individual settings.")
			os.Exit(0)
		case "a":
			fmt.Println("Run 'gleann setup' for the full advanced wizard.")
			os.Exit(0)
		case "", "y", "yes":
			// Continue with detected config.
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
	fmt.Println()
	fmt.Println("Checking models...")
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
	docsDir, _ = filepath.Abs(docsDir)

	indexName := getFlag(args, "--name")
	if indexName == "" {
		indexName = filepath.Base(docsDir)
		indexName = strings.ToLower(indexName)
		indexName = strings.NewReplacer(" ", "-", ".", "-").Replace(indexName)
	}

	fmt.Println()
	fmt.Printf("Building index %q from %s...\n", indexName, docsDir)

	// Delegate to quickstart which handles the actual indexing.
	// We construct args for cmdQuickstart.
	qsArgs := []string{"--name", indexName, "--docs", docsDir}
	if hasFlag(args, "--graph") {
		qsArgs = append(qsArgs, "--graph")
	}
	cmdQuickstart(qsArgs)

	// ── Step 6: Print next steps ───────────────────────────────────────
	fmt.Println()
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
