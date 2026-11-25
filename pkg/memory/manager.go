package memory

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

	// MaxContextTokens caps the memory injected into prompts and compiled
	// context windows, in estimated tokens (0 = unlimited). Blocks over the
	// budget are dropped lowest-priority first; see ContextWindow.ApplyBudget.
	MaxContextTokens int
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
		MaxContextTokens: DefaultMaxContextTokens,
	}

	// Read char limit from env.
	if v := os.Getenv("GLEANN_BLOCK_CHAR_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mgr.DefaultCharLimit = n
			mgr.EnforceCharLimit = true
		}
	}

	// Read the injection token budget from env (0 = unlimited).
	if v := os.Getenv("GLEANN_MEMORY_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			mgr.MaxContextTokens = n
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
// Remember adds important information to long-term memory.
// This is the /remember command equivalent.
// Performs contradiction checking against existing blocks and records confirmation
// for blocks that agree with the new content.
func (m *Manager) Remember(content string, tags ...string) (*Block, error) {
	block := &Block{
		Tier:    TierLong,
		Label:   "user_memory",
		Content: content,
		Source:  "user",
		Tags:    tags,
	}
	return m.RememberBlock(block)
}

// RememberBlock stores any memory block, executing contradiction checks against
// existing knowledge and deduplicating identical memories (Confirms++).
func (m *Manager) RememberBlock(block *Block) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Canonicalize scope identifiers before dedup/contradiction checks so
	// padded spellings merge with their normalized siblings.
	normalizeBlockScopes(block)

	m.applyDefaults(block)

	// Check for contradictions with existing long-term blocks.
	if contradictions := m.checkContradictions(block); len(contradictions) > 0 {
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

// RevertReflection reverts a reflection run by ID, removing exactly the blocks
// it added and undoing exactly the confirmations it made. It restores nothing
// else and leaves original blocks otherwise untouched.
func (m *Manager) RevertReflection(runID string) (*ChangeSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cs, err := m.store.GetChangeSet(runID)
	if err != nil {
		return nil, err
	}

	// A block already gone (deleted by hand since) is not an error; any
	// other failure stops before the run record is dropped, so the revert
	// can be retried.
	for _, id := range cs.BlocksAdded {
		if _, err := m.store.Get(id); err != nil {
			continue // already gone
		}
		if err := m.store.Delete(id); err != nil {
			return nil, fmt.Errorf("reverting reflection run %s: deleting block %s: %w", runID, id, err)
		}
	}

	// Undo the confirmations the run made to pre-existing blocks: give back
	// the confirmation count and restore the prior UpdatedAt. Only the exact
	// state the run produced is reversed, so a later confirmation or edit is
	// never clobbered. Old change sets carry no confirmations and skip this.
	// Unwind in reverse (LIFO) order: when the run confirmed the same block
	// several times, each entry's Prior* state is the previous entry's
	// NewUpdatedAt state, so only last-in-first-out unwinding reaches the
	// pre-run baseline (forward iteration stops one confirmation short).
	for i := len(cs.BlocksConfirmed) - 1; i >= 0; i-- {
		conf := cs.BlocksConfirmed[i]
		b, err := m.store.Get(conf.ID)
		if err != nil {
			continue // already gone
		}
		changed := false
		if b.Confirms > conf.PriorConfirms {
			b.Confirms--
			changed = true
		}
		if !conf.NewUpdatedAt.IsZero() && b.UpdatedAt.Equal(conf.NewUpdatedAt) {
			b.UpdatedAt = conf.PriorUpdatedAt
			changed = true
		}
		if changed {
			if err := m.store.Update(b); err != nil {
				return nil, fmt.Errorf("reverting reflection run %s: restoring block %s: %w", runID, conf.ID, err)
			}
		}
	}

	if err := m.store.DeleteChangeSet(runID); err != nil {
		return nil, fmt.Errorf("reverting reflection run %s: %w", runID, err)
	}
	return cs, nil
}

// ListReflections returns persisted reflection runs, newest first. Limit <= 0
// means no cap.
func (m *Manager) ListReflections(limit int) ([]*ChangeSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.ListChangeSets(limit)
}

// MarkSuspect flags stored memory blocks associated with modified files or symbols as suspect.
func (m *Manager) MarkSuspect(changedFiles []string, changedSymbols []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.MarkSuspect(changedFiles, changedSymbols)
}

// minBlockIDPrefix is the shortest ID prefix Forget accepts: the 8 characters
// list and search display.
const minBlockIDPrefix = 8

// Forget removes a memory block by ID, by a unique ID prefix, or by content
// match. This is the /forget command equivalent.
//
// A content phrase that matches more than one block deletes nothing and
// returns a *ForgetAmbiguousError listing the matches; ForgetAll deletes them
// all when the caller asked for that explicitly (CLI --all, REST ?all=true,
// MCP "all": true, TUI /forget --all).
func (m *Manager) Forget(idOrQuery string) (int, error) {
	return m.forget(idOrQuery, false)
}

// ForgetAll is Forget that also deletes every block of a multi-block content
// match. An ID or ID prefix still addresses exactly one block.
func (m *Manager) ForgetAll(idOrQuery string) (int, error) {
	return m.forget(idOrQuery, true)
}

// ForgetMatch is one block a refused Forget would have deleted.
type ForgetMatch struct {
	ID      string `json:"id"`
	Tier    Tier   `json:"tier"`
	Label   string `json:"label,omitempty"`
	Excerpt string `json:"excerpt"`
}

// ForgetAmbiguousError is returned by Forget when a content phrase matches
// several blocks and the caller did not ask to delete them all. Nothing was
// deleted.
type ForgetAmbiguousError struct {
	Query   string
	Matches []ForgetMatch
}

func (e *ForgetAmbiguousError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%q matches %d memory blocks; nothing was deleted. Use a block ID, or ask for all of them (CLI --all, REST ?all=true, MCP \"all\": true, TUI /forget --all):", e.Query, len(e.Matches))
	for _, mt := range e.Matches {
		fmt.Fprintf(&sb, "\n  %s  [%s] %s", shortBlockID(mt.ID), mt.Tier, mt.Excerpt)
	}
	return sb.String()
}

func shortBlockID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func forgetExcerpt(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	if r := []rune(content); len(r) > 60 {
		return string(r[:60]) + "..."
	}
	return content
}

func (m *Manager) forget(idOrQuery string, all bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Try exact ID match first.
	if err := m.store.Delete(idOrQuery); err == nil {
		return 1, nil
	}

	// Then a unique ID prefix of at least minBlockIDPrefix characters (the
	// length list and search display), so `forget a1b2c3d4` addresses the block
	// it shows. A hex string can also be phrase text, so a prefix that ALSO
	// matches block content is refused as ambiguous, as is a prefix shared by
	// several IDs: deleting on a guess is worse than asking for the full ID.
	if isBlockIDPrefix(idOrQuery) {
		blocks, err := m.store.List("")
		if err != nil {
			return 0, err
		}
		prefix := strings.ToLower(idOrQuery)
		var matches []string
		for _, b := range blocks {
			if strings.HasPrefix(strings.ToLower(b.ID), prefix) {
				matches = append(matches, b.ID)
			}
		}
		switch len(matches) {
		case 0:
			// Not an ID after all: fall through to the phrase match below.
		case 1:
			phrase, err := m.store.SearchPhrase(idOrQuery)
			if err != nil {
				return 0, err
			}
			if len(phrase) > 0 {
				return 0, fmt.Errorf("%q matches block %s by ID and %d block(s) by content; pass the full 16-character ID to delete that block, or reword the text", idOrQuery, matches[0], len(phrase))
			}
			if err := m.store.Delete(matches[0]); err != nil {
				return 0, fmt.Errorf("deleting memory %s: %w", matches[0], err)
			}
			return 1, nil
		default:
			return 0, fmt.Errorf("ambiguous memory ID prefix %q matches %d blocks; provide a longer prefix", idOrQuery, len(matches))
		}
	}

	// Search for content match; several matches need an explicit all.
	// Strict phrase match: ranked search is deliberately loose and must not
	// drive deletion.
	blocks, err := m.store.SearchPhrase(idOrQuery)
	if err != nil {
		return 0, err
	}

	if len(blocks) > 1 && !all {
		amb := &ForgetAmbiguousError{Query: idOrQuery}
		for _, b := range blocks {
			amb.Matches = append(amb.Matches, ForgetMatch{ID: b.ID, Tier: b.Tier, Label: b.Label, Excerpt: forgetExcerpt(b.Content)})
		}
		return 0, amb
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

// isBlockIDPrefix reports whether idOrQuery looks like the beginning of a
// memory block ID: at least minBlockIDPrefix characters, all of them hex
// digits. Block IDs are 16 lowercase hexadecimal characters (blockIDFor), and
// the CLI shows their first eight, so anything shorter ("2024", "cafe") or
// non-hex stays a phrase query.
func isBlockIDPrefix(s string) bool {
	if len(s) < minBlockIDPrefix {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
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
	normalizeBlockScopes(block)

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

	scope = NormalizeScope(scope)
	if IsAllScopesSentinel(scope) {
		scope = ""
	}

	all, err := m.store.List("")
	if err != nil {
		return nil, err
	}

	// Rank only what the scope may see, so BM25 statistics are not skewed by
	// blocks the caller cannot access.
	return rankBlocks(filterScope(all, scope), query), nil
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

	scope = NormalizeScope(scope)
	if IsAllScopesSentinel(scope) {
		scope = ""
	}

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

func (m *Manager) buildContextLocked() (*ContextWindow, error) {
	cw, err := m.store.BuildContext()
	if err != nil {
		return nil, err
	}

	cw.ShortTerm = filterValid(cw.ShortTerm)
	cw.MediumTerm = filterValid(cw.MediumTerm)
	cw.LongTerm = filterValid(cw.LongTerm)
	return cw, nil
}

// BuildContext compiles memory into a ContextWindow for LLM injection.
// Automatically filters out low-validity (< 0.2) or expired blocks to keep context clean.
// The result is capped at MaxContextTokens (lowest-priority blocks drop first)
// and Budget records what was injected and what was dropped.
func (m *Manager) BuildContext() (*ContextWindow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cw, err := m.buildContextLocked()
	if err != nil {
		return nil, err
	}
	m.applyBudget(cw)
	return cw, nil
}

// BuildRepoContext compiles the memory visible to the current repository:
// global blocks, the repository scope and its path ancestors. It is the
// default scope of MCP memory_context and the context injected by ask, chat
// and the TUI.
func (m *Manager) BuildRepoContext() (*ContextWindow, error) {
	return m.BuildScopedContext(DetectRepoScope())
}

// BuildScopedContext compiles memory visible to a specific scope.
// Includes both global blocks (scope="") and blocks matching the given scope.
// The result is capped at MaxContextTokens after scope filtering.
func (m *Manager) BuildScopedContext(scope string) (*ContextWindow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scope = NormalizeScope(scope)
	if IsAllScopesSentinel(scope) {
		scope = ""
	}

	cw, err := m.buildContextLocked()
	if err != nil {
		return nil, err
	}

	if scope != "" {
		cw.ShortTerm = filterScope(cw.ShortTerm, scope)
		cw.MediumTerm = filterScope(cw.MediumTerm, scope)
		cw.LongTerm = filterScope(cw.LongTerm, scope)
	}
	m.applyBudget(cw)
	return cw, nil
}

// applyBudget caps cw at the manager's configured injection budget; the
// report stays on cw.Budget for the CLI, REST and MCP outputs. It does not
// log: the TUI shares the terminal and a log line would corrupt its screen.
// Must be called with m.mu held.
func (m *Manager) applyBudget(cw *ContextWindow) {
	cw.ApplyBudget(m.MaxContextTokens)
}

// Compact prunes expired blocks and blocks whose validity score falls below minValidity.
// If minValidity <= 0, it defaults to 0.2 (unreliable threshold).
// Returns the count of pruned blocks.
func (m *Manager) Compact(minValidity float64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if minValidity <= 0 {
		minValidity = 0.2
	}

	prunedCount := 0

	// 1. Prune expired first.
	n, _ := m.store.PruneExpired()
	prunedCount += n

	// 2. Prune low-validity short-term blocks.
	var remainingShort []Block
	for _, b := range m.store.shortTerm {
		if b.ValidityScore() < minValidity {
			prunedCount++
		} else {
			remainingShort = append(remainingShort, b)
		}
	}
	m.store.shortTerm = remainingShort

	// 3. Prune low-validity medium and long-term blocks from store.
	for _, tier := range []Tier{TierMedium, TierLong} {
		blocks, err := m.store.List(tier)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			if b.ValidityScore() < minValidity {
				if err := m.store.Delete(b.ID); err == nil {
					prunedCount++
				}
			}
		}
	}

	return prunedCount, nil
}

func filterValid(blocks []Block) []Block {
	var valid []Block
	for _, b := range blocks {
		if !b.IsExpired() && b.ValidityScore() >= 0.2 {
			valid = append(valid, b)
		}
	}
	return valid
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
			b.ID = ""
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
//
// Hierarchical scope: when the query scope contains a "/" separator (e.g.
// "myorg/backend/feature-x"), the filter also returns blocks scoped to any
// ancestor in the path ("myorg", "myorg/backend"). This enables
// project/repo/branch-level memory inheritance: facts stored at a parent scope
// are automatically visible to all descendants. Global blocks (Scope=="") are
// always visible regardless of scope structure.
//
// Examples (query scope -> visible scopes):
//
//	"myorg/backend/x" -> {"", "myorg", "myorg/backend", "myorg/backend/x"}
//	"acme"            -> {"", "acme"}
//	"" (no scope)     -> all blocks
func filterScope(blocks []Block, scope string) []Block {
	if scope == "" {
		return blocks
	}
	allowed := scopeAncestors(scope)
	var out []Block
	for _, b := range blocks {
		if _, ok := allowed[b.Scope]; ok {
			out = append(out, b)
		}
	}
	return out
}

// scopeAncestors returns the set of scopes visible to a given query scope.
// Always includes "" (global) and the scope itself, plus every ancestor when
// the scope is path-shaped ("a/b/c" -> "a", "a/b", "a/b/c").
func scopeAncestors(scope string) map[string]struct{} {
	out := map[string]struct{}{"": {}, scope: {}}
	if !strings.Contains(scope, "/") {
		return out
	}
	parts := strings.Split(scope, "/")
	cur := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if cur == "" {
			cur = p
		} else {
			cur = cur + "/" + p
		}
		out[cur] = struct{}{}
	}
	return out
}

// checkContradictions checks a new block against the existing long-term
// blocks visible to its scope (global blocks, the scope itself and its
// ancestors) for potential contradictions using heuristic text comparison.
// The block the write would merge into is skipped: a duplicate confirms the
// stored knowledge, it does not contradict it.
//
// Scope visibility is asymmetric, exactly like search: a writer only sees
// global blocks and its own scope chain, never descendants or siblings. A
// write therefore never lowers the validity of a block in a scope the writer
// cannot see — a global write conflicts only with global blocks. filterScope
// is deliberately not used here: for the global scope it returns every block
// (correct for search, wrong for conflict marking).
func (m *Manager) checkContradictions(block *Block) []*Block {
	blocks, err := m.store.List(TierLong)
	if err != nil {
		return nil
	}
	visible := scopeAncestors(block.Scope)
	inScope := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		if _, ok := visible[b.Scope]; ok {
			inScope = append(inScope, b)
		}
	}
	blocks = inScope

	mergeID := ""
	if dup, err := m.store.FindDuplicate(block); err == nil && dup != nil {
		mergeID = dup.ID
	}

	var contradictions []*Block
	for i := range blocks {
		if blocks[i].ID == mergeID {
			continue
		}
		if blocks[i].ContradictionCheck(block.Content) {
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

// Export exports all memory blocks and summaries to markdown files in dir.
func (m *Manager) Export(dir string) (*ExportStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.Export(dir)
}

// Import imports memory blocks and summaries from markdown files in dir.
func (m *Manager) Import(dir string) (*ImportStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.Import(dir)
}
