package tui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ── handleImageCommand ─────────────────────────────────────────

func TestHandleImageCommand_EmptyPath(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleImageCommand("")
	if !strings.Contains(result, "Usage") {
		t.Error("expected usage message for empty path")
	}
}

func TestHandleImageCommand_FileNotFound(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleImageCommand("/nonexistent/image.png")
	if !strings.Contains(result, "not found") {
		t.Error("expected 'not found' error")
	}
}

func TestHandleImageCommand_Directory(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	result := m.handleImageCommand(dir)
	if !strings.Contains(result, "directory") {
		t.Error("expected directory warning")
	}
}

func TestHandleImageCommand_UnsupportedFormat(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "doc.pdf")
	os.WriteFile(f, []byte("fake"), 0644)
	result := m.handleImageCommand(f)
	if !strings.Contains(result, "Unsupported") {
		t.Errorf("expected unsupported format, got: %s", result)
	}
}

func TestHandleImageCommand_TooLarge(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "big.png")
	// Create a file that's just over 20MB via Truncate
	fh, _ := os.Create(f)
	fh.Truncate(21 * 1024 * 1024)
	fh.Close()
	result := m.handleImageCommand(f)
	if !strings.Contains(result, "too large") {
		t.Errorf("expected size error, got: %s", result)
	}
}

func TestHandleImageCommand_Success(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "test.png")
	data := []byte("fake-png-data")
	os.WriteFile(f, data, 0644)
	result := m.handleImageCommand(f)
	if !strings.Contains(result, "Queued") {
		t.Errorf("expected Queued message, got: %s", result)
	}
	if len(m.pendingImages) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(m.pendingImages))
	}
	decoded, err := base64.StdEncoding.DecodeString(m.pendingImages[0])
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if string(decoded) != "fake-png-data" {
		t.Errorf("decoded mismatch: %q", decoded)
	}
}

func TestHandleImageCommand_RelativePath(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "relative.jpg")
	os.WriteFile(f, []byte("ok"), 0644)
	// Use absolute path since relative depends on cwd
	result := m.handleImageCommand(f)
	if !strings.Contains(result, "Queued") {
		t.Errorf("expected success for jpg, got: %s", result)
	}
}

// ── handleAttachCommand ────────────────────────────────────────

func TestHandleAttachCommand_Empty(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleAttachCommand("")
	if !strings.Contains(result, "No files attached") {
		t.Error("expected empty attach list message")
	}
}

func TestHandleAttachCommand_List(t *testing.T) {
	m := newTestChatModel(t)
	m.attachedFiles = []string{"/tmp/a.txt", "/tmp/b.txt"}
	result := m.handleAttachCommand("--list")
	if !strings.Contains(result, "Attached files (2)") {
		t.Errorf("expected list with 2 files, got: %s", result)
	}
	if !strings.Contains(result, "a.txt") || !strings.Contains(result, "b.txt") {
		t.Error("expected file names in list")
	}
}

func TestHandleAttachCommand_Clear(t *testing.T) {
	m := newTestChatModel(t)
	m.attachedFiles = []string{"/a", "/b", "/c"}
	result := m.handleAttachCommand("--clear")
	if !strings.Contains(result, "Cleared 3") {
		t.Errorf("expected 'Cleared 3', got: %s", result)
	}
	if len(m.attachedFiles) != 0 {
		t.Error("attachedFiles should be empty after --clear")
	}
}

func TestHandleAttachCommand_NotFound(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleAttachCommand("/nonexistent/path/whatever")
	if !strings.Contains(result, "not found") {
		t.Errorf("expected not found: %s", result)
	}
}

func TestHandleAttachCommand_Duplicate(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "dup.txt")
	os.WriteFile(f, []byte("hello"), 0644)
	m.attachedFiles = []string{f}
	result := m.handleAttachCommand(f)
	if !strings.Contains(result, "Already attached") {
		t.Errorf("expected duplicate warning: %s", result)
	}
}

