package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

func agentCmd(t *testing.T, ac *agentChat, name string) agenttui.SlashCommand {
	t.Helper()
	for _, c := range ac.commands() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no /%s command", name)
	return agenttui.SlashCommand{}
}

func runAgentCmd(t *testing.T, ac *agentChat, name string, args ...string) string {
	t.Helper()
	out, local, err := agentCmd(t, ac, name).Handler(context.Background(), args, ac.app)
	if err != nil || !local {
		t.Fatalf("/%s: local=%v err=%v", name, local, err)
	}
	return out
}

// withMemory gives the chat a real memory manager backed by a temp store.
func withMemory(t *testing.T, ac *agentChat) *memory.Manager {
	t.Helper()
	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := memory.NewManager(store)
	t.Cleanup(func() { mgr.Close() })
	ac.cm.memoryMgr = mgr
	return mgr
}

func TestAgentChat_MemoryCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ac := newTestAgentChat(nil)
	ac.cm.memoryMgr = nil
	for _, name := range []string{"remember", "memories"} {
		if out := runAgentCmd(t, ac, name, "x"); !strings.Contains(out, "unavailable") {
			t.Errorf("/%s without a manager: %q", name, out)
		}
	}
	if out := runAgentCmd(t, ac, "forget", "x"); !strings.Contains(out, "unavailable") {
		t.Errorf("/forget without a manager: %q", out)
	}

	withMemory(t, ac)
	if out := runAgentCmd(t, ac, "memories"); !strings.Contains(out, "No memories") {
		t.Errorf("empty store: %q", out)
	}
	if out := runAgentCmd(t, ac, "remember"); !strings.Contains(out, "Usage") {
		t.Errorf("remember needs text: %q", out)
	}
	if out := runAgentCmd(t, ac, "forget"); !strings.Contains(out, "Usage") {
		t.Errorf("forget needs a query: %q", out)
	}
	if out := runAgentCmd(t, ac, "remember", "the", "sky", "is", "blue"); !strings.Contains(out, "Remembered") || !strings.Contains(out, "the sky is blue") {
		t.Errorf("remember: %q", out)
	}
	out := runAgentCmd(t, ac, "memories")
	if !strings.Contains(out, "Memories (1)") || !strings.Contains(out, "the sky is blue") {
		t.Errorf("memories must list what was stored: %q", out)
	}
	// The confirmation cannot be answered without a running app: nothing may be deleted.
	if out := runAgentCmd(t, ac, "forget", "sky"); !strings.Contains(out, "Nothing forgotten") {
		t.Errorf("forget without confirmation: %q", out)
	}
	if out := runAgentCmd(t, ac, "memories"); !strings.Contains(out, "Memories (1)") {
		t.Errorf("memory must survive an unconfirmed /forget: %q", out)
	}
}

func TestAgentChat_RepeatCommandPersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ac := newTestAgentChat(nil)
	if out := runAgentCmd(t, ac, "repeat", "1.3"); !strings.Contains(out, "1.30") {
		t.Fatalf("repeat: %q", out)
	}
	if ac.cm.repeatPenalty != 1.3 {
		t.Errorf("model not updated: %v", ac.cm.repeatPenalty)
	}
	if cfg := LoadSavedConfig(); cfg == nil || cfg.RepeatPenalty != 1.3 {
		t.Errorf("repeat penalty must be saved to the config: %+v", cfg)
	}
	for _, bad := range []string{"abc", "0.1", ""} {
		if out := runAgentCmd(t, ac, "repeat", bad); !strings.Contains(out, "Usage") {
			t.Errorf("/repeat %q: %q", bad, out)
		}
	}
}

func TestAgentChat_ClearAndNew(t *testing.T) {
	ac := newTestAgentChat(nil)
	ac.cm.chat.AppendHistory(gleann.ChatMessage{Role: "user", Content: "hi"})
	ac.cm.pendingImages = []string{"a.png"}
	ac.cm.tokensReceived = 5
	if out := runAgentCmd(t, ac, "clear"); !strings.Contains(out, "cleared") || len(ac.cm.chat.History()) != 0 {
		t.Errorf("clear: %q history=%v", out, ac.cm.chat.History())
	}
	ac.cm.chat.AppendHistory(gleann.ChatMessage{Role: "user", Content: "hi"})
	runAgentCmd(t, ac, "new")
	if len(ac.cm.chat.History()) != 0 || ac.cm.pendingImages != nil || ac.cm.tokensReceived != 0 {
		t.Errorf("/new must reset history, media and budget: %+v", ac.cm)
	}
}

