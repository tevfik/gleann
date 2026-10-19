package tui

import (
	"context"
	"errors"
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

	// Last rendered header/info values, served while mu is held elsewhere.
	lastHeader string
	lastRows   [][2]string
}

// RunAgentChat runs the chat on the agenttui engine (streaming with
// cancellation, collapsible reasoning, source cards, slash-command popup).
func RunAgentChat(chat *gleann.LeannChat, indexName, modelName string) error {
	ac := newAgentChat(chat, indexName, modelName)
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

// newAgentChat builds the app and restores the transcript, without running it.
func newAgentChat(chat *gleann.LeannChat, indexName, modelName string) *agentChat {
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
		HideMode:     true,
		Banner:       gleannBanner,
		QuickActions: gleannQuickActions,
		HeaderInfo:   ac.headerInfo,
		InfoRows:     ac.infoRows,
		FooterHint:   "Enter send · / commands · Ctrl+S settings · Ctrl+T thinking · Ctrl+Y copy · Ctrl+C quit",
		OnKey: func(key string, a *agenttui.App) bool {
			if key == "ctrl+s" {
				a.Submit("/settings")
				return true
			}
			return false
		},
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
	go ac.app.ScanRepoFiles(mustGetwd())
	return ac
}

var gleannBanner = []string{
	"   ██████╗ ██╗     ███████╗ █████╗ ███╗   ██╗███╗   ██╗",
	"  ██╔════╝ ██║     ██╔════╝██╔══██╗████╗  ██║████╗  ██║",
	"  ██║  ███╗██║     █████╗  ███████║██╔██╗ ██║██╔██╗ ██║",
	"  ██║   ██║██║     ██╔══╝  ██╔══██║██║╚██╗██║██║╚██╗██║",
	"  ╚██████╔╝███████╗███████╗██║  ██║██║ ╚████║██║ ╚████║",
	"   ╚═════╝ ╚══════╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═══╝╚═╝  ╚═══╝",
}

var gleannQuickActions = []agenttui.QuickAction{
	{Cmd: "/settings", Desc: "Model, temperature, top-k, reranker, role, system prompt (Ctrl+S)"},
	{Cmd: "/history", Desc: "Resume a saved conversation"},
	{Cmd: "/attach", Desc: "Add a file or directory to the session context"},
	{Cmd: "/image", Desc: "Queue an image for your next message"},
	{Cmd: "/screenshot", Desc: "Grab clipboard image / screen region for your next message"},
	{Cmd: "/remember", Desc: "Store a memory the assistant keeps across sessions"},
	{Cmd: "/index", Desc: "Show index info or switch index"},
	{Cmd: "/help", Desc: "All commands"},
}

// headerInfo is the compact status shown in the header, e.g. "docs · t=0.7 · k=10".
func (ac *agentChat) headerInfo() string {
	// View must never wait on a slow command that holds the state lock.
	if !ac.mu.TryLock() {
		return ac.lastHeader
	}
	defer ac.mu.Unlock()
	cm := ac.cm
	parts := []string{cm.indexName, fmt.Sprintf("t=%.1f", cm.temperature)}
	if cm.indexName != "(no index)" {
		parts = append(parts, fmt.Sprintf("k=%d", cm.topK))
		if cm.rerankEnabled {
			parts = append(parts, "rerank")
		}
	}
	if n := len(cm.pendingImages); n > 0 {
		parts = append(parts, fmt.Sprintf("📷×%d", n))
	}
	if n := len(cm.attachedFiles); n > 0 {
		parts = append(parts, fmt.Sprintf("📎×%d", n))
	}
	ac.lastHeader = strings.Join(parts, " · ")
	return ac.lastHeader
}

// infoRows is the sidebar's info block: the same values the Ctrl+S settings
// menu edits, plus the index/mode and what is queued for the next message.
func (ac *agentChat) infoRows() [][2]string {
	if !ac.mu.TryLock() {
		return ac.lastRows
	}
	defer ac.mu.Unlock()
	cm := ac.cm

	mode := "RAG · " + cm.indexName
	if cm.indexName == "(no index)" {
		mode = "LLM only (no retrieval)"
	}
	rerank := "off"
	if cm.rerankEnabled {
		rerank = "on"
		if len(cm.rerankModels) > 0 && cm.rerankModelIdx < len(cm.rerankModels) {
			rerank = cm.rerankModels[cm.rerankModelIdx]
		}
	}
	role := "(none)"
	if cm.roleIdx > 0 && cm.roleIdx < len(cm.roleNames) {
		role = cm.roleNames[cm.roleIdx]
	}
	maxTok := strconv.Itoa(cm.maxTokens)
	if cm.maxTokens == 0 {
		maxTok = "unlimited"
	}
	rows := [][2]string{
		{"mode", mode},
		{"model", cm.modelName},
		{"temperature", fmt.Sprintf("%.1f", cm.temperature)},
		{"repeat penalty", fmt.Sprintf("%.2f", cm.repeatPenalty)},
		{"max tokens", maxTok},
		{"top-k", strconv.Itoa(cm.topK)},
		{"reranker", rerank},
		{"role", role},
	}
	if cm.embeddingModel != "" {
		rows = append(rows, [2]string{"embedding", cm.embeddingModel})
	}
	if q := len(cm.pendingImages); q > 0 || len(cm.attachedFiles) > 0 {
		rows = append(rows, [2]string{"queued", fmt.Sprintf("%d media · %d files", q, len(cm.attachedFiles))})
	}
	ac.lastRows = rows
	return rows
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// stream answers one prompt: retrieval + generation. The retrieved passages are
// reported first as a "sources" tool card, then reasoning as Thinking events and
// the answer as Text events.
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

	chat.SetResultsHandler(func(results []gleann.SearchResult) {
		if len(results) == 0 {
			return
		}
		emit(agenttui.Event{Kind: agenttui.EventToolStart, ToolID: "sources", ToolName: "sources",
			ToolArgs: fmt.Sprintf("%d passages", len(results))})
		emit(agenttui.Event{Kind: agenttui.EventToolEnd, ToolID: "sources", ToolOutput: formatSources(results), ToolOK: true})
	})
	defer chat.SetResultsHandler(nil)

	var answered int
	cb := func(tok string) {
		answered += len(tok)
		emit(agenttui.Event{Kind: agenttui.EventText, Text: tok})
	}
	opts := []gleann.SearchOption{gleann.WithTopK(topK), gleann.WithReranker(rerank)}

	var err error
	if len(images) > 0 {
		_, err = chat.AskStreamWithImages(ctx, question, images, cb, opts...)
	} else {
		_, err = chat.AskStream(ctx, question, cb, opts...)
	}

	ac.mu.Lock()
	cm.tokensReceived += answered / 4
	total := cm.tokensSent + cm.tokensReceived
	ac.mu.Unlock()
	ac.app.SetContextTokens(total, cm.contextLimit)

	return total, err
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
			Name: "history", Description: "Pick a saved conversation to resume", Usage: "",
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				return ac.resumeConversation(ctx, a)
			},
		},
		{
			Name: "clear", Description: "Clear the screen and the conversation history",
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				ac.withState(func(cm *ChatModel) string { cm.chat.ClearHistory(); return "" })
				a.ClearMessages()
				return "Conversation cleared.", true, nil
			},
		},
		{
			Name: "settings", Description: "Edit model, sampling, reranker, role and system prompt",
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				return ac.editSettings(ctx, a)
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
		local("image", "Queue an image (png/jpg/gif/webp) for your next message, then ask about it", "<path>", func(cm *ChatModel, arg string) string { return cm.handleImageCommand(arg) }),
		local("screenshot", "Queue the clipboard image, or select a screen region (no arg); with a path it acts like /image", "[path]", func(cm *ChatModel, arg string) string { return cm.handleScreenshotCommand(arg) }),
		local("audio", "Queue audio (mp3/wav/flac/ogg/m4a/opus) for your next message — needs an audio-capable model", "<path>", func(cm *ChatModel, arg string) string { return cm.handleAudioCommand(arg) }),
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

// resumeConversation lets the user pick a saved conversation and loads it into
// both the LLM history and the transcript.
func (ac *agentChat) resumeConversation(ctx context.Context, a *agenttui.App) (string, bool, error) {
	store := conversations.DefaultStore()
	items, err := store.List()
	if err != nil || len(items) == 0 {
		return "No saved conversations found.", true, nil
	}
	labels := make([]string, len(items))
	for i, c := range items {
		labels[i] = fmt.Sprintf("%s · %s · %d msgs", truncStr(c.Title, 50), c.UpdatedAt.Format("2006-01-02 15:04"), len(c.Messages))
	}
	picked, err := a.Ask(ctx, agenttui.Question{Text: "Resume which conversation?", Options: labels})
	if errors.Is(err, agenttui.ErrAskCancelled) {
		return "Cancelled.", true, nil
	}
	if err != nil {
		return "", true, err
	}
	idx := -1
	for i, l := range labels {
		if l == picked {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", true, fmt.Errorf("selection %q not found", picked)
	}
	full, err := store.Load(items[idx].ID)
	if err != nil {
		return "", true, fmt.Errorf("load conversation: %w", err)
	}

	ac.withState(func(cm *ChatModel) string {
		cm.chat.ClearHistory()
		for _, m := range full.Messages {
			if m.Role == "system" && m.Content != "" {
				cm.systemPrompt = m.Content
				cm.chat.SetSystemPrompt(m.Content)
				continue
			}
			cm.chat.AppendHistory(gleann.ChatMessage{Role: m.Role, Content: m.Content})
		}
		cm.tokensSent, cm.tokensReceived = 0, 0
		return ""
	})
	a.ClearMessages()
	for _, m := range full.Messages {
		role := agenttui.RoleUser
		switch m.Role {
		case "assistant":
			role = agenttui.RoleAssistant
		case "system":
			continue
		}
		a.AddMessage(agenttui.Message{Role: role, Content: m.Content})
	}
	return fmt.Sprintf("Loaded conversation: %s (%d messages)", full.Title, len(full.Messages)), true, nil
}

// editSettings shows the settings form and applies the result through the
// classic panel's applySettings so persistence and side effects stay identical.
func (ac *agentChat) editSettings(ctx context.Context, a *agenttui.App) (string, bool, error) {
	ac.mu.Lock()
	cm := ac.cm
	floatOpts := func(vals []float64, f string) []string {
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = fmt.Sprintf(f, v)
		}
		return out
	}
	intOpts := func(vals []int) []string {
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = strconv.Itoa(v)
		}
		return out
	}
	// closest keeps the current value selectable even when it is not a preset.
	withCurrent := func(opts []string, cur string) []string {
		for _, o := range opts {
			if o == cur {
				return opts
			}
		}
		return append([]string{cur}, opts...)
	}
	model := cm.llmModels[cm.llmModelIdx]
	temp := fmt.Sprintf("%.1f", cm.temperature)
	rep := fmt.Sprintf("%.2f", cm.repeatPenalty)
	maxTok := strconv.Itoa(cm.maxTokens)
	topK := strconv.Itoa(cm.topK)
	rerank := "off"
	if cm.rerankEnabled {
		rerank = "on"
	}
	role := "(none)"
	if cm.roleIdx >= 0 && cm.roleIdx < len(cm.roleNames) {
		role = cm.roleNames[cm.roleIdx]
	}
	fields := []agenttui.Field{
		{Label: "Model", Kind: agenttui.FieldChoice, Value: model, Options: withCurrent(cm.llmModels, model)},
		{Label: "Temperature", Kind: agenttui.FieldChoice, Value: temp, Options: withCurrent(floatOpts(temperaturePresets, "%.1f"), temp)},
		{Label: "Repeat penalty", Kind: agenttui.FieldChoice, Value: rep, Options: withCurrent(floatOpts(repeatPenaltyPresets, "%.2f"), rep)},
		{Label: "Max tokens", Kind: agenttui.FieldChoice, Value: maxTok, Options: withCurrent(intOpts(maxTokensPresets), maxTok)},
		{Label: "Top-K", Kind: agenttui.FieldChoice, Value: topK, Options: withCurrent(intOpts(topKPresets), topK)},
		{Label: "Reranker", Kind: agenttui.FieldBool, Value: rerank},
	}
	rerankIdx := -1
	if len(cm.rerankModels) > 0 {
		rerankIdx = len(fields)
		fields = append(fields, agenttui.Field{Label: "Rerank model", Kind: agenttui.FieldChoice,
			Value: cm.rerankModels[cm.rerankModelIdx], Options: cm.rerankModels})
	}
	roleIdx := len(fields)
	fields = append(fields, agenttui.Field{Label: "Role", Kind: agenttui.FieldChoice, Value: role, Options: cm.roleNames})
	promptIdx := len(fields)
	fields = append(fields, agenttui.Field{Label: "System prompt", Kind: agenttui.FieldText, Value: cm.systemPrompt})
	ac.mu.Unlock()

	vals, err := a.Form(ctx, agenttui.Form{Title: "Chat settings", Fields: fields})
	if errors.Is(err, agenttui.ErrAskCancelled) {
		return "Settings unchanged.", true, nil
	}
	if err != nil {
		return "", true, err
	}

	return ac.withState(func(cm *ChatModel) string {
		cm.llmModelIdx = indexOf(cm.llmModels, vals[0], cm.llmModelIdx)
		if v, e := strconv.ParseFloat(vals[1], 64); e == nil {
			cm.temperature = v
		}
		if v, e := strconv.ParseFloat(vals[2], 64); e == nil {
			cm.repeatPenalty = v
		}
		if v, e := strconv.Atoi(vals[3]); e == nil {
			cm.maxTokens = v
		}
		if v, e := strconv.Atoi(vals[4]); e == nil {
			cm.topK = v
		}
		cm.rerankEnabled = vals[5] == "on"
		if rerankIdx >= 0 {
			cm.rerankModelIdx = indexOf(cm.rerankModels, vals[rerankIdx], cm.rerankModelIdx)
		}
		newRole := indexOf(cm.roleNames, vals[roleIdx], cm.roleIdx)
		promptChanged := vals[promptIdx] != cm.systemPrompt
		cm.systemPrompt = vals[promptIdx]
		if newRole != cm.roleIdx || !promptChanged {
			cm.roleIdx = newRole // a changed role resolves to its prompt in applySettings
		} else {
			cm.roleIdx = 0 // an edited prompt wins over the previously selected role
		}
		before := len(cm.messages)
		cm.applySettings()
		a.SetModelName(cm.modelName)
		if len(cm.messages) > before {
			return cm.messages[len(cm.messages)-1].content
		}
		return "Settings updated."
	}), true, nil
}

func indexOf(list []string, v string, fallback int) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return fallback
}