func TestHandleAttachCommand_File(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "file.txt")
	os.WriteFile(f, []byte("content"), 0644)
	result := m.handleAttachCommand(f)
	if !strings.Contains(result, "Attached") {
		t.Errorf("expected attached message: %s", result)
	}
	if len(m.attachedFiles) != 1 || m.attachedFiles[0] != f {
		t.Error("file not added to attachedFiles")
	}
}

func TestHandleAttachCommand_Directory(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	result := m.handleAttachCommand(dir)
	if !strings.Contains(result, "directory") {
		t.Errorf("expected directory message: %s", result)
	}
	if !strings.Contains(result, "2 files") {
		t.Errorf("expected file count: %s", result)
	}
}

// ── handleDetachCommand ────────────────────────────────────────

func TestHandleDetachCommand_Empty(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleDetachCommand("")
	if !strings.Contains(result, "Usage") {
		t.Error("expected usage message")
	}
}

func TestHandleDetachCommand_NotFound(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "b.txt")
	m.attachedFiles = []string{p}
	result := m.handleDetachCommand("nonexistent.txt")
	if !strings.Contains(result, "Not found") {
		t.Errorf("expected not found: %s", result)
	}
}

func TestHandleDetachCommand_ByAbsPath(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	p1 := filepath.Join(dir, "b.txt")
	p2 := filepath.Join(dir, "d.txt")
	m.attachedFiles = []string{p1, p2}
	result := m.handleDetachCommand(p1)
	if !strings.Contains(result, "Detached") {
		t.Errorf("expected detached: %s", result)
	}
	if len(m.attachedFiles) != 1 || m.attachedFiles[0] != p2 {
		t.Error("wrong file removed")
	}
}

func TestHandleDetachCommand_ByBaseName(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "file.txt")
	m.attachedFiles = []string{p}
	result := m.handleDetachCommand("file.txt")
	if !strings.Contains(result, "Detached") {
		t.Errorf("expected detached via basename: %s", result)
	}
	if len(m.attachedFiles) != 0 {
		t.Error("file should be removed")
	}
}

// ── handleIndexCommand ─────────────────────────────────────────

func TestHandleIndexCommand_Default(t *testing.T) {
	m := newTestChatModel(t)
	m.embeddingProvider = "ollama"
	m.embeddingModel = "bge-m3"
	result := m.handleIndexCommand("")
	if !strings.Contains(result, "test-index") {
		t.Errorf("expected index name in output: %s", result)
	}
	if !strings.Contains(result, "test-model") {
		t.Errorf("expected model name in output: %s", result)
	}
	if !strings.Contains(result, "ollama") {
		t.Errorf("expected embedding provider: %s", result)
	}
}

func TestHandleIndexCommand_Info(t *testing.T) {
	m := newTestChatModel(t)
	m.embeddingProvider = "ollama"
	m.embeddingModel = "bge-m3"
	result := m.handleIndexCommand("info")
	if !strings.Contains(result, "Index") {
		t.Error("expected index info")
	}
}

func TestHandleIndexCommand_SwitchNoConfig(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleIndexCommand("new-index")
	// Depending on whether a saved config exists, we get different errors.
	if !strings.Contains(result, "No index directory") && !strings.Contains(result, "not found") && !strings.Contains(result, "Error") {
		t.Errorf("expected error for missing index: %s", result)
	}
}

// ── handleBudgetCommand ────────────────────────────────────────

func TestHandleBudgetCommand_ZeroTokens(t *testing.T) {
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(nil, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "test"})
	result := m.handleBudgetCommand()
	if !strings.Contains(result, "Sent: ~0") {
		t.Errorf("expected zero sent tokens: %s", result)
	}
	if !strings.Contains(result, "Received: ~0") {
		t.Errorf("expected zero received tokens: %s", result)
	}
}

