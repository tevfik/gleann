package gleann

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ── DefaultChatConfig ──────────────────────────────────────────

func TestDefaultChatConfig(t *testing.T) {
	cfg := DefaultChatConfig()
	if cfg.Provider != LLMOllama {
		t.Errorf("Provider = %q, want ollama", cfg.Provider)
	}
	if cfg.Model == "" {
		t.Error("Model should not be empty")
	}
	if cfg.Temperature == 0 {
		t.Error("Temperature should not be 0")
	}
	if cfg.MaxTokens == 0 {
		t.Error("MaxTokens should not be 0")
	}
	if cfg.SystemPrompt == "" {
		t.Error("SystemPrompt should not be empty")
	}
}

// ── NewChat ────────────────────────────────────────────────────

func TestNewChatNilSearcher(t *testing.T) {
	cfg := DefaultChatConfig()
	c := NewChat(nil, cfg)
	if c == nil {
		t.Fatal("NewChat returned nil")
	}
}

func TestNewChatDefaultBaseURL(t *testing.T) {
	// Ollama with empty BaseURL.
	cfg := ChatConfig{Provider: LLMOllama, Model: "test"}
	c := NewChat(nil, cfg)
	if c.config.BaseURL == "" {
		t.Error("BaseURL should be defaulted for ollama")
	}
}

func TestNewChatOpenAIDefaultBaseURL(t *testing.T) {
	cfg := ChatConfig{Provider: LLMOpenAI, Model: "test"}
	c := NewChat(nil, cfg)
	if c.config.BaseURL == "" {
		t.Error("BaseURL should be defaulted for openai")
	}
}

func TestNewChatAnthropicDefaultBaseURL(t *testing.T) {
	cfg := ChatConfig{Provider: LLMAnthropic, Model: "test"}
	c := NewChat(nil, cfg)
	if c.config.BaseURL == "" {
		t.Error("BaseURL should be defaulted for anthropic")
	}
}

// ── Setters / Getters ──────────────────────────────────────────

func TestChatSetTemperature(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.SetTemperature(0.5)
	if c.Config().Temperature != 0.5 {
		t.Errorf("Temperature = %f", c.Config().Temperature)
	}
}

func TestChatSetMaxTokens(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.SetMaxTokens(4096)
	if c.Config().MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d", c.Config().MaxTokens)
	}
}

func TestChatSetSystemPrompt(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.SetSystemPrompt("Be helpful")
	if c.Config().SystemPrompt != "Be helpful" {
		t.Error("SystemPrompt mismatch")
	}
}

func TestChatSetModel(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.SetModel("llama3.2")
	if c.Config().Model != "llama3.2" {
		t.Error("Model mismatch")
	}
}

func TestChatSetMemoryContext(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.SetMemoryContext("<memory>test</memory>")
	if c.memoryContext != "<memory>test</memory>" {
		t.Error("memoryContext mismatch")
	}
}

func TestChatGetSearcher(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	if c.GetSearcher() != nil {
		t.Error("expected nil searcher")
	}
}

// ── History / ClearHistory ─────────────────────────────────────

func TestChatClearHistory(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.AppendHistory(ChatMessage{Role: "user", Content: "hello"})
	c.AppendHistory(ChatMessage{Role: "assistant", Content: "hi"})
	if len(c.History()) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(c.History()))
	}

	c.ClearHistory()
	if len(c.History()) != 0 {
		t.Errorf("expected empty history, got %d", len(c.History()))
	}
}

// ── SaveSession / LoadSession ──────────────────────────────────

func TestSaveAndLoadSession(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	c.AppendHistory(ChatMessage{Role: "user", Content: "What is Go?"})
	c.AppendHistory(ChatMessage{Role: "assistant", Content: "A programming language."})

	dir := t.TempDir()
	path, err := c.SaveSession(dir, "testindex")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	// Load into a new chat.
	c2 := NewChat(nil, DefaultChatConfig())
	err = c2.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.History()) != 2 {
		t.Errorf("loaded history len = %d, want 2", len(c2.History()))
	}
	if c2.History()[0].Content != "What is Go?" {
		t.Errorf("first message = %q", c2.History()[0].Content)
	}
}

