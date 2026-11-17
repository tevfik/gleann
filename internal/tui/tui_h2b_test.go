package tui

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tevfik/gleann/pkg/gleann"
)

// h2bKey builds the key press a user would type: a named key ("enter",
// "down", "esc", "ctrl+c", …) or a single printable character.
func h2bKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

// h2bPress feeds keys to a model the way the bubbletea loop would and
// returns the final model.
func h2bPress(m tea.Model, keys ...string) tea.Model {
	for _, k := range keys {
		m, _ = m.Update(h2bKey(k))
	}
	return m
}

// h2bScreen is what the fake program loop does with one screen: usually it
// types keys into it, sometimes it fails like a broken terminal would.
type h2bScreen func(m tea.Model) (tea.Model, error)

func h2bKeys(keys ...string) h2bScreen {
	return func(m tea.Model) (tea.Model, error) { return h2bPress(m, keys...), nil }
}

func h2bFail(msg string) h2bScreen {
	return func(m tea.Model) (tea.Model, error) { return m, errors.New(msg) }
}

// h2bScript replaces runProgram with a loop that plays the given screens in
// order and records the type of every screen shown.
func h2bScript(t *testing.T, screens ...h2bScreen) *[]string {
	t.Helper()
	var shown []string
	old := runProgram
	t.Cleanup(func() { runProgram = old })
	runProgram = func(m tea.Model) (tea.Model, error) {
		shown = append(shown, fmt.Sprintf("%T", m))
		if len(shown) > len(screens) {
			return m, fmt.Errorf("unexpected extra screen %T", m)
		}
		return screens[len(shown)-1](m)
	}
	return &shown
}

// h2bHome isolates HOME and the plugin registry (no network) for one test.
func h2bHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CHAT_UI", "")
	t.Setenv("GLEANN_CONFIG_DIR", "") // models live under the temporary HOME
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	oldURL := gleann.RegistryURL
	gleann.RegistryURL = srv.URL
	t.Cleanup(func() { gleann.RegistryURL = oldURL })
	return home
}

func h2bClassicChat(t *testing.T) {
	t.Helper()
	old := UseClassicUI
	UseClassicUI = true
	t.Cleanup(func() { UseClassicUI = old })
}

func h2bSetOnboard(f func(*OnboardModel)) h2bScreen {
	return func(m tea.Model) (tea.Model, error) {
		ob := m.(OnboardModel)
		f(&ob)
		return ob, nil
	}
}

func TestH2bRunVisitsEveryScreenUntilQuit(t *testing.T) {
	h2bHome(t)
	shown := h2bScript(t,
		h2bKeys("down", "down", "enter"),         // home → Indexes
		h2bKeys("q"),                             // index manager: back
		h2bKeys("down", "down", "down", "enter"), // home → Plugins
		h2bKeys("q"),                             // plugins: back
		h2bKeys("enter"),                         // home → Setup
		h2bSetOnboard(func(o *OnboardModel) { o.cancelled = true }),
		h2bKeys("down", "enter"), // home → Chat
		h2bKeys("esc"),           // index picker: back
		h2bKeys("q"),             // home: quit
	)
	if err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"tui.HomeModel", "tui.IndexManageModel",
		"tui.HomeModel", "tui.PluginModel",
		"tui.HomeModel", "tui.OnboardModel",
		"tui.HomeModel", "tui.IndexListModel",
		"tui.HomeModel",
	}
	if strings.Join(*shown, ",") != strings.Join(want, ",") {
		t.Errorf("screens = %v\nwant      %v", *shown, want)
	}
}

