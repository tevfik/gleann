package gleann

import (
	"testing"
	"time"
)

func TestDefaultContextField(t *testing.T) {
	cft := DefaultContextField()
	if cft == nil {
		t.Fatal("DefaultContextField returned nil")
	}
	sum := cft.Alpha + cft.Beta + cft.Gamma + cft.Delta
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("weights should sum to ~1.0, got %f", sum)
	}
}

func TestRecencyScore(t *testing.T) {
	now := time.Now()

	// Just accessed → high score
	recent := recencyScore(now.Add(-1*time.Minute), now)
	if recent < 0.9 {
		t.Errorf("1 minute ago should score > 0.9, got %f", recent)
	}

	// 1 hour ago → ~0.5 (half-life)
	hourAgo := recencyScore(now.Add(-1*time.Hour), now)
	if hourAgo < 0.4 || hourAgo > 0.6 {
		t.Errorf("1 hour ago should score ~0.5, got %f", hourAgo)
	}

	// 24 hours ago → near 0
	dayAgo := recencyScore(now.Add(-24*time.Hour), now)
	if dayAgo > 0.01 {
		t.Errorf("24 hours ago should score near 0, got %f", dayAgo)
	}

	// Zero time → 0
	zero := recencyScore(time.Time{}, now)
	if zero != 0 {
		t.Errorf("zero time should score 0, got %f", zero)
	}
}

func TestFrequencyScore(t *testing.T) {
	// No access → 0
	if frequencyScore(0, 0) != 0.0 {
		t.Error("zero frequency should score 0")
	}

	// Single read
	single := frequencyScore(1, 0)
	if single <= 0 {
		t.Error("single access should be positive")
	}

	// Multiple reads > single
	multi := frequencyScore(10, 0)
	if multi <= single {
		t.Error("more reads should score higher")
	}

	// Edits count 3× more than reads
	withEdits := frequencyScore(0, 3)
	withReads := frequencyScore(3, 0)
	if withEdits <= withReads {
		t.Error("edits should be weighted higher than reads")
	}

	// High frequency caps at 1.0
	high := frequencyScore(100, 50)
	if high > 1.0 {
		t.Errorf("frequency should cap at 1.0, got %f", high)
	}
}

func TestComputeProximity(t *testing.T) {
	tests := []struct {
		active    string
		candidate string
		minScore  float64
		maxScore  float64
	}{
		// Same directory
		{"pkg/gleann/searcher.go", "pkg/gleann/types.go", 0.7, 1.0},
		// Parent directory
		{"pkg/gleann/searcher.go", "pkg/backends/hnsw.go", 0.3, 0.7},
		// Different top-level
		{"pkg/gleann/searcher.go", "internal/mcp/server.go", 0.1, 0.5},
		// Empty paths
		{"", "some/file.go", 0.0, 0.01},
		{"some/file.go", "", 0.0, 0.01},
	}
	for _, tt := range tests {
		score := ComputeProximity(tt.active, tt.candidate)
		if score < tt.minScore || score > tt.maxScore {
			t.Errorf("ComputeProximity(%q, %q) = %f, want [%f, %f]",
				tt.active, tt.candidate, score, tt.minScore, tt.maxScore)
		}
	}
}

func TestScoreItem(t *testing.T) {
	cft := DefaultContextField()
	now := time.Now()

	// High-value item: recently accessed, frequently edited, close to context
	high := cft.ScoreItem(ContextSignal{
		LastAccessed:   now.Add(-5 * time.Minute),
		AccessCount:    20,
		EditCount:      5,
		ProximityScore: 0.9,
		StructureScore: 0.8,
	}, now)

	// Low-value item: old, never accessed, far away
	low := cft.ScoreItem(ContextSignal{
		LastAccessed:   now.Add(-48 * time.Hour),
		AccessCount:    0,
		EditCount:      0,
		ProximityScore: 0.1,
		StructureScore: 0.1,
	}, now)

	if high <= low {
		t.Errorf("high-value item should score higher: high=%f, low=%f", high, low)
	}
}

