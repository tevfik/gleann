package memory

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// Manager provides a high-level interface to the memory system with
// automatic lifecycle management (promotion, cleanup, expiration).
type Manager struct {
	store *Store
	mu    sync.RWMutex

	// Configuration.
	ShortTermTTL     time.Duration // Default TTL for short-term blocks (0 = session only)
	MediumTermMaxAge time.Duration // Auto-archive medium-term blocks older than this
	AutoPromote      bool          // Auto-promote short-term blocks on session end
	AutoCleanup      bool          // Auto-cleanup expired blocks

	// Character limit defaults (Letta-inspired).
	DefaultCharLimit int  // Default char limit for new blocks (0 = unlimited)
	EnforceCharLimit bool // Whether to truncate blocks that exceed their limit on Add
}

// NewManager creates a new memory manager wrapping the given store.
func NewManager(store *Store) *Manager {
	mgr := &Manager{
		store:            store,
		ShortTermTTL:     0,
		MediumTermMaxAge: 30 * 24 * time.Hour, // 30 days default
		AutoPromote:      true,
		AutoCleanup:      true,
		DefaultCharLimit: 0,
		EnforceCharLimit: false,
	}

	// Read char limit from env.
	if v := os.Getenv("GLEANN_BLOCK_CHAR_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mgr.DefaultCharLimit = n
			mgr.EnforceCharLimit = true
		}
	}

	return mgr
}

// DefaultManager opens the default store and returns a manager.
func DefaultManager() (*Manager, error) {
	store, err := DefaultStore()
	if err != nil {
		return nil, err
	}
	return NewManager(store), nil
}

// Close closes the underlying store.
func (m *Manager) Close() error {
	return m.store.Close()
}

// Store returns the underlying Store for direct access.
func (m *Manager) Store() *Store {
	return m.store
}

// ── Remember / Forget ─────────────────────────────────────────────

// Remember adds important information to long-term memory.
// This is the /remember command equivalent.
// Performs contradiction checking against existing blocks and records confirmation
// for blocks that agree with the new content.
func (m *Manager) Remember(content string, tags ...string) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	block := &Block{
		Tier:    TierLong,
		Label:   "user_memory",
		Content: content,
		Source:  "user",
		Tags:    tags,
	}
	m.applyDefaults(block)

	// Check for contradictions with existing long-term blocks.
	if contradictions := m.checkContradictions(content); len(contradictions) > 0 {
		// Mark the new block metadata with contradiction info.
		if block.Metadata == nil {
			block.Metadata = make(map[string]string)
		}
		block.Metadata["has_contradictions"] = "true"
		ids := ""
		for _, c := range contradictions {
			c.Conflict()
			_ = m.store.Update(c)
			if ids != "" {
				ids += ","
			}
			ids += c.ID
		}
		block.Metadata["contradicts"] = ids
	}

	if err := m.store.Add(block); err != nil {
		return nil, err
	}
	return block, nil
}

// Forget removes a memory block by ID or content match.
// This is the /forget command equivalent.
func (m *Manager) Forget(idOrQuery string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Try exact ID match first.
	if err := m.store.Delete(idOrQuery); err == nil {
		return 1, nil
	}

	// Search for content match and delete all matches.
	blocks, err := m.store.Search(idOrQuery)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, b := range blocks {
		if err := m.store.Delete(b.ID); err == nil {
			count++
		}
	}

	if count == 0 {
		return 0, fmt.Errorf("no matching memories found for %q", idOrQuery)
	}
	return count, nil
}

// ── Note Operations ───────────────────────────────────────────────

// AddNote adds a note to the specified tier.
func (m *Manager) AddNote(tier Tier, label, content string, tags ...string) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	block := &Block{
		Tier:    tier,
		Label:   label,
		Content: content,
		Source:  "user",
		Tags:    tags,
	}

	// Apply TTL for short-term.
	if tier == TierShort && m.ShortTermTTL > 0 {
		exp := time.Now().Add(m.ShortTermTTL)
		block.ExpiresAt = &exp
	}

	m.applyDefaults(block)
	if err := m.store.Add(block); err != nil {
		return nil, err
	}
	return block, nil
}

// AddScopedNote adds a note scoped to a specific context (e.g. conversation).
func (m *Manager) AddScopedNote(scope string, tier Tier, label, content string, tags ...string) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	block := &Block{
		Tier:    tier,
		Label:   label,
		Content: content,
		Source:  "user",
		Tags:    tags,
		Scope:   scope,
	}

	if tier == TierShort && m.ShortTermTTL > 0 {
		exp := time.Now().Add(m.ShortTermTTL)
		block.ExpiresAt = &exp
	}

	m.applyDefaults(block)
	if err := m.store.Add(block); err != nil {
		return nil, err
	}
	return block, nil
}

// ── Clear ─────────────────────────────────────────────────────────

// Clear removes all blocks from the specified tier.
// This is the /clear command equivalent.
func (m *Manager) Clear(tier Tier) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.ClearTier(tier)
}

// ClearAll removes all blocks from all tiers.
func (m *Manager) ClearAll() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.ClearAll()
}

