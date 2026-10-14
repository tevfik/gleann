package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
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
	case "set":
		cmdConfigSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: gleann config <show|path|edit|validate|set>")
		os.Exit(1)
	}
}

func cmdConfigShow() {
	cfg := tui.LoadSavedConfig()
	if cfg == nil {
		fmt.Println("No configuration found. Run 'gleann setup' to configure.")
		return
	}

	data, err := json.MarshalIndent(maskConfigSecrets(*cfg), "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error serializing config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// maskConfigSecrets returns a copy of cfg with API keys masked for display
// (e.g. "sk-…"), so `gleann config show` never prints secrets in full.
func maskConfigSecrets(cfg tui.OnboardResult) tui.OnboardResult {
	cfg.OllamaAPIKey = gleann.MaskSecret(cfg.OllamaAPIKey)
	cfg.OpenAIKey = gleann.MaskSecret(cfg.OpenAIKey)
	cfg.AnthropicKey = gleann.MaskSecret(cfg.AnthropicKey)
	return cfg
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
	if cfg.OllamaAPIKey != "" {
		fmt.Printf("   Ollama Key: %s\n", gleann.MaskSecret(cfg.OllamaAPIKey))
	}
	fmt.Printf("   Index Dir: %s\n", valueOrDefault(cfg.IndexDir, "(default)"))

	if len(cfg.Roles) > 0 {
		fmt.Printf("   Roles:     %d custom\n", len(cfg.Roles))
	}

	// Verify Index Directory accessibility.
	idxDir := cfg.IndexDir
	if idxDir != "" {
		idxDir = filepath.Clean(idxDir)
		info, err := os.Stat(idxDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("\n⚠️  Index Directory does not exist: %s\n   Hint: Run 'gleann setup' or create the directory.\n", idxDir)
			} else {
				fmt.Printf("\n⚠️  Index Directory error: %v\n", err)
			}
		} else if !info.IsDir() {
			fmt.Printf("\n⚠️  Index Directory path is a file, not a directory: %s\n", idxDir)
		} else {
			// Check writeability
			tempFile := filepath.Join(idxDir, ".gleann_write_test")
			if err := os.WriteFile(tempFile, []byte("test"), 0o644); err != nil {
				fmt.Printf("\n⚠️  Index Directory is not writeable: %v\n", err)
			} else {
				os.Remove(tempFile)
			}
		}
	}

	// Verify Ollama connectivity.
	if cfg.EmbeddingProvider == "ollama" || cfg.LLMProvider == "ollama" {
		host := cfg.OllamaHost
		if host == "" {
			host = "http://localhost:11434"
		}
		client := http.Client{
			Timeout: 2 * time.Second,
		}
		resp, err := gleann.OllamaGet(&client, host+"/api/tags", gleann.ResolveOllamaAPIKey(cfg.OllamaAPIKey))
		if err != nil {
			fmt.Printf("\n⚠️  Ollama service at %s is unreachable.\n   Hint: Make sure Ollama is running ('ollama serve') or check your network/host configuration.\n", host)
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				fmt.Printf("\n⚠️  Ollama service at %s rejected the request (HTTP %d).\n   Hint: Set an API key with 'gleann config set ollama_api_key <key>' or OLLAMA_API_KEY.\n", host, resp.StatusCode)
			}
		}
	}

	// Verify EIF model file if configured
	if cfg.EmbeddingProvider == "eif" && cfg.EmbeddingModel != "" {
		if _, err := os.Stat(cfg.EmbeddingModel); err != nil {
			fmt.Printf("\n⚠️  EIF model file not found at: %s\n   Hint: Place a valid BERT model (.gguf or .eifm) in ~/.gleann/models/\n", cfg.EmbeddingModel)
		}
	}
}

func valueOrDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func cmdConfigSet(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann config set <key> <value>")
		fmt.Fprintln(os.Stderr, "examples:")
		fmt.Fprintln(os.Stderr, "  gleann config set ollama_host http://localhost:11435")
		fmt.Fprintln(os.Stderr, "  gleann config set ollama_api_key <key>   # optional; Ollama behind an auth proxy")
		fmt.Fprintln(os.Stderr, "  gleann config set embedding_provider eif")
		fmt.Fprintln(os.Stderr, "  gleann config set embedding_model bge-m3-q8_0.gguf")
		fmt.Fprintln(os.Stderr, "  gleann config set code_model jina-code-q8_0.gguf")
		fmt.Fprintln(os.Stderr, "  gleann config set doc_model bge-small-en-q8_0.gguf")
		fmt.Fprintln(os.Stderr, "  gleann config set llm_model qwen2.5:7b")
		os.Exit(1)
	}
	key := strings.ToLower(args[0])
	val := args[1]

	cfg := tui.LoadSavedConfig()
	if cfg == nil {
		cfg = &tui.OnboardResult{}
	}
	display, err := applyConfigSet(cfg, key, val)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := tui.SaveConfig(*cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error updating config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Updated %s = %s\n", key, display)
}

// applyConfigSet applies `gleann config set <key> <val>` to cfg and returns
// the value to display (secrets are masked). key must already be lowercased.
func applyConfigSet(cfg *tui.OnboardResult, key, val string) (string, error) {
	display := val
	switch key {
	case "ollama_host", "ollama-host", "host", "port":
		if !strings.Contains(val, "://") {
			if !strings.Contains(val, ":") {
				val = "http://localhost:" + val
			} else {
				val = "http://" + val
			}
		}
		cfg.OllamaHost = val
		display = val
	case "ollama_api_key", "ollama-api-key", "ollama_key", "ollama-key":
		cfg.OllamaAPIKey = val
		display = gleann.MaskSecret(val)
	case "embedding_provider", "emb_provider":
		cfg.EmbeddingProvider = val
	case "embedding_model", "emb_model":
		cfg.EmbeddingModel = val
	case "code_embedding_model", "code_model", "code-model", "code-embedding-model":
		cfg.CodeEmbeddingModel = val
	case "doc_embedding_model", "doc_model", "doc-model", "docs_model", "docs-model", "doc-embedding-model", "docs-embedding-model":
		cfg.DocEmbeddingModel = val
	case "llm_provider":
		cfg.LLMProvider = val
	case "llm_model":
		cfg.LLMModel = val
	case "openai_api_key", "openai_key", "api_key", "api-key", "openai-api-key":
		cfg.OpenAIKey = val
		display = gleann.MaskSecret(val)
	case "openai_base_url", "openai-base-url", "base_url", "base-url":
		cfg.OpenAIBaseURL = val
	case "anthropic_api_key", "anthropic_key":
		cfg.AnthropicKey = val
		display = gleann.MaskSecret(val)
	case "index_dir", "index-dir":
		cfg.IndexDir = val
	case "server_addr", "server-addr":
		cfg.ServerAddr = val
	case "backend":
		cfg.Backend = val
	default:
		return "", fmt.Errorf("unknown config key: %s", key)
	}
	if display == "" {
		display = `""`
	}
	cfg.Completed = true
	return display, nil
}

