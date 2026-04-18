package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ════════════════════════════════════════════════════════════════
// IndexManageModel extended tests
// ════════════════════════════════════════════════════════════════

func TestIndexManageUpdateListQuit(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'q'})
	im := updated.(IndexManageModel)
	if !im.Quitting() {
		t.Error("q should quit")
	}
}

func TestIndexManageUpdateListCtrlC(t *testing.T) {
	m := NewIndexManageModel(t.TempDir())
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	im := updated.(IndexManageModel)
	if !im.Quitting() {
		t.Error("ctrl+c should quit")
	}
}

func TestIndexManageUpdateDetailBack(t *testing.T) {
	// Create model with fake index so we can navigate to detail.
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imDetail,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	im := updated.(IndexManageModel)
	if im.state != imList {
		t.Errorf("state = %d, want imList", im.state)
	}
}

func TestIndexManageUpdateDetailDelete(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imDetail,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	im := updated.(IndexManageModel)
	if im.state != imConfirm {
		t.Errorf("state = %d, want imConfirm", im.state)
	}
}

func TestIndexManageUpdateConfirmNo(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imConfirm,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'n'})
	im := updated.(IndexManageModel)
	if im.state != imList {
		t.Errorf("state = %d, want imList", im.state)
	}
}

func TestIndexManageUpdateConfirmCtrlC(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imConfirm,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	im := updated.(IndexManageModel)
	if !im.Quitting() {
		t.Error("ctrl+c should quit")
	}
}

func TestIndexManageUpdateDeleteResult(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imConfirm,
	}

	// Simulate deleteResultMsg.
	updated, _ := m.Update(deleteResultMsg{name: "test", err: nil})
	im := updated.(IndexManageModel)
	if !strings.Contains(im.status, "Deleted") {
		t.Errorf("status = %q", im.status)
	}
	if im.state != imList {
		t.Errorf("state = %d, want imList", im.state)
	}
}

func TestIndexManageViewDetail(t *testing.T) {
	now := time.Now()
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes: []gleann.IndexMeta{{
			Name:           "test-idx",
			Backend:        "hnsw",
			EmbeddingModel: "bge-m3",
			NumPassages:    42,
			Dimensions:     768,
			CreatedAt:      now,
			UpdatedAt:      now,
		}},
		cursor: 0,
		state:  imDetail,
		width:  100,
		height: 40,
	}

	v := m.View()
	if !strings.Contains(v.Content, "test-idx") {
		t.Error("detail view should show index name")
	}
}

func TestIndexManageViewConfirm(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "to-delete", NumPassages: 10}},
		cursor:   0,
		state:    imConfirm,
		width:    100,
		height:   40,
	}

	v := m.View()
	if !strings.Contains(v.Content, "to-delete") {
		t.Error("confirm view should show index name")
	}
}

func TestIndexManageViewListWithStatus(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "idx1", NumPassages: 5, EmbeddingModel: "bge-m3", Dimensions: 768}},
		cursor:   0,
		state:    imList,
		status:   "✓ Deleted index \"old\"",
		width:    100,
		height:   40,
	}

	v := m.View()
	if !strings.Contains(v.Content, "Deleted") {
		t.Error("should show status message")
	}
}

func TestIndexManageViewListError(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  nil,
		state:    imList,
		err:      os.ErrNotExist,
		width:    100,
		height:   40,
	}

	v := m.View()
	if !strings.Contains(v.Content, "Error") {
		t.Error("should show error")
	}
}

func TestIndexManageViewQuitting(t *testing.T) {
	m := IndexManageModel{quitting: true}
	v := m.View()
	if v.Content != "" {
		t.Error("quitting view should be empty")
	}
}

func TestIndexManageListEnterToDetail(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imList,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	im := updated.(IndexManageModel)
	if im.state != imDetail {
		t.Errorf("state = %d, want imDetail", im.state)
	}
}

