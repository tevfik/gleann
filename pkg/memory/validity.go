package memory

import (
	"math"
	"strings"
	"time"
)

// ValidityScore computes a Bayesian-inspired confidence score for a memory block.
// Returns a value between 0.0 (unreliable) and 1.0 (highly reliable).
//
// The score combines:
//   - Confirmation ratio: confirms / (confirms + conflicts)
//   - Staleness decay: exponential decay based on age and access recency
//   - Access frequency: blocks used more often are scored higher
func (b *Block) ValidityScore() float64 {
	// Prior: start with 50% confidence (uninformative prior).
	const priorAlpha = 1.0 // pseudo-confirm
	const priorBeta = 1.0  // pseudo-conflict

	alpha := priorAlpha + float64(b.Confirms)
	beta := priorBeta + float64(b.Conflicts)

	// Bayesian mean of Beta(alpha, beta).
	confirmScore := alpha / (alpha + beta)

	// Staleness decay: halve confidence every 90 days without access.
	const halfLifeDays = 90.0
	age := time.Since(b.UpdatedAt)
	if b.LastAccessedAt != nil && b.LastAccessedAt.After(b.UpdatedAt) {
		age = time.Since(*b.LastAccessedAt)
	}
	decayFactor := math.Pow(0.5, age.Hours()/(halfLifeDays*24.0))

	// Access frequency boost (diminishing returns).
	accessBoost := 1.0
	if b.AccessCount > 0 {
		accessBoost = 1.0 + 0.1*math.Log1p(float64(b.AccessCount))
	}

	score := confirmScore * decayFactor * accessBoost
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// ValidityLabel returns a human-readable label for the block's validity score.
func (b *Block) ValidityLabel() string {
	score := b.ValidityScore()
	switch {
	case score >= 0.8:
		return "high"
	case score >= 0.5:
		return "medium"
	case score >= 0.2:
		return "low"
	default:
		return "unreliable"
	}
}

// IsStale returns true if the block hasn't been accessed or updated recently.
// The staleness threshold depends on the tier.
func (b *Block) IsStale() bool {
	var threshold time.Duration
	switch b.Tier {
	case TierShort:
		threshold = 24 * time.Hour // 1 day
	case TierMedium:
		threshold = 30 * 24 * time.Hour // 30 days
	case TierLong:
		threshold = 180 * 24 * time.Hour // 6 months
	default:
		threshold = 90 * 24 * time.Hour
	}

	ref := b.UpdatedAt
	if b.LastAccessedAt != nil && b.LastAccessedAt.After(ref) {
		ref = *b.LastAccessedAt
	}
	return time.Since(ref) > threshold
}

// Confirm marks this block as confirmed by new evidence.
func (b *Block) Confirm() {
	b.Confirms++
	b.UpdatedAt = time.Now()
}

// Conflict marks this block as conflicting with new information.
func (b *Block) Conflict() {
	b.Conflicts++
	b.UpdatedAt = time.Now()
}

// RecordAccess updates the access tracking for this block.
func (b *Block) RecordAccess() {
	b.AccessCount++
	now := time.Now()
	b.LastAccessedAt = &now
}

// ContradictionCheck performs a simple text-similarity check for potential
// contradictions between this block and new content.
// Returns true if the new content likely contradicts this block.
//
// This is a heuristic: it checks for negation patterns and opposing keywords.
// For LLM-powered contradiction detection, use the manager's CheckContradiction method.
func (b *Block) ContradictionCheck(newContent string) bool {
	oldLower := strings.ToLower(b.Content)
	newLower := strings.ToLower(newContent)

	// Check for explicit negation of the same topic.
	negationPairs := [][2]string{
		{"is not", "is"},
		{"should not", "should"},
		{"does not", "does"},
		{"cannot", "can"},
		{"never", "always"},
		{"disable", "enable"},
		{"false", "true"},
		{"deprecated", "recommended"},
		{"removed", "added"},
		{"don't", "do"},
	}

	for _, pair := range negationPairs {
		neg, pos := pair[0], pair[1]
		// If old says positive and new says negative (or vice versa) about similar content.
		if (strings.Contains(oldLower, pos) && strings.Contains(newLower, neg)) ||
			(strings.Contains(oldLower, neg) && strings.Contains(newLower, pos)) {
			// Check if they share at least one significant word (>4 chars).
			oldWords := strings.Fields(oldLower)
			newWords := strings.Fields(newLower)
			for _, ow := range oldWords {
				if len(ow) <= 4 {
					continue
				}
				for _, nw := range newWords {
					if ow == nw {
						return true
					}
				}
			}
		}
	}
	return false
}
