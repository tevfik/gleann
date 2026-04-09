// Package memory provides a hierarchical long-term memory system for gleann.
//
// Memory is organized into three tiers:
//   - Short-term (in-memory): Active conversation notes, ephemeral context
//   - Medium-term (BBolt): Daily summaries, conversation digests
//   - Long-term (BBolt): Persistent facts, user preferences, archived knowledge
//
// Inspired by Letta's memory block architecture, adapted for gleann's RAG pipeline.
package memory

import (
	"fmt"
	"time"
)

// Tier represents a memory storage tier.
type Tier string

const (
	TierShort  Tier = "short"  // In-memory, session-scoped
	TierMedium Tier = "medium" // BBolt, daily summaries
	TierLong   Tier = "long"   // BBolt, permanent archive
)

// ParseTier parses a tier string, returning an error for unknown tiers.
func ParseTier(s string) (Tier, error) {
	switch s {
	case "short", "short-term":
		return TierShort, nil
	case "medium", "medium-term":
		return TierMedium, nil
	case "long", "long-term":
		return TierLong, nil
	default:
		return "", fmt.Errorf("unknown memory tier: %q (valid: short, medium, long)", s)
	}
}

// Block represents a single memory entry.
type Block struct {
	ID        string            `json:"id"`       // Unique identifier (UUID-like or content hash)
	Tier      Tier              `json:"tier"`     // Storage tier
	Label     string            `json:"label"`    // Semantic label (e.g. "user_preference", "conversation_summary")
	Content   string            `json:"content"`  // The memory content
	Source    string            `json:"source"`   // Origin: "user", "auto_summary", "system", "sleep_time"
	Tags      []string          `json:"tags"`     // Searchable tags
	Metadata  map[string]string `json:"metadata"` // Arbitrary key-value metadata
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	ExpiresAt *time.Time        `json:"expires_at"` // nil = never expires

	// Character limit for this block's content. 0 = unlimited.
	// When content exceeds this limit, it is truncated or compressed.
	CharLimit int `json:"char_limit,omitempty"`

	// Scope isolates the block to a specific context (e.g. conversation ID,
	// session ID, or a named group). Empty string means global (visible everywhere).
	Scope string `json:"scope,omitempty"`
}

// IsExpired returns true if the block has passed its expiration date.
func (b *Block) IsExpired() bool {
	if b.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*b.ExpiresAt)
}

// ExceedsLimit returns true if the block's content exceeds its character limit.
// Returns false when CharLimit is 0 (unlimited).
func (b *Block) ExceedsLimit() bool {
	if b.CharLimit <= 0 {
		return false
	}
	return len(b.Content) > b.CharLimit
}

// TruncateToLimit trims content to the character limit if exceeded.
// Appends "... [truncated]" when trimming occurs.
func (b *Block) TruncateToLimit() {
	if !b.ExceedsLimit() {
		return
	}
	// Keep the last portion (most recent info is often appended).
	cutoff := b.CharLimit - 15 // room for "... [truncated]"
	if cutoff < 0 {
		cutoff = 0
	}
	b.Content = b.Content[:cutoff] + "... [truncated]"
	b.UpdatedAt = time.Now()
}

// Summary represents a conversation summary stored in medium-term memory.
type Summary struct {
	ConversationID string    `json:"conversation_id"`
	Title          string    `json:"title"`
	Content        string    `json:"content"` // The summary text
	MessageCount   int       `json:"message_count"`
	IndexNames     []string  `json:"index_names"` // Which indexes were used
	Model          string    `json:"model"`       // LLM model used
	CreatedAt      time.Time `json:"created_at"`
}

// Stats holds memory system statistics.
type Stats struct {
	ShortTermCount  int   `json:"short_term_count"`
	MediumTermCount int   `json:"medium_term_count"`
	LongTermCount   int   `json:"long_term_count"`
	TotalCount      int   `json:"total_count"`
	DiskSizeBytes   int64 `json:"disk_size_bytes"`
}

// ContextWindow represents compiled memory for LLM injection.
type ContextWindow struct {
	ShortTerm  []Block   `json:"short_term,omitempty"`
	MediumTerm []Block   `json:"medium_term,omitempty"`
	LongTerm   []Block   `json:"long_term,omitempty"`
	Summaries  []Summary `json:"summaries,omitempty"`
}

// Render compiles the context window into a formatted string for LLM consumption.
func (cw *ContextWindow) Render() string {
	if cw.isEmpty() {
		return ""
	}

	var b []byte

	b = append(b, "<memory_context>\n"...)

	if len(cw.LongTerm) > 0 {
		b = append(b, "<long_term_memory>\n"...)
		for _, block := range cw.LongTerm {
			b = append(b, fmt.Sprintf("[%s] %s\n", block.Label, block.Content)...)
		}
		b = append(b, "</long_term_memory>\n"...)
	}

	if len(cw.MediumTerm) > 0 {
		b = append(b, "<medium_term_memory>\n"...)
		for _, block := range cw.MediumTerm {
			b = append(b, fmt.Sprintf("[%s] %s\n", block.Label, block.Content)...)
		}
		b = append(b, "</medium_term_memory>\n"...)
	}

	if len(cw.Summaries) > 0 {
		b = append(b, "<conversation_summaries>\n"...)
		for _, s := range cw.Summaries {
			b = append(b, fmt.Sprintf("[%s] %s: %s\n", s.CreatedAt.Format("2006-01-02"), s.Title, s.Content)...)
		}
		b = append(b, "</conversation_summaries>\n"...)
	}

	if len(cw.ShortTerm) > 0 {
		b = append(b, "<short_term_memory>\n"...)
		for _, block := range cw.ShortTerm {
			b = append(b, fmt.Sprintf("[%s] %s\n", block.Label, block.Content)...)
		}
		b = append(b, "</short_term_memory>\n"...)
	}

	b = append(b, "</memory_context>"...)
	return string(b)
}

func (cw *ContextWindow) isEmpty() bool {
	return len(cw.ShortTerm) == 0 && len(cw.MediumTerm) == 0 &&
		len(cw.LongTerm) == 0 && len(cw.Summaries) == 0
}