func TestAgentChat_AttachDetachAndMediaCommandsReportErrors(t *testing.T) {
	ac := newTestAgentChat(nil)
	dir := t.TempDir()
	f := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(f, []byte("hello attached"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runAgentCmd(t, ac, "attach", f); strings.Contains(out, "rror") && !strings.Contains(out, "note.txt") {
		t.Errorf("attach: %q", out)
	}
	if len(ac.cm.attachedFiles) != 1 {
		t.Fatalf("file not attached: %v", ac.cm.attachedFiles)
	}
	runAgentCmd(t, ac, "detach", f)
	if len(ac.cm.attachedFiles) != 0 {
		t.Errorf("file not detached: %v", ac.cm.attachedFiles)
	}
	missing := filepath.Join(dir, "missing.png")
	for _, name := range []string{"image", "audio", "video", "pdf"} {
		out := runAgentCmd(t, ac, name, missing)
		if out == "" {
			t.Errorf("/%s of a missing file must explain itself", name)
		}
	}
	if len(ac.cm.pendingImages) != 0 {
		t.Errorf("a missing image must not be queued: %v", ac.cm.pendingImages)
	}
}

func TestAgentChat_ResumeConversation(t *testing.T) {
	convDir := t.TempDir()
	t.Setenv("GLEANN_CONVERSATIONS_DIR", convDir)
	ac := newTestAgentChat(nil)
	if out := runAgentCmd(t, ac, "history"); !strings.Contains(out, "No saved conversations") {
		t.Errorf("empty store: %q", out)
	}
	err := conversations.NewStore(convDir).Save(&conversations.Conversation{
		Title:    "first chat",
		Messages: []conversations.Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// With a conversation to choose from the picker needs a running app; a bare
	// app reports that instead of hanging or silently succeeding.
	if _, _, err := agentCmd(t, ac, "history").Handler(context.Background(), nil, ac.app); err == nil {
		t.Error("picking without a running app must return an error")
	}
}

func TestAgentChat_EditSettingsNeedsRunningApp(t *testing.T) {
	ac := newTestAgentChat(nil)
	ac.cm.rerankModels = []string{"r1", "r2"}
	// The form is built (including the rerank field) and handed to the app; a
	// bare app cannot show it, and the error must surface rather than apply defaults.
	before := ac.cm.temperature
	if _, _, err := agentCmd(t, ac, "settings").Handler(context.Background(), nil, ac.app); err == nil {
		t.Error("expected an error from a non-running app")
	}
	if ac.cm.temperature != before {
		t.Error("settings must be untouched when the form fails")
	}
}

func TestNewAgentChat_RestoresHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chat := gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	chat.AppendHistory(gleann.ChatMessage{Role: "system", Content: "sys"})
	chat.AppendHistory(gleann.ChatMessage{Role: "user", Content: "question"})
	chat.AppendHistory(gleann.ChatMessage{Role: "assistant", Content: "answer"})
	ac := newAgentChat(chat, "idx", "mock")
	if ac.app == nil || ac.retr == nil || ac.cm == nil {
		t.Fatal("agent chat not fully built")
	}
	if got := ac.headerInfo(); !strings.Contains(got, "idx") {
		t.Errorf("header: %q", got)
	}
}

func TestWithCurrent(t *testing.T) {
	opts := []string{"a", "b"}
	if got := withCurrent(opts, "b"); len(got) != 2 {
		t.Errorf("present value must not be duplicated: %v", got)
	}
	if got := withCurrent(opts, "z"); len(got) != 3 || got[0] != "z" {
		t.Errorf("missing value must be prepended: %v", got)
	}
}

// writeScopeIndex creates a minimal index directory that gleann.ListIndexes lists.
func writeScopeIndex(t *testing.T, dir, name string, tags []string, passages int) {
	t.Helper()
	meta := gleann.IndexMeta{Name: name, NumPassages: passages, Tags: tags, Description: name + " desc"}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, name+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentChat_IndexCommandSubcommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeScopeIndex(t, dir, "api", []string{"work"}, 1200)
	writeScopeIndex(t, dir, "docs", []string{"work"}, 340)
	writeScopeIndex(t, dir, "notes", nil, 5)
	oldDir, oldLoader := searchIndexDir, SearcherLoader
	searchIndexDir = dir
	var opened [][]string
	SearcherLoader = func(_ context.Context, names []string) (gleann.Searcher, error) {
		opened = append(opened, append([]string(nil), names...))
		return fakeSearcher{}, nil
	}
	t.Cleanup(func() { searchIndexDir, SearcherLoader = oldDir, oldLoader })

	infos := listIndexInfo()
	if len(infos) != 3 || infos[0].Name != "api" || infos[2].Name != "notes" || infos[0].Passages != 1200 {
		t.Fatalf("listIndexInfo must be sorted and carry metadata: %+v", infos)
	}

	ac := newTestAgentChat(nil)
	ac.cm.indexName = "api"
	idx := func(args ...string) string {
		out, local, err := ac.indexCommand(context.Background(), args, ac.app)
		if err != nil || !local {
			t.Fatalf("/index %v: local=%v err=%v", args, local, err)
		}
		return out
	}

	info := idx("info")
	if !strings.Contains(info, "● api") || !strings.Contains(info, "○ docs") || !strings.Contains(info, "#work") {
		t.Errorf("info must mark the active index:\n%s", info)
	}
	if out := idx("add", "docs"); !strings.Contains(out, "api, docs") {
		t.Errorf("add: %q", out)
	}
	if got := ac.currentScope(); strings.Join(got, ",") != "api,docs" {
		t.Errorf("scope after add: %v", got)
	}
	if out := idx("remove", "api"); !strings.Contains(out, "docs") {
		t.Errorf("remove: %q", out)
	}
	if got := ac.currentScope(); strings.Join(got, ",") != "docs" {
		t.Errorf("scope after remove: %v", got)
	}
	if out := idx("remove", "docs"); !strings.Contains(out, "at least one") {
		t.Errorf("removing the last index must be refused: %q", out)
	}
	if out := idx("#work"); !strings.Contains(out, "api, docs") {
		t.Errorf("tag selection: %q", out)
	}
	if out := idx("add", "ghost"); !strings.Contains(out, "not found") {
		t.Errorf("add unknown: %q", out)
	}
	if out := idx("remove", "#nope"); !strings.Contains(out, "no index has tag") {
		t.Errorf("remove unknown tag: %q", out)
	}
	if out := idx("notes"); !strings.Contains(out, "notes") {
		t.Errorf("single select: %q", out)
	}
	if len(opened) != 4 {
		t.Errorf("a searcher must be opened once per real change (add, remove, #work, notes): %v", opened)
	}
	// The picker needs a running app.
	if _, _, err := ac.indexCommand(context.Background(), nil, ac.app); err == nil {
		t.Error("picker without a running app must fail")
	}
	// The sidebar reflects the new scope.
	ac.refreshRetrieval()
	if out := (retrievalPanel{ac.retr}).Render(40, 20); !strings.Contains(out, "● notes") {
		t.Errorf("sidebar not refreshed:\n%s", out)
	}
}

func TestAgentChat_EditScopeEmptyAndSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldDir := searchIndexDir
	searchIndexDir = t.TempDir()
	t.Cleanup(func() { searchIndexDir = oldDir })
	ac := newTestAgentChat(nil)
	out, local, err := ac.editScope(context.Background(), ac.app)
	if err != nil || !local || !strings.Contains(out, "No indexes found") {
		t.Errorf("empty dir: %q %v %v", out, local, err)
	}
	if got := ac.scopeSummary(); got != "" {
		t.Errorf("summary of nothing: %q", got)
	}

	writeScopeIndex(t, searchIndexDir, "api", []string{"work"}, 3)
	ac.cm.indexName = "api"
	if _, _, err := ac.editScope(context.Background(), ac.app); err == nil {
		t.Error("the form cannot be shown by a non-running app")
	}
	sum := ac.scopeSummary()
	if !strings.Contains(sum, "● api — 3 passages") || !strings.Contains(sum, "#work") {
		t.Errorf("summary: %q", sum)
	}
}

func TestNewSearcherLoader_SetsIndexDirAndReportsMissingIndex(t *testing.T) {
	old := searchIndexDir
	t.Cleanup(func() { searchIndexDir = old })
	dir := t.TempDir()
	load := NewSearcherLoader(gleann.Config{IndexDir: dir}, nil)
	if searchIndexDir != dir {
		t.Errorf("picker must list the loader's directory, got %q", searchIndexDir)
	}
	if _, err := load(context.Background(), []string{"missing"}); err == nil {
		t.Error("loading a missing index must fail")
	}
	if _, err := load(context.Background(), []string{"a", "b"}); err == nil {
		t.Error("loading missing indexes through the multi-searcher must fail")
	}
}

func TestConfirmWithoutRunningApp(t *testing.T) {
	app := agenttui.New(agenttui.Config{})
	if confirm(context.Background(), app, "sure?") {
		t.Error("an unanswerable question must default to No")
	}
}
