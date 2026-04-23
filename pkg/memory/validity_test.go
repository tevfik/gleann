package memory

import (
	"testing"
	"time"
)

func TestValidityScore_Default(t *testing.T) {
	b := &Block{
		ID:        "test-1",
		Tier:      TierLong,
		Content:   "Go uses goroutines for concurrency",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	score := b.ValidityScore()
	if score < 0.3 || score > 0.7 {
		t.Errorf("default block should have moderate score (~0.5), got %f", score)
	}
}

func TestValidityScore_HighConfirmation(t *testing.T) {
	b := &Block{
		ID:        "test-2",
		Tier:      TierLong,
		Content:   "The sky is blue",
		Confirms:  10,
		Conflicts: 0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	score := b.ValidityScore()
	if score < 0.8 {
		t.Errorf("highly confirmed block should have score >= 0.8, got %f", score)
	}
}

func TestValidityScore_HighConflict(t *testing.T) {
	b := &Block{
		ID:        "test-3",
		Tier:      TierLong,
		Content:   "Disputed fact",
		Confirms:  0,
		Conflicts: 10,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	score := b.ValidityScore()
	if score > 0.2 {
		t.Errorf("highly conflicted block should have score < 0.2, got %f", score)
	}
}

func TestValidityScore_StaleDecay(t *testing.T) {
	old := time.Now().Add(-365 * 24 * time.Hour) // 1 year ago
	b := &Block{
		ID:        "test-4",
		Tier:      TierLong,
		Content:   "Old fact",
		CreatedAt: old,
		UpdatedAt: old,
	}

	score := b.ValidityScore()
	if score > 0.2 {
		t.Errorf("stale block (1 year old) should have decayed score < 0.2, got %f", score)
	}
}

func TestValidityScore_RecentAccess(t *testing.T) {
	old := time.Now().Add(-365 * 24 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	b := &Block{
		ID:             "test-5",
		Tier:           TierLong,
		Content:        "Old but recently accessed",
		Confirms:       5,
		CreatedAt:      old,
		UpdatedAt:      old,
		LastAccessedAt: &recent,
		AccessCount:    10,
	}

	score := b.ValidityScore()
	if score < 0.6 {
		t.Errorf("recently accessed block should have higher score, got %f", score)
	}
}

func TestValidityLabel(t *testing.T) {
	tests := []struct {
		name      string
		confirms  int
		conflicts int
		want      string
	}{
		{"high", 20, 0, "high"},
		{"medium", 3, 2, "medium"},
		{"low_conflict", 1, 5, "low"},
		{"unreliable", 0, 10, "unreliable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Block{
				ID:        tt.name,
				Confirms:  tt.confirms,
				Conflicts: tt.conflicts,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			got := b.ValidityLabel()
			if got != tt.want {
				t.Errorf("ValidityLabel() = %q, want %q (score: %f)", got, tt.want, b.ValidityScore())
			}
		})
	}
}

func TestIsStale(t *testing.T) {
	// Short-term: stale after 1 day.
	b := &Block{
		Tier:      TierShort,
		UpdatedAt: time.Now().Add(-2 * 24 * time.Hour),
	}
	if !b.IsStale() {
		t.Error("short-term block 2 days old should be stale")
	}

	// Long-term: not stale within 6 months.
	b2 := &Block{
		Tier:      TierLong,
		UpdatedAt: time.Now().Add(-30 * 24 * time.Hour),
	}
	if b2.IsStale() {
		t.Error("long-term block 30 days old should not be stale")
	}
}

func TestContradictionCheck(t *testing.T) {
	b := &Block{
		Content: "The deployment should use Docker containers",
	}

	// No contradiction.
	if b.ContradictionCheck("Docker is a containerization tool") {
		t.Error("unrelated content should not be flagged as contradiction")
	}

	// Potential contradiction.
	if !b.ContradictionCheck("The deployment should not use Docker containers") {
		t.Error("negated statement should be flagged as contradiction")
	}
}

func TestContradictionCheck_EnableDisable(t *testing.T) {
	b := &Block{
		Content: "Enable logging for all services",
	}

	if !b.ContradictionCheck("Disable logging for all production services") {
		t.Error("enable/disable with shared topic should be flagged")
	}
}

func TestConfirmAndConflict(t *testing.T) {
	b := &Block{
		ID:        "test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	b.Confirm()
	if b.Confirms != 1 {
		t.Errorf("Commits = %d, want 1", b.Confirms)
	}

	b.Conflict()
	if b.Conflicts != 1 {
		t.Errorf("Conflicts = %d, want 1", b.Conflicts)
	}
}

func TestRecordAccess(t *testing.T) {
	b := &Block{
		ID:        "test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	b.RecordAccess()
	if b.AccessCount != 1 {
		t.Errorf("AccessCount = %d, want 1", b.AccessCount)
	}
	if b.LastAccessedAt == nil {
		t.Error("LastAccessedAt should be set")
	}
}
