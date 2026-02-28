package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tevfik/gleann/internal/server"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/internal/backend/hnsw"
)

func main() {
	config := gleann.DefaultConfig()
	homeDir, _ := os.UserHomeDir()
	config.IndexDir = filepath.Join(homeDir, ".gleann", "indexes")

	addr := ":8080"

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--addr":
			if i+1 < len(os.Args) {
				addr = os.Args[i+1]
				i++
			}
		case "--index-dir":
			if i+1 < len(os.Args) {
				config.IndexDir = os.Args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(os.Args) {
				config.EmbeddingModel = os.Args[i+1]
				i++
			}
		case "--provider":
			if i+1 < len(os.Args) {
				config.EmbeddingProvider = os.Args[i+1]
				i++
			}
		case "--help", "-h":
			fmt.Println("gleann-server — REST API server for gleann vector database")
			fmt.Println()
			fmt.Println("Usage: gleann-server [options]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --addr <addr>         Listen address (default: :8080)")
			fmt.Println("  --index-dir <dir>     Index storage directory")
			fmt.Println("  --model <model>       Embedding model (default: bge-m3)")
			fmt.Println("  --provider <provider> Embedding provider (default: ollama)")
			os.Exit(0)
		}
	}

	srv := server.NewServer(config, addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-stop
		fmt.Println("\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()

	fmt.Printf("🚀 gleann-server starting on %s\n", addr)
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
