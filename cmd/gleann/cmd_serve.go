package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tevfik/gleann/internal/autosetup"
	"github.com/tevfik/gleann/internal/server"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdServe(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Println(`Usage: gleann serve [options]

Start the Gleann REST API server, Swagger UI, OpenAI-compatible proxy, and A2A service.

Options:
  --addr <host:port>    Listen address (default: 127.0.0.1:8081; use 0.0.0.0:<port> to expose it)
  --port, -p <port>     Port number (shorthand for --addr 127.0.0.1:<port>, loopback only)
  --host <url>          Ollama host URL
  --quiet               Suppress model bootstrap output
  --ollama-api-key <k>  API key for Ollama / proxy authentication`)
		return
	}

	// Auto-bootstrap: if no config exists, detect Ollama and create one.
	ollamaHost := getFlag(args, "--host")
	quiet := hasFlag(args, "--quiet")
	autosetup.SetOllamaAPIKey(getFlag(args, "--ollama-api-key"))
	if _, err := autosetup.EnsureConfig(ollamaHost, quiet); err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-setup failed: %v\n", err)
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	// Ensure required models are available in Ollama (skip for llamacpp/openai etc).
	if !quiet {
		var modelsToEnsure []string
		if config.EmbeddingProvider == "ollama" && config.EmbeddingModel != "" {
			modelsToEnsure = append(modelsToEnsure, config.EmbeddingModel)
		}
		if config.LLMProvider == "ollama" && config.LLMModel != "" {
			modelsToEnsure = append(modelsToEnsure, config.LLMModel)
		}
		if len(modelsToEnsure) > 0 {
			autosetup.SetOllamaAPIKey(config.OllamaAPIKey)
			autosetup.EnsureModels(config.OllamaHost, quiet, modelsToEnsure...)
		}
	}

	addr := getFlag(args, "--addr")
	if addr == "" {
		portFlag := getFlag(args, "--port")
		if portFlag == "" {
			portFlag = getFlag(args, "-p")
		}
		if portFlag != "" {
			// A bare port binds loopback only; use --addr :PORT to expose.
			if !strings.HasPrefix(portFlag, ":") {
				addr = "127.0.0.1:" + portFlag
			} else {
				addr = "127.0.0.1" + portFlag
			}
		} else if savedCfg := tui.LoadSavedConfig(); savedCfg != nil && savedCfg.ServerAddr != "" {
			addr = savedCfg.ServerAddr
		} else {
			addr = gleann.DefaultServerAddr
		}
	} else if !strings.Contains(addr, ":") {
		addr = ":" + addr
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

	configAsLoaded := config
	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  warning: failed to start embedded llamacpp: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Falling back to Ollama provider for missing models...\n")
		if config.EmbeddingProvider == "llamacpp" {
			config.EmbeddingProvider = "ollama"
			if !strings.HasSuffix(config.EmbeddingModel, ".gguf") {
				// E.g. nomic-embed-text:latest
			} else {
				config.EmbeddingModel = "nomic-embed-text"
			}
		}
		if config.LLMProvider == "llamacpp" {
			config.LLMProvider = "ollama"
			if !strings.HasSuffix(config.LLMModel, ".gguf") {
				// Keep it
			} else {
				config.LLMModel = "nemotron-3-nano:4b" // arbitrary fallback
			}
		}
	}

	config.EmbedderResolver = defaultEmbedderResolver(config)
	srv := server.NewServer(config, addr, version)
	// Remember what start-up changed in memory so a UI Save cannot persist it.
	srv.SetRuntimeOverrides(gleann.ChangedKeys(configAsLoaded, config))

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	stopped := make(chan struct{})
	go func() {
		<-stop
		fmt.Println("\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
		close(stopped)
	}()

	adminToken, err := srv.PrepareAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		cleanupLlamaCPP()
		os.Exit(1)
	}
	if !bindIsLoopbackAddr(addr) {
		fmt.Fprintf(os.Stderr, "⚠️  server listens on %s (not loopback): reachable from the network; every request needs a token\n", addr)
	}

	fmt.Printf("🚀 gleann server starting on %s\n", addr)
	if adminToken != "" {
		fmt.Printf("   Auth:      bearer token required (admin token: %s)\n", authTokenPath())
		fmt.Printf("   Web UI:    http://%s/?token=%s\n", uiHost(addr), adminToken)
		fmt.Println("   New token: gleann token create <name> [--scope query|admin]")
	} else {
		fmt.Println("   Auth:      DISABLED (loopback only)")
	}
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
	fmt.Println("   A2A Protocol (Agent-to-Agent):")
	fmt.Println("   GET  /.well-known/agent-card.json  Agent discovery card")
	fmt.Println("   POST /a2a/v1/message:send          Send message to agent")
	fmt.Println("   GET  /a2a/v1/tasks/{id}            Get task status")
	fmt.Println()

	err = srv.Start()
	if errors.Is(err, http.ErrServerClosed) {
		// Graceful stop requested by a signal: wait for srv.Stop to finish,
		// stop the embedded llama-server and exit 0.
		<-stopped
		cleanupLlamaCPP()
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		cleanupLlamaCPP()
		os.Exit(1)
	}
}

func bindIsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// uiHost turns a listen address into something a browser can open.
func uiHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}
