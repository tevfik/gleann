package gleann

import (
	"math"
	"sort"
	"strings"
	"time"
)

// ContextFieldTheory implements Φ (phi) scoring for context relevance ranking.
// Inspired by lean-ctx's multi-factor scoring, it evaluates file/symbol
// relevance using four dimensions:
//
//   Φ(x) = α·Recency(x) + β·Frequency(x) + γ·Proximity(x) + δ·Structure(x)
//
// Each factor is normalised to [0, 1] and weighted by configurable coefficients.
type ContextFieldTheory struct {
	// Alpha weights recency (how recently the file was accessed/modified).
	Alpha float64
	// Beta weights frequency (how often the file appears in searches/edits).
	Beta float64
	// Gamma weights proximity (structural distance from the query context).
	Gamma float64
	// Delta weights structure (graph-derived importance: degree centrality, etc.).
	Delta float64
}

// DefaultContextField returns a ContextFieldTheory with balanced weights.
func DefaultContextField() *ContextFieldTheory {
	return &ContextFieldTheory{
		Alpha: 0.25, // recency
		Beta:  0.25, // frequency
		Gamma: 0.30, // proximity
		Delta: 0.20, // structure
	}
}

// ContextSignal holds the raw signals for a single item (file, symbol, passage).
type ContextSignal struct {
	// ID uniquely identifies the item (e.g., file path or passage ID).
	ID string
	// LastAccessed is the last time this item was read/modified.
	LastAccessed time.Time
	// AccessCount is how many times this item was accessed in the session.
	AccessCount int
	// EditCount is how many times this item was edited.
	EditCount int
	// ProximityScore is a [0,1] score indicating structural closeness to the active context.
	// E.g., same directory = 0.8, imported by active file = 0.9, etc.
	ProximityScore float64
	// StructureScore is a [0,1] score from graph analysis (degree centrality, hub score).
	StructureScore float64
	// SemanticScore is the base semantic similarity score from vector search.
	SemanticScore float64
}

// PhiResult holds a scored item.
type PhiResult struct {
	ID    string  `json:"id"`
	Phi   float64 `json:"phi"`
	Raw   ContextSignal `json:"-"`
}

// ScoreItem computes the Φ score for a single context signal.
func (cft *ContextFieldTheory) ScoreItem(sig ContextSignal, now time.Time) float64 {
	recency := recencyScore(sig.LastAccessed, now)
	frequency := frequencyScore(sig.AccessCount, sig.EditCount)
	proximity := sig.ProximityScore
	structure := sig.StructureScore

	return cft.Alpha*recency + cft.Beta*frequency + cft.Gamma*proximity + cft.Delta*structure
}

// RankItems scores and sorts items by Φ, highest first.
func (cft *ContextFieldTheory) RankItems(signals []ContextSignal) []PhiResult {
	now := time.Now()
	results := make([]PhiResult, len(signals))
	for i, sig := range signals {
		results[i] = PhiResult{
			ID:  sig.ID,
			Phi: cft.ScoreItem(sig, now),
			Raw: sig,
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Phi > results[j].Phi
	})
	return results
}

// EnrichSearchResults re-ranks search results by combining vector/BM25 score
// with context field signals. Returns results sorted by combined score.
func (cft *ContextFieldTheory) EnrichSearchResults(results []SearchResult, signalMap map[string]ContextSignal) []SearchResult {
	now := time.Now()

	type enriched struct {
		result SearchResult
		phi    float64
		combined float64
	}

	items := make([]enriched, len(results))
	for i, r := range results {
		source, _ := r.Metadata["source"].(string)
		sig, ok := signalMap[source]
		if !ok {
			// No context signal → use only semantic score
			sig = ContextSignal{
				SemanticScore:  float64(r.Score),
				ProximityScore: 0,
				StructureScore: 0,
			}
		}

		phi := cft.ScoreItem(sig, now)

		// Combined = 0.6 * semantic + 0.4 * context_field
		combined := 0.6*float64(r.Score) + 0.4*phi

		items[i] = enriched{result: r, phi: phi, combined: combined}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].combined > items[j].combined
	})

	ranked := make([]SearchResult, len(items))
	for i, item := range items {
		item.result.Score = float32(item.combined)
		if item.result.Metadata == nil {
			item.result.Metadata = make(map[string]any)
		}
		item.result.Metadata["phi_score"] = item.phi
		ranked[i] = item.result
	}
	return ranked
}

// recencyScore converts a timestamp to [0,1] using exponential decay.
// Recent items score higher; items older than 24h decay rapidly.
func recencyScore(lastAccessed, now time.Time) float64 {
	if lastAccessed.IsZero() {
		return 0.0
	}
	age := now.Sub(lastAccessed)
	if age < 0 {
		age = 0
	}
	// Half-life of 1 hour
	halfLifeHours := 1.0
	hours := age.Hours()
	return math.Exp(-0.693 * hours / halfLifeHours)
}

// frequencyScore converts access/edit counts to [0,1] using log scaling.
func frequencyScore(accessCount, editCount int) float64 {
	// Edits are 3× more valuable than reads
	combined := float64(accessCount) + 3.0*float64(editCount)
	if combined <= 0 {
		return 0.0
	}
	// log(1+x) / log(1+max) capped at score=1.0 when x=50
	return math.Min(1.0, math.Log1p(combined)/math.Log1p(50))
}

// ComputeProximity calculates proximity score between two file paths.
// Same directory → 0.8, parent directory → 0.5, same project → 0.2, unrelated → 0.0.
func ComputeProximity(activePath, candidatePath string) float64 {
	if activePath == "" || candidatePath == "" {
		return 0.0
	}

	aParts := strings.Split(activePath, "/")
	cParts := strings.Split(candidatePath, "/")

	// Find common prefix length
	common := 0
	maxCommon := len(aParts) - 1 // exclude filename
	if len(cParts)-1 < maxCommon {
		maxCommon = len(cParts) - 1
	}
	for i := 0; i < maxCommon; i++ {
		if i < len(aParts) && i < len(cParts) && aParts[i] == cParts[i] {
			common++
		} else {
			break
		}
	}

	totalDirs := len(aParts) - 1
	if totalDirs <= 0 {
		totalDirs = 1
	}

	// Same directory = same # of common parts as total dirs
	if common >= totalDirs {
		return 0.8
	}
	// Some common path
	ratio := float64(common) / float64(totalDirs)
	return 0.2 + ratio*0.6 // Maps [0,1] → [0.2, 0.8]
}
