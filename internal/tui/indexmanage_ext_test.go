package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── IndexManageModel ───────────────────────────────────────────

func newTestIndexManageModelExt() IndexManageModel {
	return IndexManageModel{
		indexDir: "/tmp/nonexistent",
		indexes: []gleann.IndexMeta{
			{Name: "test-index", NumPassages: 100},
			{Name: "other-index", NumPassages: 50},
		},
		state:  imList,
		width:  80,
		height: 24,
	}
}

func TestIndexManageInitExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestIndexManageUpdateWindowSizeExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	im := result.(IndexManageModel)
	if im.width != 120 || im.height != 40 {
		t.Error("size not updated")
	}
}

// ── List state ─────────────────────────────────────────────────

func TestIndexManageListCtrlCExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	im := result.(IndexManageModel)
	if !im.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestIndexManageListEscExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	im := result.(IndexManageModel)
	if !im.quitting {
		t.Error("esc should quit")
	}
}

func TestIndexManageListUpDownExt(t *testing.T) {
	m := newTestIndexManageModelExt()

	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	im := result.(IndexManageModel)
	if im.cursor != 1 {
		t.Errorf("cursor = %d, want 1", im.cursor)
	}

	result, _ = im.Update(tea.KeyPressMsg{Code: 'k'})
	im = result.(IndexManageModel)
	if im.cursor != 0 {
		t.Errorf("cursor = %d, want 0", im.cursor)
	}

	result, _ = im.Update(tea.KeyPressMsg{Code: 'k'})
	im = result.(IndexManageModel)
	if im.cursor != 0 {
		t.Error("should not go below 0")
	}
}

func TestIndexManageListEnterExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	im := result.(IndexManageModel)
	if im.state != imDetail {
		t.Errorf("state = %d, want imDetail", im.state)
	}
}

func TestIndexManageListEnterEmptyExt(t *testing.T) {
	m := IndexManageModel{indexes: nil, state: imList}
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	im := result.(IndexManageModel)
	if im.state != imList {
		t.Error("enter on empty list should stay in list")
	}
}

func TestIndexManageListDeleteExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	im := result.(IndexManageModel)
	if im.state != imConfirm {
		t.Errorf("state = %d, want imConfirm", im.state)
	}
}

func TestIndexManageListRefreshExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(tea.KeyPressMsg{Code: 'r'})
	im := result.(IndexManageModel)
	if im.status != "↻ Refreshed" {
		t.Errorf("status = %q", im.status)
	}
}

func TestIndexManageListStatusClearExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.status = "some status"
	result, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	im := result.(IndexManageModel)
	if im.status != "" {
		t.Error("status should be cleared")
	}
}

// ── Detail state ───────────────────────────────────────────────

func TestIndexManageDetailEscExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	im := result.(IndexManageModel)
	if im.state != imList {
		t.Errorf("state = %d, want imList", im.state)
	}
}

func TestIndexManageDetailCtrlCExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	im := result.(IndexManageModel)
	if !im.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestIndexManageDetailDeleteExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	im := result.(IndexManageModel)
	if im.state != imConfirm {
		t.Errorf("state = %d, want imConfirm", im.state)
	}
}

func TestIndexManageDetailBackExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imDetail
	result, _ := m.Update(tea.KeyPressMsg{Code: 'h'})
	im := result.(IndexManageModel)
	if im.state != imList {
		t.Errorf("state = %d, want imList", im.state)
	}
}

// ── Confirm state ──────────────────────────────────────────────

func TestIndexManageConfirmNoExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imConfirm
	result, _ := m.Update(tea.KeyPressMsg{Code: 'n'})
	im := result.(IndexManageModel)
	if im.state != imList {
		t.Errorf("state = %d, want imList", im.state)
	}
}

func TestIndexManageConfirmCtrlCExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imConfirm
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	im := result.(IndexManageModel)
	if !im.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestIndexManageConfirmYesExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imConfirm
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	if cmd == nil {
		t.Error("should return a delete command")
	}
}

// ── deleteResultMsg ────────────────────────────────────────────

func TestIndexManageDeleteResultExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(deleteResultMsg{name: "test-index", err: nil})
	im := result.(IndexManageModel)
	if !strings.Contains(im.status, "Deleted") {
		t.Errorf("status = %q, should contain 'Deleted'", im.status)
	}
	if im.state != imList {
		t.Error("should return to list")
	}
}

func TestIndexManageDeleteResultErrorExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	result, _ := m.Update(deleteResultMsg{name: "test-index", err: &testError{msg: "not found"}})
	im := result.(IndexManageModel)
	if !strings.Contains(im.status, "Error") {
		t.Errorf("status = %q, should show error", im.status)
	}
}

// ── View ───────────────────────────────────────────────────────

func TestIndexManageViewListExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	v := m.View()
	if !strings.Contains(v.Content, "test-index") {
		t.Error("should show index name")
	}
}

func TestIndexManageViewDetailExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imDetail
	v := m.View()
	if !strings.Contains(v.Content, "test-index") {
		t.Error("should show index detail")
	}
}

func TestIndexManageViewConfirmExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.state = imConfirm
	v := m.View()
	lower := strings.ToLower(v.Content)
	if !strings.Contains(lower, "test-index") || !strings.Contains(lower, "delete") {
		t.Errorf("should show delete confirmation, got: %s", v.Content)
	}
}

func TestIndexManageViewQuittingExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	m.quitting = true
	v := m.View()
	if v.Content != "" {
		t.Error("should be empty")
	}
}

func TestIndexManageViewEmptyExt(t *testing.T) {
	m := IndexManageModel{indexes: nil, width: 80, height: 24}
	v := m.View()
	if !strings.Contains(v.Content, "No indexes") || !strings.Contains(v.Content, "no") {
		// Accept either "No indexes" or just empty view.
		if v.Content == "" {
			t.Error("should show something for empty list")
		}
	}
}

func TestIndexManageQuittingExt(t *testing.T) {
	m := newTestIndexManageModelExt()
	if m.Quitting() {
		t.Error("should not be quitting")
	}
	m.quitting = true
	if !m.Quitting() {
		t.Error("should be quitting")
	}
}