func TestH2bRunReportsScreenErrors(t *testing.T) {
	cases := []struct {
		name    string
		screens []h2bScreen
		want    string
	}{
		{"home", []h2bScreen{h2bFail("no tty")}, "home screen: no tty"},
		{"setup", []h2bScreen{h2bKeys("enter"), h2bFail("boom")}, "onboard: boom"},
		{"chat", []h2bScreen{h2bKeys("down", "enter"), h2bFail("boom")}, "index list: boom"},
		{"indexes", []h2bScreen{h2bKeys("down", "down", "enter"), h2bFail("boom")}, "index manager: boom"},
		{"plugins", []h2bScreen{h2bKeys("down", "down", "down", "enter"), h2bFail("boom")}, "plugins: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h2bHome(t)
			h2bScript(t, tc.screens...)
			err := Run()
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestH2bRunOnboardSavesACompletedSetup(t *testing.T) {
	home := h2bHome(t)
	h2bScript(t, h2bSetOnboard(func(o *OnboardModel) {
		o.result = OnboardResult{Completed: true, EmbeddingProvider: "ollama", EmbeddingModel: "bge-m3",
			LLMProvider: "ollama", LLMModel: "gemma3:4b", IndexDir: filepath.Join(home, "idx")}
	}))
	if err := runOnboard(); err != nil {
		t.Fatalf("runOnboard: %v", err)
	}
	cfg := LoadSavedConfig()
	if cfg == nil || !cfg.Completed || cfg.LLMModel != "gemma3:4b" || cfg.EmbeddingModel != "bge-m3" {
		t.Fatalf("saved config = %+v", cfg)
	}
}

func TestH2bRunOnboardStartsFromTheSavedConfig(t *testing.T) {
	h2bHome(t)
	if err := SaveConfig(OnboardResult{Completed: true, LLMProvider: "ollama", LLMModel: "saved-model"}); err != nil {
		t.Fatal(err)
	}
	var started OnboardModel
	h2bScript(t, func(m tea.Model) (tea.Model, error) {
		started = m.(OnboardModel)
		started.cancelled = true
		return started, nil
	})
	if err := runOnboard(); err != nil {
		t.Fatalf("runOnboard: %v", err)
	}
	if !started.menuMode || started.existingCfg == nil || started.existingCfg.LLMModel != "saved-model" {
		t.Error("a completed saved config must open the settings menu, not a fresh wizard")
	}
}

func TestH2bRunOnboardSaveFailureIsOnlyAWarning(t *testing.T) {
	home := h2bHome(t)
	// ~/.gleann is a file: the config directory cannot be created.
	if err := os.WriteFile(filepath.Join(home, ".gleann"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2bScript(t, h2bSetOnboard(func(o *OnboardModel) {
		o.result = OnboardResult{Completed: true, LLMProvider: "ollama", LLMModel: "m"}
	}))
	if err := runOnboard(); err != nil {
		t.Fatalf("a config that cannot be saved must not abort the TUI: %v", err)
	}
	if LoadSavedConfig() != nil {
		t.Error("nothing should have been saved")
	}
}

func TestH2bRunOnboardOpensSettingsThenReturns(t *testing.T) {
	h2bHome(t)
	shown := h2bScript(t,
		h2bSetOnboard(func(o *OnboardModel) { o.openSettings = true }),
		h2bKeys("esc"), // settings screen: back
		h2bSetOnboard(func(o *OnboardModel) { o.cancelled = true }),
	)
	if err := runOnboard(); err != nil {
		t.Fatalf("runOnboard: %v", err)
	}
	want := "tui.OnboardModel,tui.SettingsModel,tui.OnboardModel"
	if got := strings.Join(*shown, ","); got != want {
		t.Errorf("screens = %s, want %s", got, want)
	}

	h2bScript(t,
		h2bSetOnboard(func(o *OnboardModel) { o.openSettings = true }),
		h2bFail("tty gone"),
	)
	if err := runOnboard(); err == nil || err.Error() != "settings: tty gone" {
		t.Errorf("a settings screen failure must surface: %v", err)
	}
}

func TestH2bRunOnboardOpensPluginsAndSavesFirst(t *testing.T) {
	h2bHome(t)
	shown := h2bScript(t,
		h2bSetOnboard(func(o *OnboardModel) {
			o.openPlugins = true
			o.result = OnboardResult{Completed: true, LLMProvider: "ollama", LLMModel: "before-plugins"}
		}),
		h2bKeys("q"), // plugin screen: back
		h2bSetOnboard(func(o *OnboardModel) { o.cancelled = true }),
	)
	if err := runOnboard(); err != nil {
		t.Fatalf("runOnboard: %v", err)
	}
	if got := strings.Join(*shown, ","); got != "tui.OnboardModel,tui.PluginModel,tui.OnboardModel" {
		t.Errorf("screens = %s", got)
	}
	if cfg := LoadSavedConfig(); cfg == nil || cfg.LLMModel != "before-plugins" {
		t.Errorf("the completed settings must be saved before the plugin screen opens: %+v", cfg)
	}

	h2bScript(t,
		h2bSetOnboard(func(o *OnboardModel) { o.openPlugins = true }),
		h2bFail("tty gone"),
	)
	if err := runOnboard(); err == nil || err.Error() != "plugins: tty gone" {
		t.Errorf("a plugin screen failure must surface: %v", err)
	}
}

func TestH2bRunOnboardWithPlugins(t *testing.T) {
	h2bHome(t)

	h2bScript(t, h2bSetOnboard(func(o *OnboardModel) { o.cancelled = true }))
	r, plugins, err := RunOnboardWithPlugins()
	if r != nil || plugins || err != nil {
		t.Errorf("cancel: r=%v plugins=%v err=%v", r, plugins, err)
	}

	h2bScript(t, h2bSetOnboard(func(o *OnboardModel) {
		o.openPlugins = true
		o.result = OnboardResult{Completed: true, LLMModel: "x"}
	}))
	r, plugins, err = RunOnboardWithPlugins()
	if err != nil || !plugins || r == nil || r.LLMModel != "x" {
		t.Errorf("plugins: r=%+v plugins=%v err=%v", r, plugins, err)
	}

	if err := SaveConfig(OnboardResult{Completed: true, LLMModel: "saved"}); err != nil {
		t.Fatal(err)
	}
	h2bScript(t, h2bFail("boom"))
	if _, _, err := RunOnboardWithPlugins(); err == nil || err.Error() != "onboard: boom" {
		t.Errorf("error: %v", err)
	}
}

func TestH2bRunSettingsAndRunPlugins(t *testing.T) {
	h2bHome(t)
	shown := h2bScript(t, h2bKeys("esc"), h2bKeys("q"))
	if err := RunSettings(); err != nil {
		t.Fatalf("RunSettings: %v", err)
	}
	if err := RunPlugins(); err != nil {
		t.Fatalf("RunPlugins: %v", err)
	}
	if got := strings.Join(*shown, ","); got != "tui.SettingsModel,tui.PluginModel" {
		t.Errorf("screens = %s", got)
	}
	h2bScript(t, h2bFail("x"))
	if err := RunSettings(); err == nil || !strings.HasPrefix(err.Error(), "settings: ") {
		t.Errorf("RunSettings error = %v", err)
	}
}

// h2bChatScreen captures the classic chat screen the flow opens.
func h2bChatScreen(got *ChatModel) h2bScreen {
	return func(m tea.Model) (tea.Model, error) {
		*got = m.(ChatModel)
		return m, nil
	}
}

func TestH2bRunChatSavesTheSession(t *testing.T) {
	home := h2bHome(t)
	h2bClassicChat(t)
	var shown ChatModel
	h2bScript(t, h2bChatScreen(&shown))

	chat := gleann.NewChat(gleann.NullSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	chat.AppendHistory(gleann.ChatMessage{Role: "user", Content: "hello"})
	if err := RunChat(chat, "notes", "mock"); err != nil {
		t.Fatalf("RunChat: %v", err)
	}
	if shown.indexName != "notes" || shown.modelName != "mock" {
		t.Errorf("chat screen opened for %q/%q", shown.indexName, shown.modelName)
	}
	files, _ := filepath.Glob(filepath.Join(home, ".gleann", "chatsessions", "notes_*.json"))
	if len(files) != 1 {
		t.Fatalf("saved sessions = %v", files)
	}
	data, _ := os.ReadFile(files[0])
	if !strings.Contains(string(data), "hello") {
		t.Errorf("session file lacks the conversation: %s", data)
	}
}

func TestH2bRunChatSaveFailureKeepsTheProgramError(t *testing.T) {
	home := h2bHome(t)
	h2bClassicChat(t)
	// chatsessions cannot be created: ~/.gleann/chatsessions is a file.
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gleann", "chatsessions"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2bScript(t, h2bFail("terminal closed"))
	chat := gleann.NewChat(gleann.NullSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	chat.AppendHistory(gleann.ChatMessage{Role: "user", Content: "hello"})
	if err := RunChat(chat, "notes", "mock"); err == nil || err.Error() != "terminal closed" {
		t.Errorf("RunChat must return the program error even when saving fails: %v", err)
	}
}

// h2bPickNoIndex chooses "continue without index" in the index picker.
var h2bPickNoIndex = h2bKeys("enter")

func TestH2bChatFlowPureLLMUsesTheSavedProvider(t *testing.T) {
	cases := []struct {
		name      string
		saved     OnboardResult
		wantModel string
		check     func(t *testing.T, c gleann.ChatConfig)
	}{
		{
			name: "ollama",
			saved: OnboardResult{LLMProvider: "ollama", LLMModel: "qwen3:8b", OllamaHost: "http://ollama.test:1",
				OllamaAPIKey: "ok-key", RepeatPenalty: 1.3},
			wantModel: "qwen3:8b",
			check: func(t *testing.T, c gleann.ChatConfig) {
				if c.Provider != gleann.LLMOllama || c.BaseURL != "http://ollama.test:1" || c.APIKey != "ok-key" || c.RepeatPenalty != 1.3 {
					t.Errorf("chat config = %+v", c)
				}
			},
		},
		{
			name:      "openai key",
			saved:     OnboardResult{LLMProvider: "openai", LLMModel: "gpt-x", OpenAIKey: "sk-test"},
			wantModel: "gpt-x",
			check: func(t *testing.T, c gleann.ChatConfig) {
				if c.Provider != gleann.LLMOpenAI || c.APIKey != "sk-test" {
					t.Errorf("chat config = %+v", c)
				}
			},
		},
		{
			name:      "eif",
			saved:     OnboardResult{LLMProvider: "eif", LLMModel: "local.gguf"},
			wantModel: "local.gguf",
			check: func(t *testing.T, c gleann.ChatConfig) {
				if c.Provider != gleann.LLMEIF {
					t.Errorf("provider = %q", c.Provider)
				}
			},
		},
		{
			name:  "auto-scan host falls back to the default host",
			saved: OnboardResult{LLMProvider: "ollama", LLMModel: "m", OllamaHost: "(auto-scan localhost)"},
			check: func(t *testing.T, c gleann.ChatConfig) {
				if strings.Contains(c.BaseURL, "auto-scan") || c.BaseURL == "" {
					t.Errorf("base URL = %q", c.BaseURL)
				}
			},
			wantModel: "m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h2bHome(t)
			h2bClassicChat(t)
			if err := SaveConfig(tc.saved); err != nil {
				t.Fatal(err)
			}
			var chat ChatModel
			h2bScript(t, h2bPickNoIndex, h2bChatScreen(&chat))
			if err := RunChatFlow(); err != nil {
				t.Fatalf("RunChatFlow: %v", err)
			}
			if chat.indexName != "(no index)" || chat.modelName != tc.wantModel {
				t.Errorf("chat opened for %q/%q", chat.indexName, chat.modelName)
			}
			tc.check(t, chat.chat.Config())
		})
	}
}

func TestH2bChatFlowEmbeddedLlamaCppNeedsTheModelFile(t *testing.T) {
	cases := []struct {
		name  string
		saved OnboardResult
		pick  h2bScreen
		want  string
	}{
		{"chat model", OnboardResult{LLMProvider: "llamacpp", LLMModel: "missing-chat.gguf"}, h2bPickNoIndex,
			"failed to start embedded llama-server for chat"},
		{"embedding model", OnboardResult{EmbeddingProvider: "llamacpp", EmbeddingModel: "missing-embed.gguf"},
			h2bSetIndexPick("docs"), "failed to start embedded llama-server:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h2bHome(t)
			h2bClassicChat(t)
			if err := SaveConfig(tc.saved); err != nil {
				t.Fatal(err)
			}
			h2bScript(t, tc.pick)
			err := runChatFlow()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "not found") {
				t.Errorf("err = %v, want %q and a missing model file", err, tc.want)
			}
		})
	}
}

// h2bSetIndexPick selects the named index in the picker, as enter on it would.
func h2bSetIndexPick(name string) h2bScreen {
	return func(m tea.Model) (tea.Model, error) {
		il := m.(IndexListModel)
		il.selected = name
		return il, nil
	}
}

func TestH2bChatFlowMissingIndexIsAnError(t *testing.T) {
	home := h2bHome(t)
	h2bClassicChat(t)
	if err := SaveConfig(OnboardResult{EmbeddingProvider: "openai", OpenAIBaseURL: "http://embed.test:1",
		IndexDir: filepath.Join(home, "indexes")}); err != nil {
		t.Fatal(err)
	}
	h2bScript(t, h2bSetIndexPick("nope"))
	err := runChatFlow()
	if err == nil || !strings.Contains(err.Error(), `load index "nope"`) {
		t.Errorf("err = %v", err)
	}
}

func TestH2bChatFlowOpensTheSelectedIndex(t *testing.T) {
	home := h2bHome(t)
	h2bClassicChat(t)
	indexDir := filepath.Join(home, "indexes")
	b, err := gleann.NewBuilder(gleann.Config{IndexDir: indexDir, Backend: "hnsw"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BuildLexicalOnly("docs", []gleann.Item{
		{Text: "gleann indexes code", Metadata: map[string]any{"source": "a.md"}},
		{Text: "the TUI picks an index", Metadata: map[string]any{"source": "b.md"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(OnboardResult{EmbeddingProvider: "ollama", OllamaHost: "(auto-scan)",
		LLMProvider: "ollama", LLMModel: "chat-model", IndexDir: indexDir}); err != nil {
		t.Fatal(err)
	}
	var chat ChatModel
	shown := h2bScript(t,
		func(m tea.Model) (tea.Model, error) {
			il := m.(IndexListModel)
			if len(il.indexes) != 1 || il.indexes[0].Name != "docs" {
				return m, fmt.Errorf("picker lists %+v", il.indexes)
			}
			return h2bPress(il, "enter"), nil // the cursor starts on "docs"
		},
		h2bChatScreen(&chat),
	)
	t.Cleanup(func() { SearcherLoader = nil })
	if err := runChatFlow(); err != nil {
		t.Fatalf("runChatFlow: %v (screens %v)", err, *shown)
	}
	if chat.indexName != "docs" || chat.modelName != "chat-model" {
		t.Errorf("chat opened for %q/%q", chat.indexName, chat.modelName)
	}
	if SearcherLoader == nil {
		t.Error("the flow must install a SearcherLoader for /scope")
	}
}

// The chat flow sends OpenAI-compatible embeddings to openai_base_url; it
// used to be dropped here, so they went to the Ollama URL instead.
func TestH2bLoadConfigOverlaysSavedValues(t *testing.T) {
	h2bHome(t)
	if err := SaveConfig(OnboardResult{EmbeddingProvider: "openai", EmbeddingModel: "text-embedding-3-small",
		OllamaHost: "http://h:1", OpenAIKey: "sk-a", OllamaAPIKey: "ok-b", OpenAIBaseURL: "http://embed.test:1"}); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig()
	if cfg.EmbeddingProvider != "openai" || cfg.EmbeddingModel != "text-embedding-3-small" ||
		cfg.OllamaHost != "http://h:1" || cfg.OpenAIAPIKey != "sk-a" || cfg.OllamaAPIKey != "ok-b" ||
		cfg.OpenAIBaseURL != "http://embed.test:1" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.IndexDir != DefaultIndexDir() {
		t.Errorf("index dir = %q, want the default", cfg.IndexDir)
	}
}
