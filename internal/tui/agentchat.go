package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// agentChat adapts the shared gleann chat state (settings, memory, attachments,
// token budget) to the agenttui engine. It reuses the command handlers of the
// classic ChatModel, which only read and write that state and return text.
type agentChat struct {
	mu  sync.Mutex
	cm  *ChatModel
	app *agenttui.App
}

// RunAgentChat runs the chat on the agenttui engine (streaming with
// cancellation, collapsible reasoning, source cards, slash-command popup).
func RunAgentChat(chat *gleann.LeannChat, indexName, modelName string) error {
	cm := NewChatModel(chat, indexName, modelName)
	ac := &agentChat{cm: &cm}

	ac.app = agenttui.New(agenttui.Config{
		Title:        "gleann",
		Subtitle:     "RAG chat · " + indexName,
		ModelName:    modelName,
		WorkDir:      mustGetwd(),
		Placeholder:  "Ask a question about your index… (/ for commands)",
		Theme:        agenttui.ThemeByName(os.Getenv("GLEANN_THEME")),
		IgnoreDirs:   []string{".gleann"},
		ShowHeader:   true,
		ShowFooter:   true,
		EnableMouse:  true,
		StreamEvents: ac.stream,
		Commands:     ac.commands(),
	})
	ac.app.SetContextTokens(0, cm.contextLimit)

	for _, m := range chat.History() {
		role := agenttui.RoleUser
		if m.Role == "assistant" {
			role = agenttui.RoleAssistant
		} else if m.Role == "system" {
			role = agenttui.RoleSystem
		}
		ac.app.AddMessage(agenttui.Message{Role: role, Content: m.Content})
	}
	ac.app.AddMessage(agenttui.Message{
		Role:    agenttui.RoleSystem,
		Content: fmt.Sprintf("Connected to index %q — model: %s. Type / for commands.", indexName, modelName),
	})
	go ac.app.ScanRepoFiles(mustGetwd())

	err := ac.app.Run(context.Background())

	// Same wrap-up as the classic chat: end memory session, save the transcript.
	if memMgr, memErr := memory.DefaultManager(); memErr == nil {
		_ = memMgr.EndSession()
		memMgr.Close()
	}
	home, _ := os.UserHomeDir()
	if saved, saveErr := chat.SaveSession(filepath.Join(home, ".gleann", "chatsessions"), indexName); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save chat session: %v\n", saveErr)
	} else if saved != "" {
		fmt.Printf("\nChat session saved to %s\n", saved)
	}
	return err
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// stream answers one prompt: retrieval + generation, reporting reasoning as
// Thinking events and the retrieved passages as a "sources" tool card.
func (ac *agentChat) stream(ctx context.Context, prompt string, emit func(agenttui.Event)) (int, error) {
	ac.mu.Lock()
	cm := ac.cm
	chat, topK, rerank := cm.chat, cm.topK, cm.rerankEnabled
	images := cm.pendingImages
	cm.pendingImages = nil
	attached := append([]string(nil), cm.attachedFiles...)
	memMgr := cm.memoryMgr
	cm.tokensSent += len(prompt) / 4
	ac.mu.Unlock()

	if memMgr != nil {
		if cw, err := memMgr.BuildContext(); err == nil {
			chat.SetMemoryContext(cw.Render())
		}
	}
	question := prompt + attachedFilesContext(attached)

	chat.SetThinkingHandler(func(tok string) {
		emit(agenttui.Event{Kind: agenttui.EventThinking, Text: tok})
	})
	defer chat.SetThinkingHandler(nil)

	var answered int
	cb := func(tok string) {
		answered += len(tok)
		emit(agenttui.Event{Kind: agenttui.EventText, Text: tok})
	}
	opts := []gleann.SearchOption{gleann.WithTopK(topK), gleann.WithReranker(rerank)}

	var results []gleann.SearchResult
	var err error
	if len(images) > 0 {
		results, err = chat.AskStreamWithImages(ctx, question, images, cb, opts...)
	} else {
		results, err = chat.AskStream(ctx, question, cb, opts...)
	}

	ac.mu.Lock()
	cm.tokensReceived += answered / 4
	total := cm.tokensSent + cm.tokensReceived
	ac.mu.Unlock()
	ac.app.SetContextTokens(total, cm.contextLimit)

	if err != nil {
		return total, err
	}
	if len(results) > 0 {
		emit(agenttui.Event{Kind: agenttui.EventToolStart, ToolID: "sources", ToolName: "sources",
			ToolArgs: fmt.Sprintf("%d passages", len(results))})
		emit(agenttui.Event{Kind: agenttui.EventToolEnd, ToolID: "sources", ToolOutput: formatSources(results), ToolOK: true})
	}
	return total, nil
}