func TestIndexManageListDelete(t *testing.T) {
	m := IndexManageModel{
		indexDir: t.TempDir(),
		indexes:  []gleann.IndexMeta{{Name: "test"}},
		cursor:   0,
		state:    imList,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	im := updated.(IndexManageModel)
	if im.state != imConfirm {
		t.Errorf("state = %d, want imConfirm", im.state)
	}
}

// ── timeAgo ────────────────────────────────────────────────────

func TestTimeAgoJustNow(t *testing.T) {
	got := timeAgo(time.Now().Add(-10 * time.Second))
	if got != "just now" {
		t.Errorf("got %q", got)
	}
}

func TestTimeAgoMinutes(t *testing.T) {
	got := timeAgo(time.Now().Add(-15 * time.Minute))
	if !strings.HasSuffix(got, "m ago") {
		t.Errorf("got %q", got)
	}
}

func TestTimeAgoHours(t *testing.T) {
	got := timeAgo(time.Now().Add(-5 * time.Hour))
	if !strings.HasSuffix(got, "h ago") {
		t.Errorf("got %q", got)
	}
}

func TestTimeAgoDays(t *testing.T) {
	got := timeAgo(time.Now().Add(-3 * 24 * time.Hour))
	if !strings.HasSuffix(got, "d ago") {
		t.Errorf("got %q", got)
	}
}

func TestTimeAgoOld(t *testing.T) {
	got := timeAgo(time.Now().Add(-365 * 24 * time.Hour))
	if !strings.Contains(got, ",") { // formatted as "Jan 2, 2006"
		t.Errorf("got %q, expected formatted date", got)
	}
}

func TestTimeAgoZero(t *testing.T) {
	got := timeAgo(time.Time{})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ════════════════════════════════════════════════════════════════
// IndexListModel extended tests
// ════════════════════════════════════════════════════════════════

func TestIndexListUpdateNavigation(t *testing.T) {
	m := IndexListModel{
		indexes: []gleann.IndexMeta{
			{Name: "idx1"},
			{Name: "idx2"},
		},
		cursor: 0,
	}

	// Down.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	il := updated.(IndexListModel)
	if il.cursor != 1 {
		t.Errorf("cursor = %d, want 1", il.cursor)
	}

	// Down again (to "no index" row).
	updated, _ = il.Update(tea.KeyPressMsg{Code: 'j'})
	il = updated.(IndexListModel)
	if il.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (no-index row)", il.cursor)
	}

	// Up.
	updated, _ = il.Update(tea.KeyPressMsg{Code: 'k'})
	il = updated.(IndexListModel)
	if il.cursor != 1 {
		t.Errorf("cursor = %d, want 1", il.cursor)
	}
}

func TestIndexListSelectIndex(t *testing.T) {
	m := IndexListModel{
		indexes: []gleann.IndexMeta{{Name: "myindex"}},
		cursor:  0,
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	il := updated.(IndexListModel)
	if il.Selected() != "myindex" {
		t.Errorf("selected = %q", il.Selected())
	}
}

func TestIndexListSkip(t *testing.T) {
	m := IndexListModel{
		indexes: []gleann.IndexMeta{{Name: "myindex"}},
		cursor:  1, // past all indexes → "no index" option
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	il := updated.(IndexListModel)
	if !il.Skipped() {
		t.Error("should be skipped")
	}
}

func TestIndexListViewWithIndexes(t *testing.T) {
	m := IndexListModel{
		indexes: []gleann.IndexMeta{
			{Name: "idx1", NumPassages: 100, Backend: "hnsw", EmbeddingModel: "bge-m3"},
			{Name: "idx2", NumPassages: 50, Backend: "hnsw", EmbeddingModel: "nomic"},
		},
		cursor: 0,
		width:  100,
		height: 40,
	}

	v := m.View()
	if !strings.Contains(v.Content, "idx1") {
		t.Error("should show idx1")
	}
	if !strings.Contains(v.Content, "idx2") {
		t.Error("should show idx2")
	}
}

func TestIndexListViewWithError(t *testing.T) {
	m := IndexListModel{
		err: os.ErrPermission,
	}
	v := m.View()
	if !strings.Contains(v.Content, "Error") {
		t.Error("should show error")
	}
}

// ════════════════════════════════════════════════════════════════
// HomeModel extended tests
// ════════════════════════════════════════════════════════════════

func TestHomeModelEnterSetup(t *testing.T) {
	m := NewHomeModel()
	m.cursor = 0 // Setup

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	hm := updated.(HomeModel)
	if hm.Chosen() != ScreenOnboard {
		t.Errorf("chosen = %d, want ScreenOnboard", hm.Chosen())
	}
}

func TestHomeModelEnterChat(t *testing.T) {
	m := NewHomeModel()
	m.cursor = 1

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	hm := updated.(HomeModel)
	if hm.Chosen() != ScreenChat {
		t.Errorf("chosen = %d, want ScreenChat", hm.Chosen())
	}
}

func TestHomeModelEnterIndexes(t *testing.T) {
	m := NewHomeModel()
	m.cursor = 2

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	hm := updated.(HomeModel)
	if hm.Chosen() != ScreenIndexes {
		t.Errorf("chosen = %d, want ScreenIndexes", hm.Chosen())
	}
}

func TestHomeModelEnterPlugins(t *testing.T) {
	m := NewHomeModel()
	m.cursor = 3

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	hm := updated.(HomeModel)
	if hm.Chosen() != ScreenPlugins {
		t.Errorf("chosen = %d, want ScreenPlugins", hm.Chosen())
	}
}

func TestHomeModelEnterQuitOption(t *testing.T) {
	m := NewHomeModel()
	m.cursor = 4 // Quit menu item

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	hm := updated.(HomeModel)
	if !hm.Quitting() {
		t.Error("expected quitting")
	}
}

func TestHomeModelViewContent(t *testing.T) {
	m := NewHomeModel()
	m.width = 80
	m.height = 40
	v := m.View()
	if !strings.Contains(v.Content, "Setup") {
		t.Error("should show Setup option")
	}
	if !strings.Contains(v.Content, "Chat") {
		t.Error("should show Chat option")
	}
}

func TestHomeModelViewQuitting(t *testing.T) {
	m := NewHomeModel()
	m.quitting = true
	v := m.View()
	if !strings.Contains(v.Content, "Bye") {
		t.Error("should show goodbye")
	}
}

// ════════════════════════════════════════════════════════════════
// bootstrap.go tests
// ════════════════════════════════════════════════════════════════

func TestPickPreferredMatch(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3:latest"},
		{Name: "gemma3:4b"},
		{Name: "phi-4"},
	}
	got := pickPreferred(models, []string{"gemma3", "llama3"})
	if got != "gemma3:4b" {
		t.Errorf("got %q, want gemma3:4b", got)
	}
}

func TestPickPreferredFallback(t *testing.T) {
	models := []ModelInfo{
		{Name: "mistral:7b"},
	}
	got := pickPreferred(models, []string{"gemma3", "llama3"})
	if got != "mistral:7b" {
		t.Errorf("got %q, want mistral:7b (fallback)", got)
	}
}

func TestPickBestModelsWithEmbed(t *testing.T) {
	result := &OnboardResult{}
	models := []ModelInfo{
		{Name: "bge-m3"},
		{Name: "gemma3:4b"},
	}
	pickBestModels(result, models)
	// Should pick bge-m3 for embedding if filterEmbeddingModels returns it.
	// At minimum, should not panic.
	_ = result
}

func TestOllamaReachableOffline(t *testing.T) {
	// Should fail for non-existent host.
	if ollamaReachable("http://127.0.0.1:19999") {
		t.Error("should not be reachable")
	}
}

// ════════════════════════════════════════════════════════════════
// config.go extended tests
// ════════════════════════════════════════════════════════════════

func TestExpandPathTilde(t *testing.T) {
	got := ExpandPath("~")
	home, _ := os.UserHomeDir()
	if got != home {
		t.Errorf("got %q, want %q", got, home)
	}
}

func TestExpandPathTildeSlash(t *testing.T) {
	got := ExpandPath("~/docs")
	home, _ := os.UserHomeDir()
	exp := filepath.Join(home, "docs")
	if got != exp {
		t.Errorf("got %q, want %q", got, exp)
	}
}

func TestExpandPathEmpty(t *testing.T) {
	if ExpandPath("") != "" {
		t.Error("empty should stay empty")
	}
}

func TestExpandPathAbsolute(t *testing.T) {
	got := ExpandPath("/var/log")
	if got != "/var/log" {
		t.Errorf("got %q", got)
	}
}

func TestConfigPathNotEmpty(t *testing.T) {
	p := configPath()
	if p == "" {
		t.Error("config path should not be empty")
	}
	if !strings.Contains(p, ".gleann") {
		t.Errorf("path = %q, should contain .gleann", p)
	}
}

func TestSaveAndLoadConfigRoundTrip(t *testing.T) {
	// Override HOME to use temp dir.
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", origHome)

	cfg := OnboardResult{
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		LLMProvider:       "ollama",
		LLMModel:          "gemma3:4b",
		IndexDir:          filepath.Join(tmp, "indexes"),
		Completed:         true,
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	loaded := LoadSavedConfig()
	if loaded == nil {
		t.Fatal("loaded config is nil")
	}
	if loaded.EmbeddingModel != "bge-m3" {
		t.Errorf("embedding = %q", loaded.EmbeddingModel)
	}
	if loaded.LLMModel != "gemma3:4b" {
		t.Errorf("llm = %q", loaded.LLMModel)
	}
}

func TestUpdateConfigMutate(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", origHome)

	// Save initial config.
	SaveConfig(OnboardResult{EmbeddingModel: "old-model", Completed: true})

	// Update.
	err := UpdateConfig(func(cfg *OnboardResult) {
		cfg.EmbeddingModel = "new-model"
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded := LoadSavedConfig()
	if loaded.EmbeddingModel != "new-model" {
		t.Errorf("model = %q", loaded.EmbeddingModel)
	}
}

func TestDefaultIndexDirNotEmpty(t *testing.T) {
	d := DefaultIndexDir()
	if d == "" {
		t.Error("should not be empty")
	}
	if !strings.Contains(d, "indexes") {
		t.Errorf("got %q", d)
	}
}

func TestDefaultModelsDirNotEmpty(t *testing.T) {
	d := DefaultModelsDir()
	if d == "" {
		t.Error("should not be empty")
	}
	if !strings.Contains(d, "models") {
		t.Errorf("got %q", d)
	}
}

func TestIsSetupNeededNoConfig(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", origHome)

	if !IsSetupNeeded() {
		t.Error("should need setup with no config")
	}
}

func TestIsSetupNeededWithConfig(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", origHome)

	SaveConfig(OnboardResult{Completed: true})

	if IsSetupNeeded() {
		t.Error("should NOT need setup with completed config")
	}
}
