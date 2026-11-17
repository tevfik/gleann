package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestH2bIndexListWithoutIndexesEnterSkips(t *testing.T) {
	m := NewIndexListModel(t.TempDir())
	r, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	il := r.(IndexListModel)
	if il.width != 90 || il.height != 30 {
		t.Errorf("size = %dx%d", il.width, il.height)
	}
	r, cmd := il.Update(h2bKey("enter"))
	il = r.(IndexListModel)
	if !il.Skipped() || il.Selected() != "" || il.Quitting() || cmd == nil {
		t.Errorf("enter with no index continues without one: skipped=%v selected=%q", il.Skipped(), il.Selected())
	}
}

func TestH2bIndexListNoIndexRowIsHighlighted(t *testing.T) {
	m := IndexListModel{indexes: []gleann.IndexMeta{{Name: "docs", Backend: "hnsw"}}}
	m = h2bPress(m, "down").(IndexListModel)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d", m.cursor)
	}
	if v := m.View().Content; !strings.Contains(v, "▸ ") || !strings.Contains(v, "Continue without index") {
		t.Errorf("view:\n%s", v)
	}
	// The cursor sits on the "no index" row, not on docs.
	for _, line := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(line, "docs") && strings.Contains(line, "▸") {
			t.Errorf("docs still highlighted: %q", line)
		}
	}
	m = h2bPress(m, "enter").(IndexListModel)
	if !m.Skipped() || m.Selected() != "" {
		t.Error("enter on the no-index row skips")
	}
}