// formatSources lists retrieved passages with their source and score.
func formatSources(results []gleann.SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		src := ""
		for _, k := range []string{"source", "path", "file", "title"} {
			if v, ok := r.Metadata[k].(string); ok && v != "" {
				src = v
				break
			}
		}
		if src == "" {
			src = truncStr(strings.Join(strings.Fields(r.Text), " "), 60)
		}
		fmt.Fprintf(&sb, "%d. %s (%.2f)\n", i+1, src, r.Score)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// withState runs fn on the shared chat state under the lock and returns its text.
func (ac *agentChat) withState(fn func(cm *ChatModel) string) string {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return fn(ac.cm)
}

func (ac *agentChat) commands() []agenttui.SlashCommand {
	// local wraps a text-returning handler as a local (non-LLM) slash command.
	local := func(name, desc, usage string, fn func(cm *ChatModel, arg string) string) agenttui.SlashCommand {
		return agenttui.SlashCommand{
			Name: name, Description: desc, Usage: usage,
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				arg := strings.TrimSpace(strings.Join(args, " "))
				return ac.withState(func(cm *ChatModel) string { return fn(cm, arg) }), true, nil
			},
		}
	}

	cmds := []agenttui.SlashCommand{
		{
			Name: "new", Description: "Start a fresh conversation (clears history, attachments, budget)",
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				ac.withState(func(cm *ChatModel) string {
					cm.chat.ClearHistory()
					cm.pendingImages, cm.attachedFiles = nil, nil
					cm.tokensSent, cm.tokensReceived = 0, 0
					return ""
				})
				a.ClearMessages()
				a.SetContextTokens(0, ac.cm.contextLimit)
				return "New conversation started.", true, nil
			},
		},
		{
			Name: "history", Description: "List saved conversations",
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				items, err := conversations.DefaultStore().List()
				if err != nil || len(items) == 0 {
					return "No saved conversations found.", true, nil
				}
				var sb strings.Builder
				sb.WriteString("### Saved conversations\n\n")
				for i, c := range items {
					if i >= 20 {
						break
					}
					fmt.Fprintf(&sb, "- `%s` %s\n", c.ID, c.Title)
				}
				return sb.String(), true, nil
			},
		},
		local("remember", "Store a memory", "<text>", func(cm *ChatModel, arg string) string {
			if cm.memoryMgr == nil {
				return "⚠ Memory system unavailable"
			}
			if arg == "" {
				return "Usage: /remember <important information>"
			}
			b, err := cm.memoryMgr.Remember(arg)
			if err != nil {
				return "⚠ Error: " + err.Error()
			}
			return fmt.Sprintf("✅ Remembered [%s]: %s", b.ID[:8], arg)
		}),
		local("forget", "Forget memories matching a query or ID", "<query|id>", func(cm *ChatModel, arg string) string {
			if cm.memoryMgr == nil {
				return "⚠ Memory system unavailable"
			}
			if arg == "" {
				return "Usage: /forget <query or ID>"
			}
			n, err := cm.memoryMgr.Forget(arg)
			if err != nil {
				return "⚠ " + err.Error()
			}
			return fmt.Sprintf("🗑 Forgot %d memory block(s) matching %q", n, arg)
		}),
		local("memories", "List stored memories", "", func(cm *ChatModel, _ string) string {
			if cm.memoryMgr == nil {
				return "⚠ Memory system unavailable"
			}
			blocks, err := cm.memoryMgr.List("")
			if err != nil || len(blocks) == 0 {
				return "No memories stored."
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "🧠 Memories (%d):\n\n", len(blocks))
			for _, b := range blocks {
				fmt.Fprintf(&sb, "- `%s` %s %s — %s\n", b.ID[:8], b.Tier, b.Label, truncStr(b.Content, 60))
			}
			return sb.String()
		}),
		local("image", "Queue an image for the next message", "<path>", func(cm *ChatModel, arg string) string { return cm.handleImageCommand(arg) }),
		local("screenshot", "Capture screen/clipboard for the next message", "[arg]", func(cm *ChatModel, arg string) string { return cm.handleScreenshotCommand(arg) }),
		local("audio", "Queue an audio file", "<path>", func(cm *ChatModel, arg string) string { return cm.handleAudioCommand(arg) }),
		local("attach", "Add a file or directory to the session context", "<path>", func(cm *ChatModel, arg string) string { return cm.handleAttachCommand(arg) }),
		local("detach", "Remove an attached file", "<path>", func(cm *ChatModel, arg string) string { return cm.handleDetachCommand(arg) }),
		local("index", "Show index info or switch index", "[name|info|add|remove]", func(cm *ChatModel, arg string) string { return cm.handleIndexCommand(arg) }),
		local("budget", "Show token usage", "", func(cm *ChatModel, _ string) string { return cm.handleBudgetCommand() }),
		local("video", "Analyze a video", "<path>", func(cm *ChatModel, arg string) string { return cm.handleVideoCommand(arg) }),
		local("pdf", "Extract and index a PDF", "<path>", func(cm *ChatModel, arg string) string { return cm.handlePDFCommand(arg) }),
		local("graph", "Query the code graph for a symbol", "<symbol>", func(cm *ChatModel, arg string) string { return cm.handleGraphCommand(arg) }),
		local("script", "Generate an analysis script for a task", "<task>", func(cm *ChatModel, arg string) string { return cm.handleScriptCommand(arg) }),
		local("repeat", "Set repetition penalty (0.5–3.0)", "<value>", func(cm *ChatModel, arg string) string {
			v, err := strconv.ParseFloat(arg, 64)
			if err != nil || v < 0.5 || v > 3.0 {
				return "Usage: /repeat <value> (e.g. 1.0, 1.1, 1.2 — typical range 1.0 - 2.0)"
			}
			cm.repeatPenalty = v
			cm.chat.SetRepeatPenalty(v)
			_ = UpdateConfig(func(cfg *OnboardResult) { cfg.RepeatPenalty = v })
			return fmt.Sprintf("✅ Repetition penalty set to %.2f", v)
		}),
	}
	return cmds
}
