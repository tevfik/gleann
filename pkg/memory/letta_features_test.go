package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// ── Block CharLimit Tests ─────────────────────────────────────────

func TestBlock_ExceedsLimit_Unlimited(t *testing.T) {
	b := &Block{Content: "hello world", CharLimit: 0}
	if b.ExceedsLimit() {
		t.Error("unlimited block should not exceed limit")
	}
}

func TestBlock_ExceedsLimit_Under(t *testing.T) {
	b := &Block{Content: "hello", CharLimit: 100}
	if b.ExceedsLimit() {
		t.Error("short content should not exceed limit")
	}
}

func TestBlock_ExceedsLimit_Over(t *testing.T) {
	b := &Block{Content: "hello world this is longer", CharLimit: 10}
	if !b.ExceedsLimit() {
		t.Error("expected content to exceed limit")
	}
}

func TestBlock_TruncateToLimit(t *testing.T) {
	b := &Block{Content: "this is a very long block content that should be truncated at limit", CharLimit: 30}
	b.TruncateToLimit()
	if len(b.Content) > 30 {
		t.Errorf("expected content ≤30 chars, got %d", len(b.Content))
	}
	if b.Content[len(b.Content)-1] != ']' {
		t.Error("expected truncated content to end with [truncated]")
	}
}

func TestBlock_TruncateToLimit_NoOp(t *testing.T) {
	b := &Block{Content: "short", CharLimit: 100}
	b.TruncateToLimit()
	if b.Content != "short" {
		t.Errorf("expected content unchanged, got %q", b.Content)
	}
}

// ── Scope Tests ───────────────────────────────────────────────────

func TestManager_AddScopedNote(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	// Add global and scoped blocks.
	mgr.AddNote(TierLong, "global", "visible to all")
	mgr.AddScopedNote("conv-123", TierLong, "scoped", "only for conv-123")
	mgr.AddScopedNote("conv-456", TierLong, "scoped", "only for conv-456")

	// List all should return 3.
	all, err := mgr.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 total blocks, got %d", len(all))
	}
}

func TestManager_ListScoped(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	mgr.AddNote(TierLong, "global", "visible everywhere")
	mgr.AddScopedNote("conv-123", TierLong, "scoped", "conv-123 fact")
	mgr.AddScopedNote("conv-456", TierLong, "scoped", "conv-456 fact")

	// ListScoped for conv-123 should return global + conv-123 = 2.
	blocks, err := mgr.ListScoped("conv-123", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 blocks for conv-123 scope, got %d", len(blocks))
	}
}

func TestManager_SearchScoped(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	mgr.AddNote(TierLong, "pref", "I like dark mode")
	mgr.AddScopedNote("conv-123", TierLong, "pref", "dark theme is nice")
	mgr.AddScopedNote("conv-456", TierLong, "pref", "dark background preferred")

	// SearchScoped "dark" for conv-123 → global match + conv-123 match = 2.
	results, err := mgr.SearchScoped("conv-123", "dark")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 scoped results for 'dark', got %d", len(results))
	}
}

func TestManager_BuildScopedContext(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	mgr.AddNote(TierLong, "global", "global fact")
	mgr.AddScopedNote("conv-A", TierLong, "scoped", "A-only fact")
	mgr.AddScopedNote("conv-B", TierLong, "scoped", "B-only fact")

	// Scoped context for conv-A.
	cw, err := mgr.BuildScopedContext("conv-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.LongTerm) != 2 { // global + conv-A
		t.Errorf("expected 2 long-term blocks in scoped context, got %d", len(cw.LongTerm))
	}

	// Unscoped should return all.
	cwAll, err := mgr.BuildScopedContext("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cwAll.LongTerm) != 3 {
		t.Errorf("expected 3 long-term blocks in unscoped context, got %d", len(cwAll.LongTerm))
	}
}

// ── CharLimit Manager Integration Tests ───────────────────────────

func TestManager_DefaultCharLimit(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	mgr.DefaultCharLimit = 50
	mgr.EnforceCharLimit = true
	defer mgr.Close()

	longContent := "This is some very long content that definitely exceeds the fifty character limit set on the manager"
	block, err := mgr.Remember(longContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Content) > 50 {
		t.Errorf("expected content ≤50, got %d chars", len(block.Content))
	}
	if block.CharLimit != 50 {
		t.Errorf("expected char_limit=50, got %d", block.CharLimit)
	}
}

