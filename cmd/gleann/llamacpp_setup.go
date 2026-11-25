package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/backend/llamacpp"
	"github.com/tevfik/gleann/pkg/gleann"
)

// llamaRunner is the part of *llamacpp.Runner the CLI uses.
type llamaRunner interface {
	SetEmbedding(bool)
	Start(context.Context) error
	Stop() error
	BaseURL() string
}

var globalLlamaRunner llamaRunner

// llamaRunnerQuiet remembers whether the runner was started under --quiet, so
// the deferred cleanup prints the shutdown status line only in loud commands.
var llamaRunnerQuiet bool

// newLlamaRunner creates the embedded llama-server runner; a seam for tests.
var newLlamaRunner = func(model string) llamaRunner { return llamacpp.NewRunner(model) }

// initLlamaCPP intercepts the configuration. If llamacpp is requested, it starts
// the embedded server and rewrites the config to use the standard "openai" provider.
// It keeps the historical loud behaviour; commands that support --quiet use
// initLlamaCPPQuiet.
func initLlamaCPP(ctx context.Context, config *gleann.Config) error {
	return initLlamaCPPQuiet(ctx, config, false)
}

// initLlamaCPPQuiet is initLlamaCPP with --quiet handling: the startup status
// lines are suppressed, but a server that fails to start still returns an
// error for the caller to print.
func initLlamaCPPQuiet(ctx context.Context, config *gleann.Config, quiet bool) error {
	if config.EmbeddingProvider != "llamacpp" {
		return nil
	}

	if config.EmbeddingModel == "" {
		return fmt.Errorf("llamacpp provider requires a .gguf model (a path, a file name in ~/.gleann/models, or a catalogue id such as 'bge-m3') as --model")
	}

	// Opt-in only (llamacpp_config.auto_download): fetch a missing catalogue
	// model in the foreground before the runner needs it.
	if err := ensureLlamaCPPModels(ctx, config, quiet); err != nil {
		return err
	}
	// A catalogue id (or the first-run default) is resolved to its file at
	// run time; the config itself is left as the user wrote it.
	modelPath, err := resolveLlamaCPPModel(config.EmbeddingModel)
	if err != nil {
		return err
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "🚀 Starting embedded llama.cpp server for model %s\n", modelPath)
	}
	globalLlamaRunner = newLlamaRunner(modelPath)
	llamaRunnerQuiet = quiet
	globalLlamaRunner.SetEmbedding(true)

	if err := globalLlamaRunner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start embedded llama-server: %w", err)
	}

	// Override configuration to use the internal server
	config.EmbeddingProvider = "openai"
	config.OpenAIBaseURL = globalLlamaRunner.BaseURL()
	config.OllamaHost = globalLlamaRunner.BaseURL()
	config.OpenAIAPIKey = "gleann-embedded"
	if config.BatchSize <= 0 {
		config.BatchSize = 32
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 2
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "✅ Embedded llama-server is ready at %s\n", config.OpenAIBaseURL)
	}
	return nil
}

// cleanupLlamaCPP ensures the server is stopped when the command exits. The
// shutdown status line honours the quiet state of the init that started it;
// the runner is stopped and forgotten either way.
func cleanupLlamaCPP() {
	if globalLlamaRunner != nil {
		if !llamaRunnerQuiet {
			fmt.Fprintf(os.Stderr, "\nShutting down embedded llama-server...\n")
		}
		globalLlamaRunner.Stop()
		globalLlamaRunner = nil
		llamaRunnerQuiet = false
	}
}

// applyLlamaChatOverride forces the chat config to use the embedded server if it's running.
func applyLlamaChatOverride(chatConfig *gleann.ChatConfig) {
	if globalLlamaRunner != nil {
		chatConfig.Provider = gleann.LLMOpenAI
		chatConfig.BaseURL = globalLlamaRunner.BaseURL()
		chatConfig.APIKey = "gleann-embedded"
	}
}
