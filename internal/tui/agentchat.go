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
	"time"

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

	retr *retrievalState // sidebar retrieval panel data

	// Last rendered header/info values, served while mu is held elsewhere.
	lastHeader string
	lastWide   string
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
	ac := &agentChat{cm: &cm, retr: &retrievalState{}}

	ac.app = agenttui.New(agenttui.Config{
		Title:          "gleann",
		Subtitle:       "RAG chat · " + indexName,
		ModelName:      modelName,
		WorkDir:        mustGetwd(),
		Placeholder:    "Ask a question about your index… (/ for commands)",
		Theme:          agenttui.ThemeByName(os.Getenv("GLEANN_THEME")),
		IgnoreDirs:     []string{".gleann"},
		ShowHeader:     true,
		ShowFooter:     true,
		EnableMouse:    true,
		HideMode:       true,
		Banner:         gleannBanner,
		QuickActions:   gleannQuickActions,
		HeaderInfo:     ac.headerInfo,
		HeaderInfoWide: ac.headerInfoWide,
		InfoRows:       ac.infoRows,
		Sidebar:        retrievalPanel{st: ac.retr},
		SidebarFirst:   true,
		FooterHint:     "Enter send · / commands · Ctrl+S settings · Ctrl+T thinking · Ctrl+Y copy · Ctrl+C quit",
		OnKey: func(key string, a *agenttui.App) bool {
			if key == "ctrl+s" {
				a.Submit("/settings")
				return true
			}
			return false
		},
		StreamEvents:    ac.stream,
		Commands:        ac.commands(),
		MentionComplete: ac.mentionComplete,
		MentionResolve:  ac.mentionResolve,
	})
	ac.app.SetContextTokens(0, cm.contextLimit)
	ac.refreshRetrieval()

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
	{Cmd: "/index", Desc: "Pick which indexes and #tags to search"},
	{Cmd: "/help", Desc: "All commands"},
}

// indexLabel names the searched indexes, e.g. "docs" or "docs+code".
func indexLabel(cm *ChatModel) string {
	if len(cm.activeIndexes) == 0 {
		return cm.indexName
	}
	return cm.indexName + "+" + strings.Join(cm.activeIndexes, "+")
}

