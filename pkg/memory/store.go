package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/analysis"
)

// Store provides persistent memory storage backed by SQLite (RFC 003,
// docs/plans/003-storage.md): one global memory.sqlite file shared by every
// project and agent process, with WAL and BEGIN IMMEDIATE write transactions.
// Short-term blocks are kept in-memory (session-scoped, never persisted).
// Medium-term and long-term blocks are always persisted.
//
// The legacy bbolt memory.db is migrated on first open (migrate.go) and left
// as a non-bbolt guard file so older binaries fail loudly instead of creating
// an empty store. While a Store is open, a shared OS lock on that guard file
// makes an old binary's exclusive bbolt.Open time out, exactly as the old
// exclusive bbolt lock did; any number of SQLite processes open concurrently.
type Store struct {
	writer       *sql.DB
	reader       *sql.DB
	path         string // legacy path passed to OpenStore; Path() returns it
	sqlitePath   string
	guard        *os.File // shared-locked legacy-path file, held until Close
	analyzer     analysis.Analyzer
	legacyWarned sync.Map // index names already reported by WarnLegacyKuzuOnce

	// In-memory short-term cache (session-scoped).
	shortTerm []Block

	// legacyScopes holds the bare-basename scopes that predate the v1.22
	// collision-safe scope scheme; they stay readable from the matching
	// "local:"/"dir:" scope (finding H7). Set at open time by
	// migrateLegacyScopeNames.
	legacyScopes map[string]bool
}

