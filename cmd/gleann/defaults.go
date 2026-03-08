// Package main defaults — centralized constants for the gleann CLI.
package main

const (
	// DefaultEmbeddingModel is the default embedding model.
	DefaultEmbeddingModel = "bge-m3"

	// DefaultLLMModel is the default LLM model for ask/chat commands.
	DefaultLLMModel = "llama3.2"

	// DefaultProvider is the default embedding provider.
	DefaultProvider = "ollama"

	// DefaultLLMProvider is the default LLM provider.
	DefaultLLMProvider = "ollama"

	// DefaultServeAddr is the default REST API address.
	DefaultServeAddr = ":8080"

	// DefaultTopK is the default number of search results.
	DefaultTopK = 10

	// DefaultMetric is the default distance metric.
	DefaultMetric = "l2"

	// DefaultRerankModel is the default reranker model.
	DefaultRerankModel = "bge-reranker-v2-m3"

	// DefaultPluginOwner is the default GitHub/Gitea owner for plugins.
	DefaultPluginOwner = "tevfik"
)
