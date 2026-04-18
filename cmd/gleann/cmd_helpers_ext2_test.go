package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── formatSize ─────────────────────────────────────────────────

func TestFormatSizeBytes(t *testing.T) {
	if got := formatSize(42); got != "42 B" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSizeKB(t *testing.T) {
	if got := formatSize(2048); got != "2.0 KB" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSizeMB(t *testing.T) {
	if got := formatSize(5 * 1024 * 1024); got != "5.0 MB" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSizeGB(t *testing.T) {
	if got := formatSize(3 * 1024 * 1024 * 1024); got != "3.0 GB" {
		t.Errorf("got %q", got)
	}
}

// ── isCodeExtension ────────────────────────────────────────────

func TestIsCodeExtensionGo(t *testing.T) {
	if !isCodeExtension(".go") {
		t.Error("expected .go to be code")
	}
}

func TestIsCodeExtensionPy(t *testing.T) {
	if !isCodeExtension(".py") {
		t.Error("expected .py to be code")
	}
}

func TestIsCodeExtensionTxt(t *testing.T) {
	if isCodeExtension(".txt") {
		t.Error("expected .txt to NOT be code")
	}
}

func TestIsCodeExtensionUpperCase(t *testing.T) {
	if !isCodeExtension(".GO") {
		t.Error("expected .GO to be code (case-insensitive)")
	}
}

func TestIsCodeExtensionEmpty(t *testing.T) {
	if isCodeExtension("") {
		t.Error("expected empty to NOT be code")
	}
}

// ── strVal ─────────────────────────────────────────────────────

func TestStrValPresent(t *testing.T) {
	m := map[string]any{"key": "value"}
	if got := strVal(m, "key"); got != "value" {
		t.Errorf("got %q", got)
	}
}

func TestStrValMissing(t *testing.T) {
	m := map[string]any{"other": "value"}
	if got := strVal(m, "key"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestStrValNonString(t *testing.T) {
	m := map[string]any{"key": 42}
	if got := strVal(m, "key"); got != "" {
		t.Errorf("got %q", got)
	}
}

// ── getFlag ────────────────────────────────────────────────────

func TestGetFlagPresent(t *testing.T) {
	args := []string{"--host", "localhost:11434", "--model", "bge-m3"}
	if got := getFlag(args, "--model"); got != "bge-m3" {
		t.Errorf("got %q", got)
	}
}

func TestGetFlagMissing(t *testing.T) {
	args := []string{"--host", "localhost"}
	if got := getFlag(args, "--model"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestGetFlagNoValue(t *testing.T) {
	args := []string{"--model"}
	if got := getFlag(args, "--model"); got != "" {
		t.Errorf("got %q, want empty (no value after flag)", got)
	}
}

// ── hasFlag ────────────────────────────────────────────────────

func TestHasFlagPresent(t *testing.T) {
	args := []string{"--graph", "--docs", "/tmp"}
	if !hasFlag(args, "--graph") {
		t.Error("expected --graph to be present")
	}
}

func TestHasFlagMissing(t *testing.T) {
	args := []string{"--docs", "/tmp"}
	if hasFlag(args, "--graph") {
		t.Error("expected --graph to be missing")
	}
}

func TestHasFlagEmpty(t *testing.T) {
	if hasFlag(nil, "--anything") {
		t.Error("nil args should have no flags")
	}
}

// ── truncate ───────────────────────────────────────────────────

func TestTruncateExt2Short(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateExt2Long(t *testing.T) {
	s := strings.Repeat("a", 50)
	got := truncate(s, 20)
	if len(got) != 20 || !strings.HasSuffix(got, "...") {
		t.Errorf("got %q (len=%d)", got, len(got))
	}
}

func TestTruncateExt2Newlines(t *testing.T) {
	s := "line1\nline2\nline3"
	got := truncate(s, 100)
	if strings.Contains(got, "\n") {
		t.Error("newlines should be removed")
	}
}

// ── formatAge ──────────────────────────────────────────────────

func TestFormatAgeExt2Seconds(t *testing.T) {
	if got := formatAge(30 * time.Second); got != "30s" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgeExt2Minutes(t *testing.T) {
	if got := formatAge(5 * time.Minute); got != "5m" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgeExt2Hours(t *testing.T) {
	if got := formatAge(3 * time.Hour); got != "3h" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgeExt2Days(t *testing.T) {
	if got := formatAge(48 * time.Hour); got != "2d" {
		t.Errorf("got %q", got)
	}
}

// ── parseFriendlyDuration ──────────────────────────────────────

func TestParseFriendlyDurationExt2Days(t *testing.T) {
	d, err := parseFriendlyDuration("7d")
	if err != nil {
		t.Fatal(err)
	}
	if d != 7*24*time.Hour {
		t.Errorf("got %v", d)
	}
}

func TestParseFriendlyDurationExt2Weeks(t *testing.T) {
	d, err := parseFriendlyDuration("2w")
	if err != nil {
		t.Fatal(err)
	}
	if d != 14*24*time.Hour {
		t.Errorf("got %v", d)
	}
}

func TestParseFriendlyDurationExt2Invalid(t *testing.T) {
	_, err := parseFriendlyDuration("5x")
	if err == nil {
		t.Error("expected error for unknown unit")
	}
}

func TestParseFriendlyDurationExt2UpperCase(t *testing.T) {
	d, err := parseFriendlyDuration("3D")
	if err != nil {
		t.Fatal(err)
	}
	if d != 3*24*time.Hour {
		t.Errorf("got %v", d)
	}
}

// ── getDeleteArgs ──────────────────────────────────────────────

func TestGetDeleteArgsExt2(t *testing.T) {
	args := []string{"--delete", "abc123", "--delete", "def456"}
	got := getDeleteArgs(args)
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	if got[0] != "abc123" || got[1] != "def456" {
		t.Errorf("got %v", got)
	}
}

func TestGetDeleteArgsExt2None(t *testing.T) {
	args := []string{"--list"}
	got := getDeleteArgs(args)
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestGetDeleteArgsExt2Trailing(t *testing.T) {
	args := []string{"--delete"} // last flag, no value
	got := getDeleteArgs(args)
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// ── pluginResultToDoc ──────────────────────────────────────────

func TestPluginResultToDocEmpty(t *testing.T) {
	result := &gleann.PluginResult{}
	doc := pluginResultToDoc(result)
	if doc == nil {
		t.Fatal("should return doc")
	}
	if len(doc.Sections) != 0 {
		t.Errorf("sections = %d, want 0", len(doc.Sections))
	}
}

func TestPluginResultToDocWithSections(t *testing.T) {
	result := &gleann.PluginResult{
		Nodes: []gleann.PluginNode{
			{Type: "Document", Data: map[string]any{"title": "My Doc", "format": "pdf", "summary": "a doc", "word_count": float64(100)}},
			{Type: "Section", Data: map[string]any{"id": "s1", "heading": "Intro", "level": float64(1), "content": "Hello world"}},
			{Type: "Section", Data: map[string]any{"id": "s2", "heading": "Details", "level": float64(2), "content": "More details"}},
		},
		Edges: []gleann.PluginEdge{
			{Type: "HAS_SUBSECTION", From: "s1", To: "s2"},
		},
	}
	doc := pluginResultToDoc(result)
	if doc.Document.Title != "My Doc" {
		t.Errorf("title = %q", doc.Document.Title)
	}
	if doc.Document.Format != "pdf" {
		t.Errorf("format = %q", doc.Document.Format)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(doc.Sections))
	}
	// s2 should have ParentID = s1.
	if doc.Sections[1].ParentID != "s1" {
		t.Errorf("parent = %q, want s1", doc.Sections[1].ParentID)
	}
}

// ── markdownToPluginResult ─────────────────────────────────────

func TestMarkdownToPluginResultBasic(t *testing.T) {
	sections := []chunking.MarkdownSection{
		{ID: "h1", Heading: "Title", Level: 1, Content: "Some content"},
		{ID: "h2", Heading: "Sub", Level: 2, Content: "Sub content", ParentID: "h1"},
	}
	result := markdownToPluginResult(sections, "test.md", 100, "# Title\nSome content\n## Sub\nSub content")
	if result == nil {
		t.Fatal("nil result")
	}

	// Should have Document + 2 Section nodes.
	if len(result.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(result.Nodes))
	}
	if result.Nodes[0].Type != "Document" {
		t.Errorf("first node type = %s", result.Nodes[0].Type)
	}

	// Should have edges: HAS_SECTION for h1, HAS_SUBSECTION for h2.
	if len(result.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(result.Edges))
	}
	if result.Edges[0].Type != "HAS_SECTION" {
		t.Errorf("edge[0].Type = %s", result.Edges[0].Type)
	}
	if result.Edges[1].Type != "HAS_SUBSECTION" {
		t.Errorf("edge[1].Type = %s", result.Edges[1].Type)
	}
}

func TestMarkdownToPluginResultEmpty(t *testing.T) {
	result := markdownToPluginResult(nil, "empty.md", 0, "")
	if result == nil {
		t.Fatal("nil result")
	}
	// Should still have Document node.
	if len(result.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(result.Nodes))
	}
	if result.Nodes[0].Type != "Document" {
		t.Errorf("type = %s", result.Nodes[0].Type)
	}
}

// ── pickerModel ────────────────────────────────────────────────

func TestNewPickerModel(t *testing.T) {
	m := newPickerModel(nil)
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestPickerModelView(t *testing.T) {
	m := newPickerModel(nil)
	v := m.View()
	if v.Content == "" {
		t.Error("View should produce output")
	}
}

// ── formatBytes ────────────────────────────────────────────────

func TestFormatBytesSmall(t *testing.T) {
	if got := formatBytes(500); got != "500 B" {
		t.Errorf("got %q", got)
	}
}

func TestFormatBytesKB(t *testing.T) {
	got := formatBytes(2048)
	if !strings.Contains(got, "KB") {
		t.Errorf("got %q, want KB", got)
	}
}

func TestFormatBytesMB(t *testing.T) {
	got := formatBytes(5 * 1024 * 1024)
	if !strings.Contains(got, "MB") {
		t.Errorf("got %q, want MB", got)
	}
}

func TestFormatBytesGB(t *testing.T) {
	got := formatBytes(3 * 1024 * 1024 * 1024)
	if !strings.Contains(got, "GB") {
		t.Errorf("got %q, want GB", got)
	}
}

// ── checkDiskSpace ─────────────────────────────────────────────

func TestCheckDiskSpaceEmptyDir(t *testing.T) {
	tmp := t.TempDir()
	var msgs []string
	okFn := func(msg string) { msgs = append(msgs, "ok: "+msg) }
	warnFn := func(msg string) { msgs = append(msgs, "warn: "+msg) }

	checkDiskSpace(tmp, okFn, warnFn)
	if len(msgs) == 0 {
		t.Error("should produce at least one message")
	}
}

func TestCheckDiskSpaceWithFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "data.bin"), make([]byte, 1024), 0o644)

	var msgs []string
	okFn := func(msg string) { msgs = append(msgs, msg) }
	warnFn := func(msg string) { msgs = append(msgs, msg) }

	checkDiskSpace(tmp, okFn, warnFn)
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "storage") || strings.Contains(m, "used") {
			found = true
		}
	}
	if !found {
		t.Errorf("messages = %v", msgs)
	}
}

func TestCheckDiskSpaceNonexistent(t *testing.T) {
	var msgs []string
	okFn := func(msg string) { msgs = append(msgs, msg) }
	warnFn := func(msg string) { msgs = append(msgs, msg) }

	checkDiskSpace("/nonexistent/path/deep", okFn, warnFn)
	// Should not panic, may produce a warning or ok about root.
}

// ── configFilePath ─────────────────────────────────────────────

func TestConfigFilePathExt2(t *testing.T) {
	p := configFilePath()
	if p == "" {
		t.Error("should not be empty")
	}
	if !strings.Contains(p, "config.json") {
		t.Errorf("path = %q", p)
	}
}

// ── pickerModel Update ─────────────────────────────────────────

func TestPickerModelUpdateQuit(t *testing.T) {
	m := newPickerModel(nil)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'q'})
	pm := updated.(pickerModel)
	if !pm.quitting {
		t.Error("q should quit")
	}
}

func TestPickerModelUpdateNav(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "1", Title: "Conv1"},
		{ID: "2", Title: "Conv2"},
	}
	m := newPickerModel(items)

	// Down.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	pm := updated.(pickerModel)
	if pm.cursor != 1 {
		t.Errorf("cursor = %d, want 1", pm.cursor)
	}

	// Up.
	updated, _ = pm.Update(tea.KeyPressMsg{Code: 'k'})
	pm = updated.(pickerModel)
	if pm.cursor != 0 {
		t.Errorf("cursor = %d, want 0", pm.cursor)
	}
}

func TestPickerModelUpdateEnter(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "abc", Title: "Test"},
	}
	m := newPickerModel(items)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pm := updated.(pickerModel)
	if pm.selected == nil {
		t.Error("enter should set selected")
	}
}

func TestPickerModelViewWithItems(t *testing.T) {
	items := []conversations.Conversation{
		{ID: "1", Title: "My Chat"},
	}
	m := newPickerModel(items)
	v := m.View()
	if !strings.Contains(v.Content, "My Chat") {
		t.Error("should show conversation title")
	}
}