// headerInfo is the compact status shown in the header, e.g. "docs · t=0.7 · k=10".
func (ac *agentChat) headerInfo() string {
	// View must never wait on a slow command that holds the state lock.
	if !ac.mu.TryLock() {
		return ac.lastHeader
	}
	defer ac.mu.Unlock()
	cm := ac.cm
	parts := []string{indexLabel(cm), fmt.Sprintf("t=%.1f", cm.temperature)}
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

// headerInfoWide carries the sidebar's details in the header when the sidebar
// is hidden (Ctrl+B) or the terminal is too narrow for it.
func (ac *agentChat) headerInfoWide() string {
	if !ac.mu.TryLock() {
		return ac.lastWide
	}
	defer ac.mu.Unlock()
	cm := ac.cm
	mode := "RAG"
	if cm.indexName == "(no index)" {
		mode = "LLM only"
	}
	parts := []string{mode}
	if cm.indexName != "(no index)" {
		parts = append(parts, indexLabel(cm))
	}
	maxTok := strconv.Itoa(cm.maxTokens)
	if cm.maxTokens == 0 {
		maxTok = "∞"
	}
	parts = append(parts, fmt.Sprintf("t=%.1f", cm.temperature), fmt.Sprintf("rep=%.2f", cm.repeatPenalty), "max="+maxTok)
	if cm.indexName != "(no index)" {
		parts = append(parts, fmt.Sprintf("k=%d", cm.topK))
		if cm.rerankEnabled {
			parts = append(parts, "rerank")
		}
	}
	if cm.roleIdx > 0 && cm.roleIdx < len(cm.roleNames) {
		parts = append(parts, "role:"+cm.roleNames[cm.roleIdx])
	}
	if n := len(cm.pendingImages); n > 0 {
		parts = append(parts, fmt.Sprintf("📷×%d", n))
	}
	if n := len(cm.attachedFiles); n > 0 {
		parts = append(parts, fmt.Sprintf("📎×%d", n))
	}
	ac.lastWide = strings.Join(parts, " · ")
	return ac.lastWide
}

// infoRows is the sidebar's info block: the same values the Ctrl+S settings
// menu edits, plus the index/mode and what is queued for the next message.
func (ac *agentChat) infoRows() [][2]string {
	if !ac.mu.TryLock() {
		return ac.lastRows
	}
	defer ac.mu.Unlock()
	cm := ac.cm

	mode := "RAG · " + indexLabel(cm)
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
		rows = append(rows, [2]string{"embedding", filepath.Base(cm.embeddingModel)})
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
		ac.retr.setLast(results)
		emit(agenttui.Event{Kind: agenttui.EventSources, Sources: toSources(results)})
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
	if cm.contextLimit == 0 {
		// The model is loaded now, so the server can report the window it actually uses.
		go ac.refreshContextLimit()
	}

	return total, err
}

// refreshContextLimit re-reads the model's real context window from the provider.
func (ac *agentChat) refreshContextLimit() {
	ac.mu.Lock()
	cfg := ac.cm.chat.Config()
	ac.mu.Unlock()
	n := lookupContextLimit(cfg)
	ac.mu.Lock()
	ac.cm.contextLimit = n
	total := ac.cm.tokensSent + ac.cm.tokensReceived
	ac.mu.Unlock()
	ac.app.SetContextTokens(total, n)
}

// sourceLabel picks the most descriptive metadata field of a passage.
func sourceLabel(r gleann.SearchResult) string {
	for _, k := range []string{"source", "path", "file", "title"} {
		if v, ok := r.Metadata[k].(string); ok && v != "" {
			return v
		}
	}
	return truncStr(strings.Join(strings.Fields(r.Text), " "), 60)
}

// toSources converts retrieved passages into agenttui citations.
func toSources(results []gleann.SearchResult) []agenttui.Source {
	out := make([]agenttui.Source, len(results))
	for i, r := range results {
		out[i] = agenttui.Source{Title: sourceLabel(r), Score: float64(r.Score)}
		if n, ok := r.Metadata["_index"].(string); ok {
			out[i].Location = n // set by MultiSearcher: which index the passage came from
		}
	}
	return out
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
		{
			Name: "forget", Description: "Forget memories matching a query or ID (asks first)", Usage: "<query|id>",
			Handler: func(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
				arg := strings.TrimSpace(strings.Join(args, " "))
				if msg := ac.withState(func(cm *ChatModel) string {
					switch {
					case cm.memoryMgr == nil:
						return "⚠ Memory system unavailable"
					case arg == "":
						return "Usage: /forget <query or ID>"
					}
					return ""
				}); msg != "" {
					return msg, true, nil
				}
				if !confirm(ctx, a, fmt.Sprintf("Permanently forget every memory matching %q?", arg)) {
					return "Nothing forgotten.", true, nil
				}
				return ac.withState(func(cm *ChatModel) string {
					n, err := cm.memoryMgr.Forget(arg)
					if err != nil {
						return "⚠ " + err.Error()
					}
					return fmt.Sprintf("🗑 Forgot %d memory block(s) matching %q", n, arg)
				}), true, nil
			},
		},
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
		{
			Name: "index", Description: "Pick the indexes (and #tags) the chat retrieves from", Usage: "[name|#tag|a,b|add|remove|info]",
			Handler: ac.indexCommand,
		},
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
		{Label: "Model", Kind: agenttui.FieldChoice, Value: model, Options: withCurrent(cm.llmModels, model),
			Hint: fmt.Sprintf("LLM that writes the answers (%d available)", len(cm.llmModels))},
		{Label: "Temperature", Kind: agenttui.FieldSlider, Value: temp, Options: sliderOptions(floatOpts(temperaturePresets, "%.1f"), temp),
			Hint: "0 = focused and repeatable, higher = more creative"},
		{Label: "Repeat penalty", Kind: agenttui.FieldSlider, Value: rep, Options: sliderOptions(floatOpts(repeatPenaltyPresets, "%.2f"), rep),
			Hint: "1.0 = off, higher discourages repeating itself"},
		{Label: "Max tokens", Kind: agenttui.FieldSlider, Value: maxTok, Options: sliderOptions(intOpts(maxTokensPresets), maxTok),
			Hint: "upper limit on the length of one answer (0 = unlimited)"},
		{Label: "Top-K", Kind: agenttui.FieldSlider, Value: topK, Options: sliderOptions(intOpts(topKPresets), topK),
			Hint: "passages retrieved from the index for each question"},
		{Label: "Reranker", Kind: agenttui.FieldBool, Value: rerank,
			Hint: "re-score retrieved passages with a reranker model (slower, more precise)"},
	}
	rerankIdx := -1
	if len(cm.rerankModels) > 0 {
		rerankIdx = len(fields)
		fields = append(fields, agenttui.Field{Label: "Rerank model", Kind: agenttui.FieldChoice,
			Value: cm.rerankModels[cm.rerankModelIdx], Options: cm.rerankModels, Hint: "model used when the reranker is on"})
	}
	roleIdx := len(fields)
	fields = append(fields, agenttui.Field{Label: "Role", Kind: agenttui.FieldChoice, Value: role, Options: cm.roleNames, Hint: "persona preset; replaces the system prompt"})
	promptIdx := len(fields)
	fields = append(fields, agenttui.Field{Label: "System prompt", Kind: agenttui.FieldText, Value: cm.systemPrompt, Hint: "Enter to edit"})
	emb := cm.embeddingModel
	if emb == "" {
		emb = "(default)"
	}
	note := fmt.Sprintf("Index: %s · Embedding: %s (changing it requires an index rebuild)", cm.indexName, emb)
	ac.mu.Unlock()

	vals, err := a.Form(ctx, agenttui.Form{Title: "Chat settings", Fields: fields, Note: note})
	if errors.Is(err, agenttui.ErrAskCancelled) {
		return "Settings unchanged.", true, nil
	}
	if err != nil {
		return "", true, err
	}

	defer ac.refreshContextLimit() // model or max tokens may have changed the window
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

// sliderOptions returns the ordered presets with the current value inserted at
// its numeric position when it is not itself a preset, so the slider keeps a
// sensible order and the current value stays selectable.
func sliderOptions(presets []string, cur string) []string {
	for _, p := range presets {
		if p == cur {
			return presets
		}
	}
	v, err := strconv.ParseFloat(cur, 64)
	if err != nil {
		return presets
	}
	out := make([]string, 0, len(presets)+1)
	inserted := false
	for _, p := range presets {
		pv, perr := strconv.ParseFloat(p, 64)
		if !inserted && perr == nil && pv > v {
			out = append(out, cur)
			inserted = true
		}
		out = append(out, p)
	}
	if !inserted {
		out = append(out, cur)
	}
	return out
}

// withCurrent keeps the current value selectable in an unordered choice list.
func withCurrent(opts []string, cur string) []string {
	for _, o := range opts {
		if o == cur {
			return opts
		}
	}
	return append([]string{cur}, opts...)
}

// mentionTimeout bounds graph lookups made on the UI goroutine.
const mentionTimeout = 300 * time.Millisecond

// graphSearcher returns a searcher with a code graph (for a multi-index
// search, the first index that has one); symbol mentions are only offered then
// (the vector fallback is too slow per keystroke).
func (ac *agentChat) graphSearcher() *gleann.LeannSearcher {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	switch s := ac.cm.chat.GetSearcher().(type) {
	case *gleann.LeannSearcher:
		if s != nil && s.GraphDB() != nil {
			return s
		}
	case *gleann.MultiSearcher:
		for _, n := range s.Names() {
			if ls := s.Searcher(n); ls != nil && ls.GraphDB() != nil {
				return ls
			}
		}
	}
	return nil
}

// mentionComplete suggests code symbols for the @-popup.
func (ac *agentChat) mentionComplete(query string) []string {
	ls := ac.graphSearcher()
	if ls == nil || len(query) < 2 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), mentionTimeout)
	defer cancel()
	defs, err := gleann.FindDefinitions(ctx, ls, query, gleann.DefinitionOptions{TopK: 5})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		name := d.FQN
		if name == "" {
			name = d.Name
		}
		if name != "" && !strings.ContainsAny(name, " \t\n") {
			out = append(out, name)
		}
	}
	return out
}

// mentionResolve attaches the definition of an @symbol to the prompt.
func (ac *agentChat) mentionResolve(name string) (string, bool) {
	ls := ac.graphSearcher()
	if ls == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), mentionTimeout*3)
	defer cancel()
	defs, err := gleann.FindDefinitions(ctx, ls, name, gleann.DefinitionOptions{TopK: 1})
	if err != nil || len(defs) == 0 {
		return "", false
	}
	return gleann.FormatDefinitions(defs), true
}
