package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/gleann"
)

func mkIndex(t *testing.T, dir, name, backend string, passages, dims int, indexBytes int) gleann.IndexMeta {
	t.Helper()
	d := filepath.Join(dir, name)
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, name+".index"), make([]byte, indexBytes), 0o644)
	_ = os.WriteFile(filepath.Join(d, name+".passages.db"), make([]byte, 1000), 0o644)
	return gleann.IndexMeta{Name: name, Backend: backend, NumPassages: passages, Dimensions: dims, VectorReady: true}
}

func imKey(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }

func TestIndexManage_StorageBadgeAndSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	small := mkIndex(t, dir, "small", "hnsw", 1000, 768, 50_000) // << 3 MB expected => recompute
	big := mkIndex(t, dir, "big", "hnsw", 1000, 768, 3_200_000)  // >= 70% of 3 MB => store
	da := mkIndex(t, dir, "da", "diskann", 1000, 768, 3_200_000)

	var gotDir, gotName, gotTarget string
	old := storageRunner
	storageRunner = func(d, n, tg string) (string, error) { gotDir, gotName, gotTarget = d, n, tg; return "ok", nil }
	defer func() { storageRunner = old }()

	m := IndexManageModel{indexDir: dir, indexes: []gleann.IndexMeta{small, big, da}, state: imList, width: 120, height: 30, defaultMode: "store"}
	view := m.viewList()
	if !strings.Contains(view, "recompute") || !strings.Contains(view, "vectors stored") {
		t.Fatalf("badges missing:\n%s", view)
	}

	// DiskANN cannot be switched.
	m.cursor = 2
	r, _ := m.Update(imKey("s"))
	if im := r.(IndexManageModel); im.state != imList || !strings.Contains(im.status, "cannot be switched") {
		t.Fatalf("diskann switch should be refused: state=%v status=%q", im.state, im.status)
	}

	// small (recompute) -> confirm -> runs the switch to "store".
	m.cursor = 0
	r, _ = m.Update(imKey("s"))
	im := r.(IndexManageModel)
	if im.state != imStorage || !strings.Contains(im.viewStorage(), "recompute → store") {
		t.Fatalf("confirm screen wrong:\n%s", im.viewStorage())
	}
	r, cmd := im.Update(imKey("y"))
	im = r.(IndexManageModel)
	if im.busy == "" || cmd == nil {
		t.Fatal("expected a busy state and a command")
	}
	msg := cmd()
	if gotDir != dir || gotName != "small" || gotTarget != "store" {
		t.Fatalf("runner args: %q %q %q", gotDir, gotName, gotTarget)
	}
	r, _ = im.Update(msg)
	im = r.(IndexManageModel)
	if im.busy != "" || !strings.Contains(im.status, "now uses store") {
		t.Fatalf("result not applied: busy=%q status=%q", im.busy, im.status)
	}

	// Cancel leaves everything alone.
	m.cursor = 1
	r, _ = m.Update(imKey("s"))
	r, _ = r.(IndexManageModel).Update(imKey("n"))
	if r.(IndexManageModel).state != imList {
		t.Fatal("n should cancel")
	}
}

func TestIndexManage_DefaultModeToggle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := IndexManageModel{indexDir: t.TempDir(), state: imList, defaultMode: "store", indexes: []gleann.IndexMeta{{Name: "x"}}}
	r, _ := m.Update(imKey("g"))
	im := r.(IndexManageModel)
	if im.defaultMode != "recompute" {
		t.Fatalf("mode = %q", im.defaultMode)
	}
	if cfg := LoadSavedConfig(); cfg == nil || cfg.StorageMode != "recompute" {
		t.Fatalf("not persisted: %+v", cfg)
	}
}