// ── Query ─────────────────────────────────────────────────────────

// Search searches across all memory tiers.
func (m *Manager) Search(query string) ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store.Search(query)
}

// SearchScoped searches within a specific scope plus global blocks.
func (m *Manager) SearchScoped(scope, query string) ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all, err := m.store.Search(query)
	if err != nil {
		return nil, err
	}

	return filterScope(all, scope), nil
}

// List lists all blocks for a tier (empty string = all tiers).
func (m *Manager) List(tier Tier) ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store.List(tier)
}

// ListScoped lists blocks for a tier within a specific scope plus global blocks.
func (m *Manager) ListScoped(scope string, tier Tier) ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all, err := m.store.List(tier)
	if err != nil {
		return nil, err
	}

	return filterScope(all, scope), nil
}

// Stats returns memory statistics.
func (m *Manager) Stats() (*Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store.Stats()
}

// ── Context ───────────────────────────────────────────────────────

// BuildContext compiles memory into a ContextWindow for LLM injection.
func (m *Manager) BuildContext() (*ContextWindow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store.BuildContext()
}

// BuildScopedContext compiles memory visible to a specific scope.
// Includes both global blocks (scope="") and blocks matching the given scope.
func (m *Manager) BuildScopedContext(scope string) (*ContextWindow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cw, err := m.store.BuildContext()
	if err != nil {
		return nil, err
	}

	if scope == "" {
		return cw, nil // No filtering needed.
	}

	cw.ShortTerm = filterScope(cw.ShortTerm, scope)
	cw.MediumTerm = filterScope(cw.MediumTerm, scope)
	cw.LongTerm = filterScope(cw.LongTerm, scope)
	return cw, nil
}

// ── Lifecycle ─────────────────────────────────────────────────────

// EndSession handles session end: promotes short-term notes and cleans up.
func (m *Manager) EndSession() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.AutoPromote {
		// Promote non-expired short-term blocks to medium-term.
		var remaining []Block
		for _, b := range m.store.shortTerm {
			if b.IsExpired() {
				continue
			}
			b.Tier = TierMedium
			b.UpdatedAt = time.Now()
			if err := m.store.Add(&b); err != nil {
				remaining = append(remaining, b)
				continue
			}
		}
		m.store.shortTerm = remaining
	} else {
		m.store.shortTerm = nil
	}

	if m.AutoCleanup {
		_, _ = m.store.PruneExpired()
	}

	return nil
}

// RunMaintenance performs periodic maintenance:
// - Prune expired blocks
// - Archive old medium-term blocks to long-term
// - Clean up old summaries
func (m *Manager) RunMaintenance() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Prune expired.
	_, _ = m.store.PruneExpired()

	// 2. Archive old medium-term blocks.
	if m.MediumTermMaxAge > 0 {
		blocks, err := m.store.List(TierMedium)
		if err != nil {
			return err
		}
		cutoff := time.Now().Add(-m.MediumTermMaxAge)
		for _, b := range blocks {
			if b.CreatedAt.Before(cutoff) {
				_ = m.store.Promote(b.ID, TierLong)
			}
		}
	}

	// 3. Clean old summaries (older than 90 days).
	_, _ = m.store.DeleteSummariesOlderThan(90 * 24 * time.Hour)

	return nil
}

// ── Internal Helpers ──────────────────────────────────────────────

// applyDefaults sets default char limit and enforces truncation if configured.
func (m *Manager) applyDefaults(block *Block) {
	if block.CharLimit == 0 && m.DefaultCharLimit > 0 {
		block.CharLimit = m.DefaultCharLimit
	}
	if m.EnforceCharLimit {
		block.TruncateToLimit()
	}
}

// filterScope returns blocks that are global (Scope=="") or match the given scope.
func filterScope(blocks []Block, scope string) []Block {
	if scope == "" {
		return blocks
	}
	var out []Block
	for _, b := range blocks {
		if b.Scope == "" || b.Scope == scope {
			out = append(out, b)
		}
	}
	return out
}

// checkContradictions checks new content against existing long-term blocks
// for potential contradictions using heuristic text comparison.
func (m *Manager) checkContradictions(content string) []*Block {
	blocks, err := m.store.List(TierLong)
	if err != nil {
		return nil
	}

	var contradictions []*Block
	for i := range blocks {
		if blocks[i].ContradictionCheck(content) {
			contradictions = append(contradictions, &blocks[i])
		}
	}
	return contradictions
}

// StaleBlocks returns all blocks that are stale across all tiers.
func (m *Manager) StaleBlocks() ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var stale []Block
	for _, tier := range []Tier{TierShort, TierMedium, TierLong} {
		blocks, err := m.store.List(tier)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			if b.IsStale() {
				stale = append(stale, b)
			}
		}
	}
	return stale, nil
}

// RecordAccess marks a block as accessed, updating its access count and timestamp.
func (m *Manager) RecordAccess(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	block, err := m.store.Get(id)
	if err != nil {
		return err
	}
	block.RecordAccess()
	return m.store.Update(block)
}
