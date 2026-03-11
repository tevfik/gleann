package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tevfik/gleann/internal/tui"
)

func cmdConfig(args []string) {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "show":
		cmdConfigShow()
	case "path":
		cmdConfigPath()
	case "edit":
		cmdConfigEdit()
	case "validate":
		cmdConfigValidate()
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: gleann config <show|path|edit|validate>")
		os.Exit(1)
	}
}

func cmdConfigShow() {
	cfg := tui.LoadSavedConfig()
	if cfg == nil {
		fmt.Println("No configuration found. Run 'gleann setup' to configure.")
		return
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error serializing config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func cmdConfigPath() {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".gleann", "config.json")
	fmt.Println(p)
}

func cmdConfigEdit() {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".gleann", "config.json")

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Try common editors.
		for _, e := range []string{"nano", "vim", "vi"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		fmt.Fprintln(os.Stderr, "error: no editor found. Set $EDITOR environment variable.")
		os.Exit(1)
	}

	// Ensure config file exists.
	if _, err := os.Stat(p); os.IsNotExist(err) {
		fmt.Printf("Config file does not exist. Creating default at %s\n", p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	cmd := exec.Command(editor, p)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error running editor: %v\n", err)
		os.Exit(1)
	}
}

func cmdConfigValidate() {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".gleann", "config.json")

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("⚠️  No config file found at", p)
			fmt.Println("   Run 'gleann setup' to create one.")
			return
		}
		fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		os.Exit(1)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Invalid JSON in %s:\n   %v\n", p, err)
		os.Exit(1)
	}

	// Try loading as OnboardResult.
	cfg := tui.LoadSavedConfig()
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "❌ Config file exists but could not be parsed.\n")
		os.Exit(1)
	}

	fmt.Println("✅ Config is valid.")
	fmt.Printf("   Path:      %s\n", p)
	fmt.Printf("   Provider:  %s\n", valueOrDefault(cfg.EmbeddingProvider, "(default)"))
	fmt.Printf("   Model:     %s\n", valueOrDefault(cfg.EmbeddingModel, "(default)"))
	fmt.Printf("   LLM Model: %s\n", valueOrDefault(cfg.LLMModel, "(default)"))
	fmt.Printf("   Host:      %s\n", valueOrDefault(cfg.OllamaHost, "(default)"))
	fmt.Printf("   Index Dir: %s\n", valueOrDefault(cfg.IndexDir, "(default)"))

	if len(cfg.Roles) > 0 {
		fmt.Printf("   Roles:     %d custom\n", len(cfg.Roles))
	}
}

func valueOrDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