func TestRankItems(t *testing.T) {
	cft := DefaultContextField()
	now := time.Now()

	signals := []ContextSignal{
		{ID: "low", LastAccessed: now.Add(-24 * time.Hour), AccessCount: 1, ProximityScore: 0.1, StructureScore: 0.1},
		{ID: "high", LastAccessed: now.Add(-1 * time.Minute), AccessCount: 20, EditCount: 5, ProximityScore: 0.9, StructureScore: 0.8},
		{ID: "med", LastAccessed: now.Add(-2 * time.Hour), AccessCount: 5, ProximityScore: 0.5, StructureScore: 0.5},
	}

	results := cft.RankItems(signals)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].ID != "high" {
		t.Errorf("expected 'high' first, got %q", results[0].ID)
	}
	if results[2].ID != "low" {
		t.Errorf("expected 'low' last, got %q", results[2].ID)
	}
	// Scores should be descending
	for i := 1; i < len(results); i++ {
		if results[i].Phi > results[i-1].Phi {
			t.Errorf("results not sorted: [%d].Phi=%f > [%d].Phi=%f", i, results[i].Phi, i-1, results[i-1].Phi)
		}
	}
}

func TestEnrichSearchResults(t *testing.T) {
	cft := DefaultContextField()

	results := []SearchResult{
		{ID: 1, Score: 0.5, Metadata: map[string]any{"source": "pkg/gleann/searcher.go"}},
		{ID: 2, Score: 0.9, Metadata: map[string]any{"source": "internal/mcp/server.go"}},
		{ID: 3, Score: 0.7, Metadata: map[string]any{"source": "pkg/gleann/types.go"}},
	}

	now := time.Now()
	signalMap := map[string]ContextSignal{
		"pkg/gleann/searcher.go": {
			LastAccessed:   now.Add(-1 * time.Minute),
			AccessCount:    30,
			EditCount:      10,
			ProximityScore: 0.9,
			StructureScore: 0.8,
		},
		// server.go has no signal → will use default (semantic only)
		"pkg/gleann/types.go": {
			LastAccessed:   now.Add(-1 * time.Hour),
			AccessCount:    5,
			ProximityScore: 0.7,
			StructureScore: 0.5,
		},
	}

	enriched := cft.EnrichSearchResults(results, signalMap)
	if len(enriched) != 3 {
		t.Fatalf("expected 3 enriched results, got %d", len(enriched))
	}

	// Check phi_score metadata exists
	for _, r := range enriched {
		if _, ok := r.Metadata["phi_score"]; !ok {
			t.Errorf("result %d missing phi_score metadata", r.ID)
		}
	}

	// Verify high context item is boosted
	// searcher.go has lower semantic score (0.5) but very high context signal
	// so it should be boosted in the final ranking
}

func TestEnrichSearchResultsNoSignals(t *testing.T) {
	cft := DefaultContextField()

	results := []SearchResult{
		{ID: 1, Score: 0.9, Metadata: map[string]any{"source": "a.go"}},
		{ID: 2, Score: 0.5, Metadata: map[string]any{"source": "b.go"}},
	}

	// Empty signal map → order preserved (only semantic)
	enriched := cft.EnrichSearchResults(results, map[string]ContextSignal{})
	if len(enriched) != 2 {
		t.Fatalf("expected 2 results, got %d", len(enriched))
	}
	if enriched[0].ID != 1 {
		t.Error("with no signals, higher semantic score should rank first")
	}
}

func TestContextFieldCustomWeights(t *testing.T) {
	// Recency-only model
	cft := &ContextFieldTheory{Alpha: 1.0, Beta: 0, Gamma: 0, Delta: 0}
	now := time.Now()

	recent := cft.ScoreItem(ContextSignal{
		LastAccessed:   now.Add(-1 * time.Minute),
		ProximityScore: 0.0, // doesn't matter
		StructureScore: 0.0,
	}, now)

	old := cft.ScoreItem(ContextSignal{
		LastAccessed:   now.Add(-24 * time.Hour),
		ProximityScore: 1.0, // high proximity but weight=0
		StructureScore: 1.0,
	}, now)

	if recent <= old {
		t.Error("recency-only model should rank recent higher")
	}
}