func TestHandleBudgetCommand_WithTokens(t *testing.T) {
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(nil, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "test"})
	m.tokensSent = 1500
	m.tokensReceived = 3200
	m.pendingImages = []string{"img1", "img2"}
	m.attachedFiles = []string{"/a.txt"}
	result := m.handleBudgetCommand()
	if !strings.Contains(result, "1500") {
		t.Errorf("expected sent count: %s", result)
	}
	if !strings.Contains(result, "3200") {
		t.Errorf("expected received count: %s", result)
	}
	if !strings.Contains(result, "4700") {
		t.Errorf("expected total: %s", result)
	}
	if !strings.Contains(result, "Pending images: 2") {
		t.Errorf("expected 2 pending images: %s", result)
	}
	if !strings.Contains(result, "Attached files: 1") {
		t.Errorf("expected 1 attached file: %s", result)
	}
}

// ── Mouse toggle ───────────────────────────────────────────────

func TestMouseToggleDefaults(t *testing.T) {
	m := newTestChatModel(t)
	m.mouseEnabled = true
	if !m.mouseEnabled {
		t.Error("mouse should be enabled by default")
	}
	m.mouseEnabled = !m.mouseEnabled
	if m.mouseEnabled {
		t.Error("toggle should disable mouse")
	}
}

// ── handleAudioCommand ─────────────────────────────────────────

func TestHandleAudioCommand_EmptyPath(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleAudioCommand("")
	if !strings.Contains(result, "Usage") {
		t.Error("expected usage message for empty path")
	}
}

func TestHandleAudioCommand_FileNotFound(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleAudioCommand("/nonexistent/audio.mp3")
	if !strings.Contains(result, "not found") {
		t.Error("expected 'not found' error")
	}
}

func TestHandleAudioCommand_Directory(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	result := m.handleAudioCommand(dir)
	if !strings.Contains(result, "directory") {
		t.Error("expected directory warning")
	}
}

func TestHandleAudioCommand_UnsupportedFormat(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "doc.txt")
	os.WriteFile(f, []byte("fake"), 0644)
	result := m.handleAudioCommand(f)
	if !strings.Contains(result, "Unsupported") {
		t.Errorf("expected unsupported format, got: %s", result)
	}
}

func TestHandleAudioCommand_TooLarge(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "big.mp3")
	fh, _ := os.Create(f)
	fh.Truncate(51 * 1024 * 1024) // over 50MB limit
	fh.Close()
	result := m.handleAudioCommand(f)
	if !strings.Contains(result, "too large") {
		t.Errorf("expected size error, got: %s", result)
	}
}

func TestHandleAudioCommand_Success(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "test.mp3")
	data := []byte("fake-mp3-data")
	os.WriteFile(f, data, 0644)
	result := m.handleAudioCommand(f)
	if !strings.Contains(result, "Queued") {
		t.Errorf("expected Queued message, got: %s", result)
	}
	if len(m.pendingImages) != 1 {
		t.Fatalf("expected 1 pending audio, got %d", len(m.pendingImages))
	}
	decoded, err := base64.StdEncoding.DecodeString(m.pendingImages[0])
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if string(decoded) != "fake-mp3-data" {
		t.Errorf("decoded mismatch: %q", decoded)
	}
}

func TestHandleAudioCommand_MultipleFormats(t *testing.T) {
	validExts := []string{".wav", ".flac", ".ogg", ".m4a", ".aac", ".opus"}
	for _, ext := range validExts {
		t.Run(ext, func(t *testing.T) {
			m := newTestChatModel(t)
			f := filepath.Join(t.TempDir(), "audio"+ext)
			os.WriteFile(f, []byte("data"), 0644)
			result := m.handleAudioCommand(f)
			if !strings.Contains(result, "Queued") {
				t.Errorf("expected Queued for %s, got: %s", ext, result)
			}
		})
	}
}

// ── handleIndexAdd / handleIndexRemove ─────────────────────────

func TestHandleIndexAdd_Empty(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleIndexAdd("")
	if !strings.Contains(result, "Usage") {
		t.Errorf("expected usage message, got: %s", result)
	}
}

