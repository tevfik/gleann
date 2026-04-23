package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/multimodal"
)

func cmdMultimodal(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann multimodal analyze <file|dir> [--model <model>] [--host <url>]")
		os.Exit(1)
	}

	sub := args[0]
	switch sub {
	case "analyze":
		cmdMultimodalAnalyze(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown multimodal sub-command: %s\nusage: gleann multimodal analyze <file|dir>\n", sub)
		os.Exit(1)
	}
}

func cmdMultimodalAnalyze(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann multimodal analyze <file|dir> [--model <model>] [--host <url>]")
		os.Exit(1)
	}

	config := getConfig(args)

	target := args[0]
	model := getFlag(args, "--model")
	host := getFlag(args, "--host")

	if host == "" {
		host = config.OllamaHost
	}
	if host == "" {
		host = "http://localhost:11434"
	}
	if model == "" {
		model = config.MultimodalModel
	}
	if model == "" {
		model = multimodal.AutoDetectModel(host)
	}
	if model == "" {
		fmt.Fprintln(os.Stderr, "error: no multimodal model found. Install one: ollama pull gemma4")
		os.Exit(1)
	}

	proc := multimodal.NewProcessor(host, model)

	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if info.IsDir() {
		fmt.Printf("🔍 Scanning directory: %s (model: %s)\n\n", target, model)
		items, err := proc.ProcessDirectory(target, nil, func(current, total int, path string) {
			fmt.Printf("  [%d/%d] %s\n", current, total, filepath.Base(path))
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n✅ Processed %d multimodal files\n", len(items))
		for _, item := range items {
			desc := item.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			fmt.Printf("  📄 %s: %s\n", item.Source, desc)
		}
	} else {
		ext := strings.ToLower(filepath.Ext(target))
		fmt.Printf("🔍 Analyzing: %s (model: %s)\n\n", target, model)

		switch ext {
		case ".pdf":
			analysis, err := proc.AnalyzePDF(target, multimodal.DefaultPDFConfig())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("📄 PDF: %s (%d pages)\n\n", target, analysis.TotalPages)
			for _, page := range analysis.Pages {
				if page.Error != nil {
					fmt.Printf("  Page %d: error — %v\n", page.PageNum, page.Error)
					continue
				}
				desc := page.Description
				if page.MarkerText != "" {
					desc = page.MarkerText
				}
				if len(desc) > 300 {
					desc = desc[:300] + "..."
				}
				fmt.Printf("  Page %d: %s\n", page.PageNum, desc)
			}

		default:
			result := proc.ProcessFile(target)
			if result.Error != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", result.Error)
				os.Exit(1)
			}
			fmt.Printf("[%v] %s\n\n%s\n", result.MediaType, target, result.Description)
		}
	}
}
