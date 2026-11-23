package tui

import (
	"testing"
)

func TestReviewMenuPreservesSavedDiskANNBackend(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	// Save an existing configuration with backend "diskann"
	initial := OnboardResult{
		Completed:         true,
		EmbeddingProvider: "ollama",
		LLMProvider:       "ollama",
		Backend:           "diskann",
	}
	if err := SaveConfig(initial); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	saved := LoadSavedConfig()
	if saved == nil || saved.Backend != "diskann" {
		t.Fatalf("expected saved backend 'diskann', got %+v", saved)
	}

	m := NewOnboardModelWithConfig(saved)

	// Verify menu pre-selection displays "diskann"
	if got := m.selectedBackend(); got != "diskann" {
		t.Errorf("settings menu shows backend %q for a saved diskann config, want diskann", got)
	}

	// Move cursor to "Save & Exit"
	saveIdx := -1
	for i, item := range m.menuItems {
		if item.phase == phaseSummary {
			saveIdx = i
			break
		}
	}
	if saveIdx == -1 {
		t.Fatal("Save & Exit menu item not found")
	}
	m.menuCursor = saveIdx

	// Press Enter to trigger Save & Exit
	updatedModel, _ := m.handleMenuKeys("enter")
	om := updatedModel.(OnboardModel)

	res := om.Result()
	if res.Backend != "diskann" {
		t.Errorf("Save & Exit result backend = %q, want the saved diskann", res.Backend)
	}

	// Persist the result as tui.runOnboard does
	if err := SaveConfig(res); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	persisted := LoadSavedConfig()
	if persisted == nil || persisted.Backend != "diskann" {
		backend := ""
		if persisted != nil {
			backend = persisted.Backend
		}
		t.Errorf("saved config.json backend after Save & Exit = %q, want diskann (untouched)", backend)
	}
}