func TestHandleIndexAdd_AlreadyPrimary(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleIndexAdd("test-index") // same as indexName
	if !strings.Contains(result, "already the primary") {
		t.Errorf("expected primary index warning, got: %s", result)
	}
}

func TestHandleIndexAdd_AlreadyActive(t *testing.T) {
	m := newTestChatModel(t)
	m.activeIndexes = []string{"extra"}
	result := m.handleIndexAdd("extra")
	if !strings.Contains(result, "already active") {
		t.Errorf("expected already active, got: %s", result)
	}
}

func TestHandleIndexAdd_NoConfig(t *testing.T) {
	m := newTestChatModel(t)
	// With no saved config, LoadSavedConfig returns nil
	result := m.handleIndexAdd("some-new-index")
	if !strings.Contains(result, "No index directory") && !strings.Contains(result, "Error") && !strings.Contains(result, "not found") {
		t.Errorf("expected error for missing config, got: %s", result)
	}
}

func TestHandleIndexRemove_Empty(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleIndexRemove("")
	if !strings.Contains(result, "Usage") {
		t.Errorf("expected usage message, got: %s", result)
	}
}

func TestHandleIndexRemove_NotInSet(t *testing.T) {
	m := newTestChatModel(t)
	m.activeIndexes = []string{"alpha"}
	result := m.handleIndexRemove("beta")
	if !strings.Contains(result, "not in the active set") {
		t.Errorf("expected not-in-set error, got: %s", result)
	}
}

func TestHandleIndexRemove_Success(t *testing.T) {
	m := newTestChatModel(t)
	m.activeIndexes = []string{"alpha", "beta", "gamma"}
	result := m.handleIndexRemove("beta")
	if !strings.Contains(result, "Removed") {
		t.Errorf("expected removed message, got: %s", result)
	}
	if len(m.activeIndexes) != 2 {
		t.Fatalf("expected 2 active indexes, got %d", len(m.activeIndexes))
	}
	for _, idx := range m.activeIndexes {
		if idx == "beta" {
			t.Error("beta should have been removed")
		}
	}
}

func TestHandleIndexRemove_LastOne(t *testing.T) {
	m := newTestChatModel(t)
	m.activeIndexes = []string{"only"}
	result := m.handleIndexRemove("only")
	if !strings.Contains(result, "Removed") {
		t.Errorf("expected removed message, got: %s", result)
	}
	if !strings.Contains(result, "Querying only") {
		t.Errorf("expected 'Querying only' fallback, got: %s", result)
	}
	if len(m.activeIndexes) != 0 {
		t.Error("activeIndexes should be empty")
	}
}

// ── /clear resets state ────────────────────────────────────────

func TestClearResetsNewFields(t *testing.T) {
	m := newTestChatModel(t)
	m.pendingImages = []string{"img"}
	m.attachedFiles = []string{"/f"}
	m.tokensSent = 100
	m.tokensReceived = 200

	// Simulate /clear: reset fields as in Update
	m.pendingImages = nil
	m.attachedFiles = nil
	m.tokensSent = 0
	m.tokensReceived = 0

	if len(m.pendingImages) != 0 || len(m.attachedFiles) != 0 {
		t.Error("clear should reset slices")
	}
	if m.tokensSent != 0 || m.tokensReceived != 0 {
		t.Error("clear should reset token counters")
	}
}

// ── extractScript ──────────────────────────────────────────────

func TestExtractScript_BashFence(t *testing.T) {
	input := "Here's a script:\n```bash\n#!/bin/bash\necho hello\n```\nDone."
	got := extractScript(input)
	if !strings.HasPrefix(got, "#!/bin/bash") {
		t.Errorf("expected script start, got: %s", got)
	}
	if !strings.Contains(got, "echo hello") {
		t.Error("expected echo hello in script")
	}
}

