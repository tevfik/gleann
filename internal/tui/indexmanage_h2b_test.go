package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/tevfik/gleann/pkg/gleann"
)

// h2bBuildIndexes builds small lexical-only indexes (no embedder needed)
// that gleann.ListIndexes finds.
func h2bBuildIndexes(t *testing.T, dir string, names ...string) {
	t.Helper()
	b, err := gleann.NewBuilder(gleann.Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := b.BuildLexicalOnly(n, []gleann.Item{{Text: "text of " + n, Metadata: map[string]any{"source": n + ".md"}}}); err != nil {
			t.Fatalf("build %s: %v", n, err)
		}
	}
}

func h2bNames(idx []gleann.IndexMeta) string {
	var n []string
	for _, i := range idx {
		n = append(n, i.Name)
	}
	return strings.Join(n, ",")
}

func TestH2bFmtBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KB"},
		{5 << 20, "5.0 MB"},
		{3 << 30, "3.00 GB"},
	} {
		if got := fmtBytes(tc.n); got != tc.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestH2bIndexManageDeleteLastIndexMovesTheCursor(t *testing.T) {
	h2bHome(t)
	dir := t.TempDir()
	h2bBuildIndexes(t, dir, "alpha", "beta")

	m := NewIndexManageModel(dir)
	if m.err != nil || h2bNames(m.indexes) != "alpha,beta" {
		t.Fatalf("indexes = %s, err = %v", h2bNames(m.indexes), m.err)
	}
	r := h2bPress(m, "down", "enter", "d") // detail of beta → confirm
	im := r.(IndexManageModel)
	if im.state != imConfirm || !strings.Contains(im.View().Content, `Delete "beta"?`) {
		t.Fatalf("confirm view:\n%s", im.View().Content)
	}
	r, cmd := im.Update(h2bKey("y"))
	if cmd == nil {
		t.Fatal("y must start the delete")
	}
	msg := cmd().(deleteResultMsg)
	if msg.err != nil || msg.name != "beta" {
		t.Fatalf("delete result %+v", msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "beta")); !os.IsNotExist(err) {
		t.Errorf("index directory still there: %v", err)
	}
	r, _ = r.Update(msg)
	im = r.(IndexManageModel)
	if h2bNames(im.indexes) != "alpha" || im.cursor != 0 || im.state != imList {
		t.Errorf("after delete: indexes=%s cursor=%d state=%v", h2bNames(im.indexes), im.cursor, im.state)
	}
}

func TestH2bIndexManageRefreshClampsTheCursor(t *testing.T) {
	h2bHome(t)
	dir := t.TempDir()
	h2bBuildIndexes(t, dir, "alpha", "beta")
	m := NewIndexManageModel(dir)
	m.cursor = 1
	if err := gleann.RemoveIndex(dir, "beta"); err != nil {
		t.Fatal(err)
	}
	im := h2bPress(m, "r").(IndexManageModel)
	if h2bNames(im.indexes) != "alpha" || im.cursor != 0 || im.status != "↻ Refreshed" {
		t.Errorf("refresh: indexes=%s cursor=%d status=%q", h2bNames(im.indexes), im.cursor, im.status)
	}
}

func TestH2bIndexManageUsesTheSavedDefaultMode(t *testing.T) {
	h2bHome(t)
	if err := SaveConfig(OnboardResult{StorageMode: gleann.StorageRecompute}); err != nil {
		t.Fatal(err)
	}
	m := NewIndexManageModel(t.TempDir())
	if m.defaultMode != gleann.StorageRecompute {
		t.Errorf("default mode = %q", m.defaultMode)
	}
}

func TestH2bIndexManageDefaultModeSaveFailure(t *testing.T) {
	home := h2bHome(t)
	if err := os.WriteFile(filepath.Join(home, ".gleann"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := IndexManageModel{indexDir: t.TempDir(), defaultMode: gleann.StorageStore, indexes: []gleann.IndexMeta{{Name: "x"}}}
	im := h2bPress(m, "g").(IndexManageModel)
	if !strings.HasPrefix(im.status, "⚠ Could not save default") {
		t.Fatalf("status = %q", im.status)
	}
	// Errors are drawn in the error colour above the list.
	if !strings.Contains(im.viewList(), "Could not save default") {
		t.Errorf("list view lacks the error:\n%s", im.viewList())
	}
}

func TestH2bIndexManageStorageSwitchFromDetail(t *testing.T) {
	h2bHome(t)
	dir := t.TempDir()
	stored := mkIndex(t, dir, "stored", "hnsw", 1000, 768, 3_200_000)
	runs := 0
	old := storageRunner
	storageRunner = func(d, n, tg string) (string, error) {
		runs++
		return "rebuilding\nError: disk full", errors.New("exit status 1")
	}
	t.Cleanup(func() { storageRunner = old })

	m := IndexManageModel{indexDir: dir, indexes: []gleann.IndexMeta{stored}, state: imDetail}
	r, _ := m.Update(h2bKey("s"))
	im := r.(IndexManageModel)
	if im.state != imStorage {
		t.Fatalf("s in the detail view opens the switch confirmation, state=%v", im.state)
	}
	view := im.View().Content
	if !strings.Contains(view, "store → recompute") || !strings.Contains(view, "shrinks") {
		t.Errorf("storage view:\n%s", view)
	}

	r, cmd := im.Update(h2bKey("y"))
	im = r.(IndexManageModel)
	if !strings.Contains(im.View().Content, "please wait") {
		t.Errorf("busy view:\n%s", im.View().Content)
	}
	// Keys other than ctrl+c are ignored while converting.
	if r2, c2 := im.Update(h2bKey("q")); c2 != nil || r2.(IndexManageModel).quitting {
		t.Error("q while busy must be ignored")
	}
	if r2, c2 := im.Update(h2bKey("ctrl+c")); c2 == nil || !r2.(IndexManageModel).quitting {
		t.Error("ctrl+c while busy must quit")
	}

	r, _ = im.Update(cmd())
	im = r.(IndexManageModel)
	if runs != 1 || im.busy != "" || im.state != imList {
		t.Fatalf("runs=%d busy=%q state=%v", runs, im.busy, im.state)
	}
	if want := `⚠ Could not switch "stored" to recompute: Error: disk full`; im.status != want {
		t.Errorf("status = %q, want %q", im.status, want)
	}
}

func TestH2bIndexManageStorageConfirmCtrlC(t *testing.T) {
	m := IndexManageModel{state: imStorage, indexes: []gleann.IndexMeta{{Name: "x"}}}
	r, cmd := m.Update(h2bKey("ctrl+c"))
	if cmd == nil || !r.(IndexManageModel).quitting {
		t.Error("ctrl+c on the storage confirmation must quit")
	}
}

func TestH2bIndexManageViewsWithoutASelection(t *testing.T) {
	m := IndexManageModel{indexDir: t.TempDir(), cursor: 3, indexes: []gleann.IndexMeta{{Name: "only"}}}
	for name, view := range map[string]string{
		"detail": m.viewDetail(), "confirm": m.viewConfirm(), "storage": m.viewStorage(),
	} {
		if view != "" {
			t.Errorf("%s view with the cursor past the list = %q, want empty", name, view)
		}
	}
	m.state = imDetail
	r, cmd := m.Update(h2bKey("s"))
	if cmd != nil || r.(IndexManageModel).state != imDetail {
		t.Error("a storage switch without a selected index must do nothing")
	}
}

func TestH2bIndexManageNarrowTerminalPanels(t *testing.T) {
	dir := t.TempDir()
	idx := mkIndex(t, dir, "recomp", "hnsw", 1000, 768, 50_000)
	m := IndexManageModel{indexDir: dir, indexes: []gleann.IndexMeta{idx}, width: 30}
	for name, view := range map[string]string{
		"detail": m.viewDetail(), "confirm": m.viewConfirm(), "storage": m.viewStorage(),
	} {
		for _, line := range strings.Split(view, "\n") {
			// The bordered panel shrinks to width-10 (plus margin and border).
			if !strings.ContainsAny(line, "│╭╰") {
				continue
			}
			if w := lipgloss.Width(line); w > 30 {
				t.Errorf("%s view line wider than the terminal (%d): %q", name, w, line)
				break
			}
		}
	}
	m.width = 0
	if !strings.Contains(m.viewStorage(), "recompute → store") || !strings.Contains(m.viewStorage(), "Search becomes fast") {
		t.Errorf("storage view:\n%s", m.viewStorage())
	}
}

func TestH2bIndexManageUnknownStorageModeHasNoBadge(t *testing.T) {
	dir := t.TempDir()
	// Vectors present but no dimensions recorded: the mode cannot be told.
	idx := mkIndex(t, dir, "odd", "hnsw", 10, 0, 100)
	m := IndexManageModel{indexDir: dir}
	if got := m.storageBadge(idx); got != "" {
		t.Errorf("badge = %q, want none", got)
	}
}

func TestH2bIndexManageIgnoresUnknownMessages(t *testing.T) {
	m := IndexManageModel{state: imDetail, indexes: []gleann.IndexMeta{{Name: "x"}}}
	r, cmd := m.Update(settingsDownloadedMsg{})
	if cmd != nil || r.(IndexManageModel).state != imDetail {
		t.Error("a message for another screen must change nothing")
	}
}
