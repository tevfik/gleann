// Package tui provides the interactive terminal user interface for gleann.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tevfik/gleann/internal/backend/llamacpp"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/pkg/backends"
)

// Run starts the interactive TUI application loop.
// It shows the home screen and routes to sub-screens.
func Run() error {
	for {
		// ── Home screen ──
		home := NewHomeModel()
		p := tea.NewProgram(home, tea.WithAltScreen())
		result, err := p.Run()
		if err != nil {
			return fmt.Errorf("home screen: %w", err)
		}
		h := result.(HomeModel)
		if h.Quitting() {
			return nil
		}

		switch h.Chosen() {
		case ScreenOnboard:
			if err := runOnboard(); err != nil {
				return err
			}
		case ScreenChat:
			if err := runChatFlow(); err != nil {
				return err
			}
		case ScreenIndexes:
			if err := runIndexManage(); err != nil {
				return err
			}
		case ScreenPlugins:
			if err := runPlugins(); err != nil {
				return err
			}
		}
	}
}

// RunOnboard runs the onboarding wizard standalone and returns the result.
func RunOnboard() (*OnboardResult, error) {
	var m OnboardModel
	if cfg := LoadSavedConfig(); cfg != nil && cfg.Completed {
		m = NewOnboardModelWithConfig(cfg)
	} else {
		m = NewOnboardModel()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("onboard: %w", err)
	}
	ob := result.(OnboardModel)
	if ob.Cancelled() {
		return nil, nil
	}
	r := ob.Result()
	return &r, nil
}

// RunOnboardWithPlugins runs the onboarding wizard and also returns whether
// the user chose "Manage Plugins" from the settings menu.
func RunOnboardWithPlugins() (*OnboardResult, bool, error) {
	var m OnboardModel
	if cfg := LoadSavedConfig(); cfg != nil && cfg.Completed {
		m = NewOnboardModelWithConfig(cfg)
	} else {
		m = NewOnboardModel()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return nil, false, fmt.Errorf("onboard: %w", err)
	}
	ob := result.(OnboardModel)
	if ob.Cancelled() {
		return nil, false, nil
	}
	r := ob.Result()
	return &r, ob.OpenPlugins(), nil
}

// RunPlugins launches the plugin management screen standalone.
func RunPlugins() error {
	return runPlugins()
}

// RunChat runs the chat TUI standalone for a given index.
func RunChat(chat *gleann.LeannChat, indexName, modelName string) error {
	m := NewChatModel(chat, indexName, modelName)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()

	home, _ := os.UserHomeDir()
	sessionDir := filepath.Join(home, ".gleann", "chatsessions")
	savedFile, saveErr := chat.SaveSession(sessionDir, indexName)
	if saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save chat session: %v\n", saveErr)
	} else if savedFile != "" {
		fmt.Printf("\nChat session saved to %s\n", savedFile)
	}

	return err
}

// --- internal multi-screen orchestration ---

func runOnboard() error {
	var m OnboardModel
	if cfg := LoadSavedConfig(); cfg != nil && cfg.Completed {
		m = NewOnboardModelWithConfig(cfg)
	} else {
		m = NewOnboardModel()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("onboard: %w", err)
	}
	ob := result.(OnboardModel)
	if ob.Cancelled() {
		return nil // go back to home
	}

	// If the user chose "Manage Plugins", launch the plugin screen.
	if ob.OpenPlugins() {
		r := ob.Result()
		if r.Completed {
			_ = SaveConfig(r)
		}
		return runPlugins()
	}

	r := ob.Result()
	if r.Uninstall {
		RunInstall(&r)
		fmt.Println("\nPress Enter to return to main menu...")
		fmt.Scanln()
		return nil
	}

	if r.Completed {
		// Save config to ~/.gleann/config.json
		if err := SaveConfig(r); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
		}
		if r.InstallPath != "" {
			RunInstall(&r)
			fmt.Println("\nPress Enter to return to main menu...")
			fmt.Scanln()
		}
	}
	return nil
}

// RunChatFlow launches the chat flow: index picker → searcher → chat TUI.
func RunChatFlow() error {
	return runChatFlow()
}

func runIndexManage() error {
	cfg := loadConfig()
	m := NewIndexManageModel(cfg.IndexDir)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("index manager: %w", err)
	}
	return nil
}

func runPlugins() error {
	m := NewPluginModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("plugins: %w", err)
	}
	return nil
}

