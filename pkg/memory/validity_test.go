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
	// Age decay applies to the short and medium tiers only; the long tier
	// is exempt (see TestValidityScore_LongTierNoAgeDecay).
	old := time.Now().Add(-365 * 24 * time.Hour) // 1 year ago
	for _, tier := range []Tier{TierShort, TierMedium} {
		t.Run(string(tier), func(t *testing.T) {
			b := &Block{
				ID:        "test-4",
				Tier:      tier,
				Content:   "Old fact",
				CreatedAt: old,
				UpdatedAt: old,
			}

			score := b.ValidityScore()
			if score > 0.2 {
				t.Errorf("stale %s block (1 year old) should have decayed score < 0.2, got %f", tier, score)
			}
		})
	}
}

// TestValidityScore_LongTierNoAgeDecay pins the maintainer decision
// (2026-11-21): long-term facts never lose validity by age alone. An
// unconfirmed, never-accessed TierLong block keeps its 0.5 prior at any
// age, while confirmations raise and conflicts lower the score exactly
// as before — the long tier stays changeable, not immortal.
func TestValidityScore_LongTierNoAgeDecay(t *testing.T) {
	ages := []struct {
		name string
		days int
	}{
		{"130_days", 130},
		{"365_days", 365},
		{"1000_days", 1000},
		{"10_years", 3650},
	}
	for _, age := range ages {
		t.Run(age.name, func(t *testing.T) {
			old := time.Now().Add(-time.Duration(age.days) * 24 * time.Hour)
			b := &Block{
				ID:        "long-" + age.name,
				Tier:      TierLong,
				Content:   "Persistent fact",
				CreatedAt: old,
				UpdatedAt: old,
			}

			// 0 confirms / 0 conflicts -> the 0.5 prior, at any age.
			score := b.ValidityScore()
			if score < 0.5 {
				t.Errorf("unconfirmed TierLong block %d days old should keep score >= 0.5, got %f", age.days, score)
			}
			if score < 0.2 {
				t.Errorf("TierLong block %d days old must stay above the 0.2 filter threshold, got %f", age.days, score)
			}
			if b.IsStale() {
				t.Errorf("TierLong block %d days old must not be stale by age", age.days)
			}

			// Confirmations still raise the score.
			confirmed := *b
			confirmed.Confirms = 5
			if confirmed.ValidityScore() <= score {
				t.Errorf("confirmed TierLong block should score higher than %f, got %f", score, confirmed.ValidityScore())
			}

			// Heavy conflict still drops the score below 0.2, so a
			// contradicted long-term fact can be pruned — on conflict,
			// never on age.
			conflicted := *b
			conflicted.Conflicts = 10
			if cs := conflicted.ValidityScore(); cs >= 0.2 {
				t.Errorf("heavily conflicted TierLong block should score < 0.2, got %f", cs)
			}
		})
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
