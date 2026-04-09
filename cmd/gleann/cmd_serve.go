package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/tevfik/gleann/internal/mcp"
	"github.com/tevfik/gleann/internal/server"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdServe(args []string) {
	config := getConfig(args)
	applySavedConfig(&config, args)

	addr := getFlag(args, "--addr")
	if addr == "" {
		addr = gleann.DefaultServerAddr
	}

	// Validate port number.
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid address %q: %v\n", addr, err)
		os.Exit(1)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "error: invalid port number %q\n", portStr)
		os.Exit(1)
	}
	if port < 1024 {
		fmt.Fprintf(os.Stderr, "warning: port %d requires root privileges (did you mean :%d?)\n", port, port+8000)
	}

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	srv := server.NewServer(config, addr, version)

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-stop
		fmt.Println("\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()

	fmt.Printf("🚀 gleann server starting on %s\n", addr)
	fmt.Printf("   Index dir: %s\n", config.IndexDir)
	fmt.Printf("   Model:     %s (%s)\n", config.EmbeddingModel, config.EmbeddingProvider)
	if config.OllamaHost != "" {
		fmt.Printf("   Host:      %s\n", config.OllamaHost)
	}
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("   GET  /health                    Health check")
	fmt.Println("   GET  /api/indexes               List indexes")
	fmt.Println("   GET  /api/indexes/{name}        Index info")
	fmt.Println("   POST /api/indexes/{name}/search Search")
	fmt.Println("   POST /api/indexes/{name}/ask    Ask (RAG Q&A)")
	fmt.Println("   POST /api/indexes/{name}/build  Build index")
	fmt.Println("   DELETE /api/indexes/{name}      Delete index")
	fmt.Println()
	fmt.Println("   GET  /api/conversations         List conversations")
	fmt.Println("   GET  /api/conversations/{id}    Get conversation")
	fmt.Println("   DELETE /api/conversations/{id}  Delete conversation")
	fmt.Println()
	fmt.Println("   GET  /api/graph/{name}          Graph stats")
	fmt.Println("   POST /api/graph/{name}/query    Graph query (callees, callers, symbols_in_file)")
	fmt.Println("   POST /api/graph/{name}/index    Trigger AST graph indexing")
	fmt.Println()
	fmt.Println("   OpenAI-Compatible Proxy:")
	fmt.Println("   GET  /v1/models                 List indexes as OpenAI models")
	fmt.Printf("   POST /v1/chat/completions       RAG proxy  (model: \"gleann/<index>\")\n")
	fmt.Println()
	fmt.Println("   Memory Blocks:")
	fmt.Println("   GET    /api/blocks              List memory blocks (?tier=short|medium|long)")
	fmt.Println("   POST   /api/blocks              Store a memory block")
	fmt.Println("   DELETE /api/blocks               Clear blocks (?tier=)")
	fmt.Println("   DELETE /api/blocks/{id}          Delete specific block")
	fmt.Println("   GET    /api/blocks/search?q=     Search memories")
	fmt.Println("   GET    /api/blocks/context       Compiled LLM memory context")
	fmt.Println("   GET    /api/blocks/stats         Storage statistics")
	fmt.Println()
	fmt.Println("   GET  /metrics                   Prometheus metrics")
	fmt.Println("   GET  /api/docs                  Swagger UI")
	fmt.Println("   GET  /api/openapi.json          OpenAPI 3.0 spec")
	fmt.Println()

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdMCP() {
	savedCfg := tui.LoadSavedConfig()

	cfg := mcp.Config{
		EmbeddingProvider: DefaultProvider,
		EmbeddingModel:    DefaultEmbeddingModel,
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           version,
	}

	homeDir, _ := os.UserHomeDir()
	cfg.IndexDir = filepath.Join(homeDir, ".gleann", "indexes")

	if savedCfg != nil {
		if savedCfg.EmbeddingProvider != "" {
			cfg.EmbeddingProvider = savedCfg.EmbeddingProvider
		}
		if savedCfg.EmbeddingModel != "" {
			cfg.EmbeddingModel = savedCfg.EmbeddingModel
		}
		if savedCfg.OllamaHost != "" {
			cfg.OllamaHost = savedCfg.OllamaHost
		}
		if savedCfg.OpenAIKey != "" {
			cfg.OpenAIAPIKey = savedCfg.OpenAIKey
		}
		if savedCfg.OpenAIBaseURL != "" {
			cfg.OpenAIBaseURL = savedCfg.OpenAIBaseURL
		}
		if savedCfg.IndexDir != "" {
			cfg.IndexDir = tui.ExpandPath(savedCfg.IndexDir)
		}
	}

	server := mcp.NewServer(cfg)
	server.Run()
}