func runChatFlow() error {
	// Load config.
	cfg := loadConfig()

	// Pick an index.
	idxModel := NewIndexListModel(cfg.IndexDir)
	p := tea.NewProgram(idxModel, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("index list: %w", err)
	}
	il := result.(IndexListModel)
	indexName := il.Selected()
	if indexName == "" {
		return nil // cancelled
	}

	// Clean dummy "auto-scan" host strings injected by UI
	embHost := cfg.OllamaHost
	if strings.Contains(embHost, "(auto-scan") {
		embHost = ""
	}

	// Start embedded servers if needed.
	if cfg.EmbeddingProvider == "llamacpp" {
		fmt.Printf("🚀 Starting embedded llama.cpp server for embedding model %s\n", cfg.EmbeddingModel)
		embedRunner := llamacpp.NewRunner(cfg.EmbeddingModel)
		if err := embedRunner.Start(context.Background()); err != nil {
			return fmt.Errorf("failed to start embedded llama-server: %w", err)
		}
		defer embedRunner.Stop()

		cfg.EmbeddingProvider = "openai"
		embHost = embedRunner.BaseURL()
		cfg.OpenAIAPIKey = "gleann-embedded"
		fmt.Printf("✅ Embedded llama-server is ready at %s\n", embHost)
	}

	// Set up searcher.
	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(cfg.EmbeddingProvider),
		Model:    cfg.EmbeddingModel,
		BaseURL:  embHost,
	})
	searcher := gleann.NewSearcher(cfg, embedder)
	ctx := context.Background()
	if err := searcher.Load(ctx, indexName); err != nil {
		return fmt.Errorf("load index %q: %w", indexName, err)
	}
	defer searcher.Close()

	// Set up chat.
	chatCfg := gleann.DefaultChatConfig()
	// Use saved LLM settings if available.
	savedCfg := LoadSavedConfig()
	if savedCfg != nil {
		if savedCfg.LLMProvider != "" {
			chatCfg.Provider = gleann.LLMProvider(savedCfg.LLMProvider)
		}
		if savedCfg.LLMModel != "" {
			chatCfg.Model = savedCfg.LLMModel
		}
		if savedCfg.OllamaHost != "" && !strings.Contains(savedCfg.OllamaHost, "(auto-scan") {
			chatCfg.BaseURL = savedCfg.OllamaHost
		}
		if savedCfg.OpenAIKey != "" {
			chatCfg.APIKey = savedCfg.OpenAIKey
		}

		if savedCfg.LLMProvider == "llamacpp" {
			fmt.Printf("🚀 Starting embedded llama.cpp server for chat model %s\n", chatCfg.Model)
			llmRunner := llamacpp.NewRunner(chatCfg.Model)
			if err := llmRunner.Start(context.Background()); err != nil {
				return fmt.Errorf("failed to start embedded llama-server for chat: %w", err)
			}
			defer llmRunner.Stop()

			chatCfg.Provider = gleann.LLMOpenAI
			chatCfg.BaseURL = llmRunner.BaseURL()
			chatCfg.APIKey = "gleann-embedded"
			fmt.Printf("✅ Embedded chat llama-server is ready at %s\n", chatCfg.BaseURL)
		}
	}
	// Ensure BaseURL for Ollama.
	if chatCfg.Provider == gleann.LLMOllama && chatCfg.BaseURL == "" {
		chatCfg.BaseURL = cfg.OllamaHost
	}

	chat := gleann.NewChat(searcher, chatCfg)

	return RunChat(chat, indexName, chatCfg.Model)
}

// loadConfig returns a gleann.Config from saved config or defaults.
func loadConfig() gleann.Config {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = DefaultIndexDir()

	saved := LoadSavedConfig()
	if saved != nil {
		if saved.EmbeddingProvider != "" {
			cfg.EmbeddingProvider = saved.EmbeddingProvider
		}
		if saved.EmbeddingModel != "" {
			cfg.EmbeddingModel = saved.EmbeddingModel
		}
		if saved.OllamaHost != "" {
			cfg.OllamaHost = saved.OllamaHost
		}
		if saved.OpenAIKey != "" {
			cfg.OpenAIAPIKey = saved.OpenAIKey
		}
		if saved.IndexDir != "" {
			cfg.IndexDir = saved.IndexDir // already expanded by LoadSavedConfig
		}
	}
	return cfg
}