// DefaultStorePath returns the default memory database path. It keeps the
// legacy memory.db name: OpenStore derives memory.sqlite beside it and
// migrates an existing memory.db (RFC 003 section 3.3).
func DefaultStorePath() string {
	if env := os.Getenv("GLEANN_MEMORY_DIR"); env != "" {
		return filepath.Join(env, "memory.db")
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".gleann", "memory", "memory.db")
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return filepath.Join(home, ".gleann", "memory", "memory.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "memory", "memory.db")
}

// OpenStore opens or creates a memory store at the given path. The path is
// the legacy bbolt location (memory.db); the data lives in memory.sqlite
// beside it. An existing bbolt memory.db is migrated through the locked,
// verified .tmp copy of RFC 003 section 6.4 Memory before the SQLite store
// opens.
func OpenStore(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	sqlitePath := sqlitePathFor(path)

	if !fileExists(sqlitePath) {
		kind, err := classifyLegacy(path)
		if err != nil {
			return nil, err
		}
		if kind == legacyBbolt {
			if err := migrateFromBbolt(path, sqlitePath, nil); err != nil {
				return nil, err
			}
		}
	}

	// A fresh store (or one whose legacy file is gone) gets the guard at the
	// old name right away, so an older binary can never create an empty
	// bbolt memory.db beside the SQLite file (RFC 003 section 3.3).
	if err := ensureGuardFile(path); err != nil {
		return nil, err
	}

	s, err := openSQLite(sqlitePath)
	if err != nil {
		return nil, err
	}
	s.path = path

	// A crash between the migration's two renames leaves the legacy bbolt
	// file beside the authoritative SQLite file; finish the switch now.
	if err := s.completeSwitch(path); err != nil {
		s.Close()
		return nil, err
	}

	guard, err := lockGuardShared(path)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.guard = guard
	return s, nil
}

// DefaultStore opens the default memory store.
func DefaultStore() (*Store, error) {
	return OpenStore(DefaultStorePath())
}

// Close closes the underlying database and releases the guard lock.
func (s *Store) Close() error {
	var errs []error
	if s.writer != nil {
		errs = append(errs, s.writer.Close())
	}
	if s.reader != nil {
		errs = append(errs, s.reader.Close())
	}
	if s.guard != nil {
		errs = append(errs, releaseLockFile(s.guard))
		s.guard = nil
	}
	return errors.Join(errs...)
}

// Path returns the database file path.
func (s *Store) Path() string {
	return s.path
}

// ── Block Operations ──────────────────────────────────────────────

// Add stores a new memory block. Short-term blocks are kept in-memory;
// medium and long-term blocks are persisted to SQLite.
// If an identical block already exists (same content up to case, whitespace
// and surrounding punctuation, same scope and tier), or a near-identical one
// (token Jaccard above a conservative threshold, see nearThreshold), it is
// deduplicated by reinforcing confidence (Confirms++) and merging any tags or
// provenance; *block is then overwritten with the existing block, so callers
// see its ID. When the new text is the old text plus extra words, the longer
// new text replaces the content (the ID is kept), so added detail is not lost. Negations, differing numbers and failure blocks are never merged
// as near duplicates. GLEANN_MEMORY_NEAR_DEDUP=off disables the similarity
// step. Blocks stored under an older ID formula are still found.
// For persisted blocks the read-then-write runs inside one BEGIN IMMEDIATE
// transaction, so several agent processes writing concurrently cannot
// interleave the deduplication (RFC 003 section 3.5).
func (s *Store) Add(block *Block) error {
	if block.ID == "" {
		block.ID = generateBlockID(block)
	}
	if block.Tier == TierShort {
		if i, sup := s.findDuplicateShort(block); i >= 0 && s.shortTerm[i].ID != block.ID {
			existing := &s.shortTerm[i]
			mergeDuplicate(existing, block)
			if sup {
				existing.Content = block.Content
			}
			*block = *existing
			return nil
		}
	}

	// Check if block already exists with this ID in the same tier for deduplication.
	// Short-term first, as Get does.
	for i := range s.shortTerm {
		if s.shortTerm[i].ID == block.ID && s.shortTerm[i].Tier == block.Tier {
			existing := &s.shortTerm[i]
			mergeDuplicate(existing, block)
			*block = *existing
			return nil
		}
	}

	if block.Tier == TierShort {
		// Rare: a persisted block with this ID already in the short tier
		// (only reachable through Update). Mirror the old read-then-update
		// dedup; the common path above never touches the database.
		if existing, err := s.getPersisted(block.ID); err == nil && existing.Tier == block.Tier {
			mergeDuplicate(existing, block)
			*block = *existing
			return s.Update(existing)
		}
		if block.CreatedAt.IsZero() {
			block.CreatedAt = time.Now()
		}
		block.UpdatedAt = time.Now()
		s.shortTerm = append(s.shortTerm, *block)
		return nil
	}

	return s.writeTx(func(tx *sql.Tx) error {
		existing, seq, err := getBlockTx(tx, block.ID)
		if err != nil {
			return err
		}
		supersede := false
		if existing == nil || existing.Tier != block.Tier {
			// No ID hit: look for the same fact under the legacy ID formula
			// or with different wording (RFC: no migration of stored IDs).
			dup, dseq, sup, err := findDuplicateQ(tx, block)
			if err != nil {
				return err
			}
			if dup != nil {
				existing, seq, supersede = dup, dseq, sup
			}
		}
		if existing != nil && existing.Tier == block.Tier {
			mergeDuplicate(existing, block)
			if supersede {
				existing.Content = block.Content
			}
			*block = *existing
			if err := updateBlockTx(tx, existing); err != nil {
				return err
			}
			return refreshLexTx(tx, seq, s.lexTokens(existing))
		}

		if block.CreatedAt.IsZero() {
			block.CreatedAt = time.Now()
		}
		block.UpdatedAt = time.Now()

		if existing != nil {
			// Same ID but a different tier: the old store overwrote the row.
			if err := updateBlockTx(tx, block); err != nil {
				return err
			}
			return refreshLexTx(tx, seq, s.lexTokens(block))
		}
		seq, err = insertBlockTx(tx, block)
		if err != nil {
			return err
		}
		return insertLexTx(tx, seq, s.lexTokens(block))
	})
}

// mergeDuplicate reinforces an existing block with a repeated addition:
// Confirms++, fresh UpdatedAt, and the union of tags, paths and symbols.
// Tags keep the historical substring containment check (containsTag).
func mergeDuplicate(existing, block *Block) {
	existing.Confirm()
	existing.UpdatedAt = time.Now()
	for _, tag := range block.Tags {
		if !containsTag(existing.Tags, tag) {
			existing.Tags = append(existing.Tags, tag)
		}
	}
	if existing.Repo == "" {
		existing.Repo = block.Repo
	}
	if existing.Commit == "" {
		existing.Commit = block.Commit
	}
	for _, p := range block.Paths {
		found := false
		for _, ep := range existing.Paths {
			if ep == p {
				found = true
				break
			}
		}
		if !found {
			existing.Paths = append(existing.Paths, p)
		}
	}
	for _, sym := range block.Symbols {
		found := false
		for _, es := range existing.Symbols {
			if es == sym {
				found = true
				break
			}
		}
		if !found {
			existing.Symbols = append(existing.Symbols, sym)
		}
	}
}

// Get retrieves a block by ID. Checks short-term first, then SQLite.
func (s *Store) Get(id string) (*Block, error) {
	// Check short-term.
	for i := range s.shortTerm {
		if s.shortTerm[i].ID == id {
			return &s.shortTerm[i], nil
		}
	}
	return s.getPersisted(id)
}

func (s *Store) getPersisted(id string) (*Block, error) {
	row := s.reader.QueryRow(`SELECT `+blockCols+` FROM blocks WHERE id = ?`, id)
	b, err := scanBlock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("block not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("reading block %s: %w", id, err)
	}
	return &b, nil
}

// Delete removes a block by ID.
func (s *Store) Delete(id string) error {
	// Check short-term.
	for i := range s.shortTerm {
		if s.shortTerm[i].ID == id {
			s.shortTerm = append(s.shortTerm[:i], s.shortTerm[i+1:]...)
			return nil
		}
	}

	// Delete from SQLite.
	return s.writeTx(func(tx *sql.Tx) error {
		seq, err := blockSeqTx(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("block not found: %s", id)
		}
		if err != nil {
			return fmt.Errorf("reading block %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM blocks WHERE id = ?`, id); err != nil {
			return fmt.Errorf("deleting block %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM blocks_lex WHERE rowid = ?`, seq); err != nil {
			return fmt.Errorf("deleting lexical row for block %s: %w", id, err)
		}
		return nil
	})
}

// Update persists changes to an existing block.
func (s *Store) Update(block *Block) error {
	// Check short-term.
	for i := range s.shortTerm {
		if s.shortTerm[i].ID == block.ID {
			s.shortTerm[i] = *block
			return nil
		}
	}

	// Update in SQLite.
	return s.writeTx(func(tx *sql.Tx) error {
		seq, err := blockSeqTx(tx, block.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("block not found: %s", block.ID)
		}
		if err != nil {
			return fmt.Errorf("reading block %s: %w", block.ID, err)
		}
		if err := updateBlockTx(tx, block); err != nil {
			return err
		}
		return refreshLexTx(tx, seq, s.lexTokens(block))
	})
}

// List returns all blocks for a given tier, sorted by creation time (newest first).
// If tier is empty, returns all blocks across all tiers.
func (s *Store) List(tier Tier) ([]Block, error) {
	var blocks []Block

	// Collect short-term if requested.
	if tier == "" || tier == TierShort {
		for _, b := range s.shortTerm {
			if !b.IsExpired() {
				blocks = append(blocks, b)
			}
		}
	}

	// Collect from SQLite.
	if tier == "" || tier == TierMedium || tier == TierLong {
		query := `SELECT ` + blockCols + ` FROM blocks`
		var args []any
		if tier != "" {
			query += ` WHERE tier = ?`
			args = append(args, string(tier))
		}
		query += ` ORDER BY created_at DESC, id`
		rows, err := s.reader.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("listing blocks: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			block, err := scanBlock(rows)
			if err != nil {
				return nil, fmt.Errorf("listing blocks: %w", err)
			}
			if block.IsExpired() {
				continue
			}
			blocks = append(blocks, block)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("listing blocks: %w", err)
		}
	}

	sort.SliceStable(blocks, func(i, j int) bool {
		return blocks[i].CreatedAt.After(blocks[j].CreatedAt)
	})
	return blocks, nil
}

// Search returns blocks matching the query, ranked by relevance (BM25 over
// label, content, tags, paths and symbols; verbatim phrase matches first,
// ties by recency). See rankBlocks.
func (s *Store) Search(query string) ([]Block, error) {
	all, err := s.List("")
	if err != nil {
		return nil, err
	}
	return rankBlocks(all, query), nil
}

// SearchPhrase returns blocks containing the query verbatim in content, label
// or a tag (case-insensitive), newest first. It is the strict matcher used by
// destructive operations.
func (s *Store) SearchPhrase(query string) ([]Block, error) {
	all, err := s.List("")
	if err != nil {
		return nil, err
	}
	return phraseMatches(all, query), nil
}

// ClearTier removes all blocks for a given tier.
func (s *Store) ClearTier(tier Tier) (int, error) {
	if tier == TierShort {
		count := len(s.shortTerm)
		s.shortTerm = nil
		return count, nil
	}

	count := 0
	err := s.writeTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM blocks_lex WHERE rowid IN (SELECT seq FROM blocks WHERE tier = ?)`, string(tier)); err != nil {
			return fmt.Errorf("clearing lexical rows for tier %s: %w", tier, err)
		}
		res, err := tx.Exec(`DELETE FROM blocks WHERE tier = ?`, string(tier))
		if err != nil {
			return fmt.Errorf("clearing tier %s: %w", tier, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("clearing tier %s: %w", tier, err)
		}
		count = int(n)
		return nil
	})
	return count, err
}

// ClearAll removes all blocks from all tiers.
func (s *Store) ClearAll() (int, error) {
	total := len(s.shortTerm)
	s.shortTerm = nil

	err := s.writeTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM blocks_lex`); err != nil {
			return fmt.Errorf("clearing lexical rows: %w", err)
		}
		res, err := tx.Exec(`DELETE FROM blocks`)
		if err != nil {
			return fmt.Errorf("clearing blocks: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("clearing blocks: %w", err)
		}
		total += int(n)
		return nil
	})
	return total, err
}

// ── Summary Operations ────────────────────────────────────────────

// SaveSummary stores a conversation summary.
func (s *Store) SaveSummary(summary *Summary) error {
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now()
	}

	data, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encoding summary: %w", err)
	}
	key := fmt.Sprintf("%s_%s", summary.CreatedAt.Format("20060102_150405"), summary.ConversationID)
	return s.writeTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO summaries (key, data) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET data = excluded.data`, key, string(data)); err != nil {
			return fmt.Errorf("saving summary %s: %w", key, err)
		}
		return nil
	})
}

// ListSummaries returns all summaries, newest first.
func (s *Store) ListSummaries() ([]Summary, error) {
	rows, err := s.reader.Query(`SELECT data FROM summaries ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("listing summaries: %w", err)
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("listing summaries: %w", err)
		}
		var sum Summary
		if err := json.Unmarshal([]byte(data), &sum); err != nil {
			continue // skip corrupt entries, as the bbolt store did
		}
		summaries = append(summaries, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing summaries: %w", err)
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

// DeleteSummariesOlderThan removes summaries older than the given duration.
func (s *Store) DeleteSummariesOlderThan(d time.Duration) (int, error) {
	cutoff := time.Now().Add(-d)
	count := 0

	err := s.writeTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT key, data FROM summaries`)
		if err != nil {
			return fmt.Errorf("listing summaries: %w", err)
		}
		var toDelete []string
		for rows.Next() {
			var key, data string
			if err := rows.Scan(&key, &data); err != nil {
				rows.Close()
				return fmt.Errorf("listing summaries: %w", err)
			}
			var sum Summary
			if err := json.Unmarshal([]byte(data), &sum); err != nil {
				continue
			}
			if sum.CreatedAt.Before(cutoff) {
				toDelete = append(toDelete, key)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("listing summaries: %w", err)
		}
		for _, key := range toDelete {
			if _, err := tx.Exec(`DELETE FROM summaries WHERE key = ?`, key); err != nil {
				return fmt.Errorf("deleting summary %s: %w", key, err)
			}
			count++
		}
		return nil
	})
	return count, err
}

// ── Lifecycle ─────────────────────────────────────────────────────

// Promote moves a block from one tier to a higher (longer-lived) tier.
func (s *Store) Promote(id string, target Tier) error {
	block, err := s.Get(id)
	if err != nil {
		return err
	}

	// Delete from old location.
	if err := s.Delete(id); err != nil {
		return err
	}

	// Re-add at new tier.
	block.Tier = target
	block.UpdatedAt = time.Now()
	return s.Add(block)
}

// PruneExpired removes all expired blocks from all tiers.
func (s *Store) PruneExpired() (int, error) {
	// Prune short-term.
	count := 0
	var kept []Block
	for _, b := range s.shortTerm {
		if b.IsExpired() {
			count++
		} else {
			kept = append(kept, b)
		}
	}
	s.shortTerm = kept

	// Prune SQLite.
	err := s.writeTx(func(tx *sql.Tx) error {
		now := time.Now().UnixNano()
		if _, err := tx.Exec(`DELETE FROM blocks_lex WHERE rowid IN (SELECT seq FROM blocks WHERE expires_at IS NOT NULL AND expires_at < ?)`, now); err != nil {
			return fmt.Errorf("pruning lexical rows: %w", err)
		}
		res, err := tx.Exec(`DELETE FROM blocks WHERE expires_at IS NOT NULL AND expires_at < ?`, now)
		if err != nil {
			return fmt.Errorf("pruning expired blocks: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("pruning expired blocks: %w", err)
		}
		count += int(n)
		return nil
	})
	return count, err
}

// DeleteOlderThan removes blocks older than the given duration from the specified tier.
func (s *Store) DeleteOlderThan(tier Tier, d time.Duration) (int, error) {
	cutoff := time.Now().Add(-d)

	if tier == TierShort {
		count := 0
		var kept []Block
		for _, b := range s.shortTerm {
			if b.CreatedAt.Before(cutoff) {
				count++
			} else {
				kept = append(kept, b)
			}
		}
		s.shortTerm = kept
		return count, nil
	}

	count := 0
	err := s.writeTx(func(tx *sql.Tx) error {
		cutoffNS := cutoff.UnixNano()
		if _, err := tx.Exec(`DELETE FROM blocks_lex WHERE rowid IN (SELECT seq FROM blocks WHERE tier = ? AND created_at < ?)`, string(tier), cutoffNS); err != nil {
			return fmt.Errorf("deleting lexical rows for tier %s: %w", tier, err)
		}
		res, err := tx.Exec(`DELETE FROM blocks WHERE tier = ? AND created_at < ?`, string(tier), cutoffNS)
		if err != nil {
			return fmt.Errorf("deleting old blocks in tier %s: %w", tier, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("deleting old blocks in tier %s: %w", tier, err)
		}
		count = int(n)
		return nil
	})
	return count, err
}

// Stats returns memory statistics.
func (s *Store) Stats() (*Stats, error) {
	stats := &Stats{}

	// Count short-term.
	for _, b := range s.shortTerm {
		if !b.IsExpired() {
			stats.ShortTermCount++
		}
	}

	// Count persisted entries.
	now := time.Now().UnixNano()
	rows, err := s.reader.Query(`SELECT tier, COUNT(*) FROM blocks WHERE expires_at IS NULL OR expires_at >= ? GROUP BY tier`, now)
	if err != nil {
		return nil, fmt.Errorf("counting blocks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier string
		var n int
		if err := rows.Scan(&tier, &n); err != nil {
			return nil, fmt.Errorf("counting blocks: %w", err)
		}
		switch Tier(tier) {
		case TierMedium:
			stats.MediumTermCount += n
		case TierLong:
			stats.LongTermCount += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("counting blocks: %w", err)
	}

	stats.TotalCount = stats.ShortTermCount + stats.MediumTermCount + stats.LongTermCount

	// Disk size of the SQLite database file.
	info, err := os.Stat(s.sqlitePath)
	if err == nil {
		stats.DiskSizeBytes = info.Size()
	}

	return stats, nil
}

// BuildContext compiles all active memory into a ContextWindow for LLM injection.
func (s *Store) BuildContext() (*ContextWindow, error) {
	cw := &ContextWindow{}

	// Short-term (in-memory).
	for _, b := range s.shortTerm {
		if !b.IsExpired() {
			cw.ShortTerm = append(cw.ShortTerm, b)
		}
	}

	// Medium and long-term from SQLite, in ID order as the bbolt cursor
	// iteration produced.
	rows, err := s.reader.Query(`SELECT `+blockCols+` FROM blocks WHERE tier IN (?, ?) ORDER BY id`, string(TierMedium), string(TierLong))
	if err != nil {
		return nil, fmt.Errorf("building context: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		block, err := scanBlock(rows)
		if err != nil {
			return nil, fmt.Errorf("building context: %w", err)
		}
		if block.IsExpired() {
			continue
		}
		// Failure memories surface in their own section (BuildTaskContext);
		// keep them out of the regular tier rendering so they do not appear
		// twice.
		if block.Label == FailureLabel {
			continue
		}
		switch block.Tier {
		case TierMedium:
			cw.MediumTerm = append(cw.MediumTerm, block)
		case TierLong:
			cw.LongTerm = append(cw.LongTerm, block)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("building context: %w", err)
	}

	// Load recent summaries (last 5).
	summaries, err := s.ListSummaries()
	if err == nil && len(summaries) > 0 {
		limit := 5
		if len(summaries) < limit {
			limit = len(summaries)
		}
		cw.Summaries = summaries[:limit]
	}

	return cw, nil
}

// ── Helpers ───────────────────────────────────────────────────────

// MarkSuspect flags any stored memory blocks whose Paths or Symbols intersect
// with the provided changed files or changed symbols. Returns the number of blocks marked suspect.
func (s *Store) MarkSuspect(changedFiles []string, changedSymbols []string) (int, error) {
	if len(changedFiles) == 0 && len(changedSymbols) == 0 {
		return 0, nil
	}

	fileSet := make(map[string]struct{}, len(changedFiles)*2)
	for _, f := range changedFiles {
		clean := filepath.Clean(f)
		fileSet[clean] = struct{}{}
		fileSet[filepath.Base(clean)] = struct{}{}
	}

	symSet := make(map[string]struct{}, len(changedSymbols)*2)
	for _, sym := range changedSymbols {
		symSet[sym] = struct{}{}
		if idx := strings.LastIndex(sym, "."); idx >= 0 {
			symSet[sym[idx+1:]] = struct{}{}
		}
	}

	count := 0

	// Check short-term
	for i := range s.shortTerm {
		b := &s.shortTerm[i]
		if b.Suspect {
			continue
		}
		if reason, matches := matchesProvenance(b, fileSet, symSet); matches {
			b.Suspect = true
			b.StaleReason = reason
			b.UpdatedAt = time.Now()
			count++
		}
	}

	// Check persistent SQLite blocks. Suspect and StaleReason are not part
	// of the lexical text, so the blocks_lex rows stay untouched.
	err := s.writeTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT ` + blockCols + ` FROM blocks WHERE suspect = 0 ORDER BY id`)
		if err != nil {
			return fmt.Errorf("marking suspect: %w", err)
		}
		var flagged []Block
		for rows.Next() {
			block, err := scanBlock(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("marking suspect: %w", err)
			}
			if reason, matches := matchesProvenance(&block, fileSet, symSet); matches {
				block.Suspect = true
				block.StaleReason = reason
				block.UpdatedAt = time.Now()
				flagged = append(flagged, block)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("marking suspect: %w", err)
		}
		for i := range flagged {
			if err := updateBlockTx(tx, &flagged[i]); err != nil {
				return err
			}
			count++
		}
		return nil
	})

	return count, err
}

func matchesProvenance(b *Block, fileSet map[string]struct{}, symSet map[string]struct{}) (string, bool) {
	for _, s := range b.Symbols {
		if _, ok := symSet[s]; ok {
			return fmt.Sprintf("symbol %s was modified", s), true
		}
		if idx := strings.LastIndex(s, "."); idx >= 0 {
			if _, ok := symSet[s[idx+1:]]; ok {
				return fmt.Sprintf("symbol %s was modified", s), true
			}
		}
	}
	for _, p := range b.Paths {
		clean := filepath.Clean(p)
		if _, ok := fileSet[clean]; ok {
			return fmt.Sprintf("file %s was modified", p), true
		}
		if _, ok := fileSet[filepath.Base(clean)]; ok {
			return fmt.Sprintf("file %s was modified", p), true
		}
	}
	return "", false
}

func containsTag(tags []string, query string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), query) {
			return true
		}
	}
	return false
}
