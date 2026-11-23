// Package memory provides a hierarchical long-term memory system for gleann.
//
// Memory is organized into three tiers:
//   - Short-term (in-memory): Active conversation notes, ephemeral context
//   - Medium-term (SQLite): Daily summaries, conversation digests
//   - Long-term (SQLite): Persistent facts, user preferences, archived knowledge
//
// Inspired by Letta's memory block architecture, adapted for gleann's RAG pipeline.
package memory

import (
	"fmt"
	"strings"
	"time"
)

// Tier represents a memory storage tier.
type Tier string

const (
	TierShort  Tier = "short"  // In-memory, session-scoped
	TierMedium Tier = "medium" // SQLite, daily summaries
	TierLong   Tier = "long"   // SQLite, permanent archive
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

	// Provenance fields linking this memory to specific code entities.
	Repo        string   `json:"repo,omitempty"`         // Repository identifier (e.g. "github.com/tevfik/gleann")
	Paths       []string `json:"paths,omitempty"`        // Relative file paths related to this memory
	Symbols     []string `json:"symbols,omitempty"`      // FQN symbol names related to this memory
	Commit      string   `json:"commit,omitempty"`       // Git commit hash when memory was recorded
	Suspect     bool     `json:"suspect,omitempty"`      // True if underlying code has changed or fact is suspect
	StaleReason string   `json:"stale_reason,omitempty"` // Reason for suspicion/staleness

	// Validity scoring — Bayesian-inspired confidence tracking.
	// Confirms counts how many times this fact was reinforced by new information.
	Confirms int `json:"confirms,omitempty"`
	// Conflicts counts how many times new information contradicted this block.
	Conflicts int `json:"conflicts,omitempty"`
	// AccessCount tracks how many times this block was retrieved/used.
	AccessCount int `json:"access_count,omitempty"`
	// LastAccessedAt tracks when this block was last retrieved.
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
}

// IsExpired returns true if the block has passed its explicit expiration date.
// A nil ExpiresAt never expires. An explicitly set ExpiresAt is honored in
// every tier: long-term facts do not expire by age, but a caller-set expiry
// is deliberate intent, not age.
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

	// Failures holds the failure memories surfaced for the current task
	// (see Manager.BuildTaskContext). They render in their own short
	// section and count against the injection budget like long-term blocks.
	Failures []Block `json:"failures,omitempty"`

	// Budget reports how the configured token budget shaped this window
	// (what was injected and what was dropped). Nil for a hand-built window.
	Budget *ContextBudgetReport `json:"budget,omitempty"`
}

// Render compiles the context window into a formatted string for LLM consumption.
func (cw *ContextWindow) Render() string {
	if cw.isEmpty() {
		return ""
	}

	var b []byte
	b = append(b, "<memory_context>\n"...)

	var suspectBlocks []Block

	renderTier := func(blocks []Block, tag string) {
		var active []Block
		for _, blk := range blocks {
			if blk.Suspect {
				suspectBlocks = append(suspectBlocks, blk)
			} else {
				active = append(active, blk)
			}
		}
		if len(active) > 0 {
			b = append(b, fmt.Sprintf("<%s>\n", tag)...)
			for _, blk := range active {
				b = append(b, renderBlockLine(blk)...)
			}
			b = append(b, fmt.Sprintf("</%s>\n", tag)...)
		}
	}

	renderTier(cw.LongTerm, "long_term_memory")
	renderTier(cw.Failures, "failure_memory")
	renderTier(cw.MediumTerm, "medium_term_memory")

	if len(cw.Summaries) > 0 {
		b = append(b, "<conversation_summaries>\n"...)
		for _, s := range cw.Summaries {
			b = append(b, fmt.Sprintf("[%s] %s: %s\n", s.CreatedAt.Format("2006-01-02"), s.Title, s.Content)...)
		}
		b = append(b, "</conversation_summaries>\n"...)
	}

	renderTier(cw.ShortTerm, "short_term_memory")

	if len(suspectBlocks) > 0 {
		b = append(b, "<suspect_memory>\n<!-- Warning: The following memories may be stale due to recent code changes. Verify against active code! -->\n"...)
		for _, sb := range suspectBlocks {
			b = append(b, renderBlockLine(sb)...)
		}
		b = append(b, "</suspect_memory>\n"...)
	}

	b = append(b, "</memory_context>"...)
	return string(b)
}

func renderBlockLine(block Block) string {
	if block.Label == FailureLabel {
		return renderFailureLine(block)
	}
	prefix := fmt.Sprintf("[%s]", block.Label)
	if block.Suspect {
		reason := block.StaleReason
		if reason == "" {
			reason = "code changed"
		}
		prefix += fmt.Sprintf(" [⚠️ SUSPECT: %s]", reason)
	}
	prov := ""
	if len(block.Symbols) > 0 {
		prov += fmt.Sprintf(" (symbols: %s)", strings.Join(block.Symbols, ", "))
	} else if len(block.Paths) > 0 {
		prov += fmt.Sprintf(" (files: %s)", strings.Join(block.Paths, ", "))
	}
	return fmt.Sprintf("%s %s%s\n", prefix, block.Content, prov)
}

// renderFailureLine renders one failure block as a single line (the failure
// section of the context window), keeping the command, error and fix in one
// compact unit so the budget's token estimate matches the rendered text.
func renderFailureLine(block Block) string {
	cmd := block.Metadata[FailureMetaCommand]
	fix := block.Metadata[FailureMetaFix]
	line := fmt.Sprintf("[%s] %s", FailureLabel, cmd)
	if block.Content != "" {
		line += fmt.Sprintf(" — %s", block.Content)
	}
	if fix != "" {
		line += fmt.Sprintf(" (fix: %s)", fix)
	}
	if len(block.Symbols) > 0 {
		line += fmt.Sprintf(" (symbols: %s)", strings.Join(block.Symbols, ", "))
	} else if len(block.Paths) > 0 {
		line += fmt.Sprintf(" (files: %s)", strings.Join(block.Paths, ", "))
	}
	return line + "\n"
}

func (cw *ContextWindow) isEmpty() bool {
	return len(cw.ShortTerm) == 0 && len(cw.MediumTerm) == 0 &&
		len(cw.LongTerm) == 0 && len(cw.Summaries) == 0 && len(cw.Failures) == 0
}