func TestManager_CharLimitFromEnv(t *testing.T) {
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "200")

	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	if mgr.DefaultCharLimit != 200 {
		t.Errorf("expected DefaultCharLimit=200, got %d", mgr.DefaultCharLimit)
	}
	if !mgr.EnforceCharLimit {
		t.Error("expected EnforceCharLimit=true")
	}
}

func TestManager_CharLimitFromEnv_Invalid(t *testing.T) {
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "invalid")

	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	if mgr.DefaultCharLimit != 0 {
		t.Errorf("expected DefaultCharLimit=0 for invalid env, got %d", mgr.DefaultCharLimit)
	}
}

// ── filterScope Tests ─────────────────────────────────────────────

func TestFilterScope_EmptyScope(t *testing.T) {
	blocks := []Block{
		{Content: "a", Scope: ""},
		{Content: "b", Scope: "conv-1"},
	}
	result := filterScope(blocks, "")
	if len(result) != 2 {
		t.Errorf("empty scope should return all blocks, got %d", len(result))
	}
}

func TestFilterScope_WithScope(t *testing.T) {
	blocks := []Block{
		{Content: "global", Scope: ""},
		{Content: "conv1", Scope: "conv-1"},
		{Content: "conv2", Scope: "conv-2"},
	}
	result := filterScope(blocks, "conv-1")
	if len(result) != 2 { // global + conv-1
		t.Errorf("expected 2 blocks for conv-1 scope, got %d", len(result))
	}
}

// ── Sleep-Time Engine Tests ───────────────────────────────────────

func TestSleepTimeConfig_Defaults(t *testing.T) {
	cfg := DefaultSleepTimeConfig()
	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
	if cfg.Interval != 30*time.Minute {
		t.Errorf("expected 30m interval, got %v", cfg.Interval)
	}
	if cfg.MaxConvs != 5 {
		t.Errorf("expected 5 max convs, got %d", cfg.MaxConvs)
	}
}

func TestSleepTimeConfig_Env(t *testing.T) {
	t.Setenv("GLEANN_SLEEPTIME_ENABLED", "true")
	t.Setenv("GLEANN_SLEEPTIME_INTERVAL", "15m")
	t.Setenv("GLEANN_SLEEPTIME_MAX_CONVS", "10")

	cfg := DefaultSleepTimeConfig()
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.Interval != 15*time.Minute {
		t.Errorf("expected 15m interval, got %v", cfg.Interval)
	}
	if cfg.MaxConvs != 10 {
		t.Errorf("expected 10 max convs, got %d", cfg.MaxConvs)
	}
}

func TestSleepTimeConfig_Env_Invalid(t *testing.T) {
	t.Setenv("GLEANN_SLEEPTIME_INTERVAL", "notaduration")
	t.Setenv("GLEANN_SLEEPTIME_MAX_CONVS", "abc")

	cfg := DefaultSleepTimeConfig()
	if cfg.Interval != 30*time.Minute {
		t.Errorf("expected default 30m for invalid interval, got %v", cfg.Interval)
	}
	if cfg.MaxConvs != 5 {
		t.Errorf("expected default 5 for invalid max_convs, got %d", cfg.MaxConvs)
	}
}

func TestSleepTimeEngine_NoConvStore(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	cfg := SleepTimeConfig{Enabled: true, ConvStore: nil}
	engine := NewSleepTimeEngine(mgr, cfg)

	err = engine.RunOnce(context.Background())
	if err == nil {
		t.Error("expected error with nil conv store")
	}
}

func TestSleepTimeEngine_EmptyConversations(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	convDir := filepath.Join(t.TempDir(), "convs")
	os.MkdirAll(convDir, 0o755)
	convStore := conversations.NewStore(convDir)

	cfg := SleepTimeConfig{
		Enabled:   true,
		MaxConvs:  5,
		ConvStore: convStore,
	}
	engine := NewSleepTimeEngine(mgr, cfg)

	// Should not error on empty conversations.
	err = engine.RunOnce(context.Background())
	if err != nil {
		t.Errorf("expected no error on empty convs, got: %v", err)
	}
}

func TestSleepTimeEngine_StartDisabled(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	cfg := SleepTimeConfig{Enabled: false}
	engine := NewSleepTimeEngine(mgr, cfg)

	// Start should be a no-op when disabled.
	stopCh := make(chan struct{})
	engine.Start(stopCh)
	close(stopCh)
	// No goroutine leak — just ensure no panic.
}