func TestExtractScript_ShFence(t *testing.T) {
	input := "```sh\nset -euo pipefail\nls -la\n```"
	got := extractScript(input)
	if !strings.Contains(got, "set -euo pipefail") {
		t.Errorf("expected script, got: %s", got)
	}
}

func TestExtractScript_GenericFence(t *testing.T) {
	input := "```\n#!/bin/bash\nfind . -name '*.go'\n```"
	got := extractScript(input)
	if !strings.HasPrefix(got, "#!/bin/bash") {
		t.Errorf("expected shebang, got: %s", got)
	}
}

func TestExtractScript_NoFence(t *testing.T) {
	input := "#!/bin/bash\necho 'no fences'"
	got := extractScript(input)
	if got != input {
		t.Errorf("expected raw script, got: %s", got)
	}
}

func TestExtractScript_Empty(t *testing.T) {
	got := extractScript("Just some text without any script")
	if got != "" {
		t.Errorf("expected empty, got: %s", got)
	}
}

// ── handleScriptCommand ────────────────────────────────────────

func TestHandleScriptCommand_Empty(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleScriptCommand("")
	if !strings.Contains(result, "Script-first mode") {
		t.Errorf("expected usage message, got: %s", result)
	}
}

// ── handleVideoCommand ─────────────────────────────────────────

func TestHandleVideoCommand_Empty(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleVideoCommand("")
	if !strings.Contains(result, "Usage") {
		t.Error("expected usage message")
	}
}

func TestHandleVideoCommand_FileNotFound(t *testing.T) {
	m := newTestChatModel(t)
	result := m.handleVideoCommand("/nonexistent/video.mp4")
	if !strings.Contains(result, "not found") {
		t.Error("expected not found error")
	}
}

func TestHandleVideoCommand_Directory(t *testing.T) {
	m := newTestChatModel(t)
	dir := t.TempDir()
	result := m.handleVideoCommand(dir)
	if !strings.Contains(result, "directory") {
		t.Error("expected directory warning")
	}
}

func TestHandleVideoCommand_UnsupportedFormat(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "video.txt")
	os.WriteFile(f, []byte("fake"), 0644)
	result := m.handleVideoCommand(f)
	if !strings.Contains(result, "Unsupported") {
		t.Errorf("expected unsupported format, got: %s", result)
	}
}

func TestHandleVideoCommand_TooLarge(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "big.mp4")
	fh, _ := os.Create(f)
	fh.Truncate(101 * 1024 * 1024) // over 100MB limit
	fh.Close()
	result := m.handleVideoCommand(f)
	if !strings.Contains(result, "too large") {
		t.Errorf("expected size error, got: %s", result)
	}
}

func TestHandleVideoCommand_Success(t *testing.T) {
	m := newTestChatModel(t)
	f := filepath.Join(t.TempDir(), "test.mp4")
	data := []byte("fake-video-data")
	os.WriteFile(f, data, 0644)
	result := m.handleVideoCommand(f)
	if !strings.Contains(result, "Queued") {
		t.Errorf("expected Queued message, got: %s", result)
	}
	if len(m.pendingImages) != 1 {
		t.Fatalf("expected 1 pending video, got %d", len(m.pendingImages))
	}
	decoded, err := base64.StdEncoding.DecodeString(m.pendingImages[0])
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if string(decoded) != "fake-video-data" {
		t.Errorf("decoded mismatch: %q", decoded)
	}
}

func TestHandleVideoCommand_MultipleFormats(t *testing.T) {
	validExts := []string{".avi", ".mkv", ".mov", ".webm", ".flv", ".wmv"}
	for _, ext := range validExts {
		t.Run(ext, func(t *testing.T) {
			m := newTestChatModel(t)
			f := filepath.Join(t.TempDir(), "video"+ext)
			os.WriteFile(f, []byte("data"), 0644)
			result := m.handleVideoCommand(f)
			if !strings.Contains(result, "Queued") {
				t.Errorf("expected Queued for %s, got: %s", ext, result)
			}
		})
	}
}