func TestSaveSessionEmpty(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	dir := t.TempDir()
	path, err := c.SaveSession(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Error("empty history should return empty path")
	}
}

func TestLoadSessionInvalidPath(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	err := c.LoadSession("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error")
	}
}

func TestLoadSessionInvalidJSON(t *testing.T) {
	c := NewChat(nil, DefaultChatConfig())
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.json")
	os.WriteFile(path, []byte("not json"), 0o644)

	err := c.LoadSession(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── LoadMediaFiles ─────────────────────────────────────────────

func TestLoadMediaFiles(t *testing.T) {
	tmp := t.TempDir()
	f1 := filepath.Join(tmp, "img1.jpg")
	f2 := filepath.Join(tmp, "img2.png")
	os.WriteFile(f1, []byte("fake image 1"), 0o644)
	os.WriteFile(f2, []byte("fake image 2"), 0o644)

	encoded, err := LoadMediaFiles([]string{f1, f2})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 2 {
		t.Fatalf("expected 2, got %d", len(encoded))
	}
	// Verify base64 encoding.
	decoded, err := base64.StdEncoding.DecodeString(encoded[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "fake image 1" {
		t.Errorf("decoded = %q", decoded)
	}
}

func TestLoadMediaFilesNonExistent(t *testing.T) {
	_, err := LoadMediaFiles([]string{"/nonexistent/file.jpg"})
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestLoadMediaFilesEmpty(t *testing.T) {
	encoded, err := LoadMediaFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 0 {
		t.Error("expected empty")
	}
}

// ── hasImages ──────────────────────────────────────────────────

func TestHasImages(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "text only"},
	}
	if hasImages(msgs) {
		t.Error("no images expected")
	}

	msgs = append(msgs, ChatMessage{
		Role:    "user",
		Content: "with image",
		Images:  []string{"base64data"},
	})
	if !hasImages(msgs) {
		t.Error("should detect images")
	}
}

func TestHasImagesEmpty(t *testing.T) {
	if hasImages(nil) {
		t.Error("nil should not have images")
	}
}

// ── toOpenAIMultimodalMessages ─────────────────────────────────

func TestToOpenAIMultimodalMessages(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Look at this", Images: []string{"base64img"}},
		{Role: "assistant", Content: "I see"},
	}

	out := toOpenAIMultimodalMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}

	// First message (no images) should be simple.
	m0, _ := json.Marshal(out[0])
	if !contains(string(m0), "system") {
		t.Error("first should be system message")
	}

	// Second message (with images) should have content parts.
	m1, _ := json.Marshal(out[1])
	s1 := string(m1)
	if !contains(s1, "image_url") {
		t.Error("should contain image_url")
	}
	if !contains(s1, "base64img") {
		t.Error("should contain image data")
	}
}

func TestToOpenAIMultimodalMessagesTextOnly(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	out := toOpenAIMultimodalMessages(msgs)
	if len(out) != 1 {
		t.Fatal("expected 1")
	}
	m, _ := json.Marshal(out[0])
	if !contains(string(m), "hello") {
		t.Error("should contain message")
	}
}

// ── toAnthropicMultimodalMessages ──────────────────────────────

func TestToAnthropicMultimodalMessages(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "Describe this image", Images: []string{"imgdata"}},
		{Role: "assistant", Content: "It shows..."},
	}

	out := toAnthropicMultimodalMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}

	m0, _ := json.Marshal(out[0])
	s := string(m0)
	if !contains(s, "image") {
		t.Error("should contain image block")
	}
	if !contains(s, "base64") {
		t.Error("should contain base64 source")
	}
}

func TestToAnthropicMultimodalMessagesTextOnly(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	out := toAnthropicMultimodalMessages(msgs)
	if len(out) != 1 {
		t.Fatal("expected 1")
	}
}

// Helper.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsString(s, substr))
}

func containsString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
