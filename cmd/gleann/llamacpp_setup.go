package main

import (
	"context"
	"fmt"

	"github.com/tevfik/gleann/internal/backend/llamacpp"
	"github.com/tevfik/gleann/pkg/gleann"
)

var globalLlamaRunner *llamacpp.Runner

// initLlamaCPP intercepts the configuration. If llamacpp is requested, it starts
// the embedded server and rewrites the config to use the standard "openai" provider.
func initLlamaCPP(ctx context.Context, config *gleann.Config) error {
	if config.EmbeddingProvider != "llamacpp" {
		return nil
	}

	if config.EmbeddingModel == "" || config.EmbeddingModel == "bge-m3" {
		return fmt.Errorf("llamacpp provider requires an absolute path to a .gguf file as --model")
	}

	fmt.Printf("🚀 Starting embedded llama.cpp server for model %s\n", config.EmbeddingModel)
	globalLlamaRunner = llamacpp.NewRunner(config.EmbeddingModel)

	if err := globalLlamaRunner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start embedded llama-server: %w", err)
	}

	// Override configuration to use the internal server
	config.EmbeddingProvider = "openai"
	config.OpenAIBaseURL = globalLlamaRunner.BaseURL()
	config.OpenAIAPIKey = "gleann-embedded"

	fmt.Printf("✅ Embedded llama-server is ready at %s\n", config.OpenAIBaseURL)
	return nil
}

// cleanupLlamaCPP ensures the server is stopped when the command exits.
func cleanupLlamaCPP() {
	if globalLlamaRunner != nil {
		fmt.Printf("\nShutting down embedded llama-server...\n")
		globalLlamaRunner.Stop()
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
