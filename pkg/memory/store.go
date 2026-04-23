package memory

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketBlocks    = []byte("blocks")
	bucketSummaries = []byte("summaries")
	bucketMeta      = []byte("meta")
)

// Store provides persistent memory storage backed by BBolt.
// Short-term blocks are kept in-memory with optional BBolt persistence.
// Medium-term and long-term blocks are always persisted.
type Store struct {
	db   *bolt.DB
	path string

	// In-memory short-term cache (session-scoped).
	shortTerm []Block
}

// DefaultStorePath returns the default memory database path.
func DefaultStorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "memory", "memory.db")
}

// OpenStore opens or creates a memory store at the given path.
func OpenStore(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open memory db: %w", err)
	}

	// Ensure buckets exist.
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketBlocks, bucketSummaries, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init buckets: %w", err)
	}

	return &Store{db: db, path: path}, nil
}

// DefaultStore opens the default memory store.
func DefaultStore() (*Store, error) {
	return OpenStore(DefaultStorePath())
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Path returns the database file path.
func (s *Store) Path() string {
	return s.path
}

// ── Block Operations ──────────────────────────────────────────────

// Add stores a new memory block. Short-term blocks are kept in-memory;
// medium and long-term blocks are persisted to BBolt.
func (s *Store) Add(block *Block) error {
	if block.ID == "" {
		block.ID = generateBlockID(block)
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = time.Now()
	}
	block.UpdatedAt = time.Now()

	if block.Tier == TierShort {
		s.shortTerm = append(s.shortTerm, *block)
		return nil
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		data, err := json.Marshal(block)
		if err != nil {
			return err
		}
		return b.Put([]byte(block.ID), data)
	})
}

// Get retrieves a block by ID. Checks short-term first, then BBolt.
func (s *Store) Get(id string) (*Block, error) {
	// Check short-term.
	for i := range s.shortTerm {
		if s.shortTerm[i].ID == id {
			return &s.shortTerm[i], nil
		}
	}

	// Check BBolt.
	var block Block
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("block not found: %s", id)
		}
		return json.Unmarshal(data, &block)
	})
	if err != nil {
		return nil, err
	}
	return &block, nil
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

	// Delete from BBolt.
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		if b.Get([]byte(id)) == nil {
			return fmt.Errorf("block not found: %s", id)
		}
		return b.Delete([]byte(id))
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

	// Update in BBolt.
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		if b.Get([]byte(block.ID)) == nil {
			return fmt.Errorf("block not found: %s", block.ID)
		}
		data, err := json.Marshal(block)
		if err != nil {
			return err
		}
		return b.Put([]byte(block.ID), data)
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

	// Collect from BBolt.
	if tier == "" || tier == TierMedium || tier == TierLong {
		err := s.db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketBlocks)
			return b.ForEach(func(k, v []byte) error {
				var block Block
				if err := json.Unmarshal(v, &block); err != nil {
					return nil // skip corrupt entries
				}
				if block.IsExpired() {
					return nil
				}
				if tier == "" || block.Tier == tier {
					blocks = append(blocks, block)
				}
				return nil
			})
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].CreatedAt.After(blocks[j].CreatedAt)
	})
	return blocks, nil
}

// Search returns blocks whose content or label contains the query (case-insensitive).
func (s *Store) Search(query string) ([]Block, error) {
	q := strings.ToLower(query)
	all, err := s.List("")
	if err != nil {
		return nil, err
	}

	var results []Block
	for _, b := range all {
		if strings.Contains(strings.ToLower(b.Content), q) ||
			strings.Contains(strings.ToLower(b.Label), q) ||
			containsTag(b.Tags, q) {
			results = append(results, b)
		}
	}
	return results, nil
}

// ClearTier removes all blocks for a given tier.
func (s *Store) ClearTier(tier Tier) (int, error) {
	if tier == TierShort {
		count := len(s.shortTerm)
		s.shortTerm = nil
		return count, nil
	}

	count := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		var toDelete [][]byte
		err := b.ForEach(func(k, v []byte) error {
			var block Block
			if err := json.Unmarshal(v, &block); err != nil {
				return nil
			}
			if block.Tier == tier {
				toDelete = append(toDelete, append([]byte{}, k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// ClearAll removes all blocks from all tiers.
func (s *Store) ClearAll() (int, error) {
	total := len(s.shortTerm)
	s.shortTerm = nil

	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		var toDelete [][]byte
		err := b.ForEach(func(k, v []byte) error {
			toDelete = append(toDelete, append([]byte{}, k...))
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			total++
		}
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

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSummaries)
		data, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%s_%s", summary.CreatedAt.Format("20060102_150405"), summary.ConversationID)
		return b.Put([]byte(key), data)
	})
}

// ListSummaries returns all summaries, newest first.
func (s *Store) ListSummaries() ([]Summary, error) {
	var summaries []Summary
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSummaries)
		return b.ForEach(func(k, v []byte) error {
			var sum Summary
			if err := json.Unmarshal(v, &sum); err != nil {
				return nil
			}
			summaries = append(summaries, sum)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

// DeleteSummariesOlderThan removes summaries older than the given duration.
func (s *Store) DeleteSummariesOlderThan(d time.Duration) (int, error) {
	cutoff := time.Now().Add(-d)
	count := 0

	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSummaries)
		var toDelete [][]byte
		err := b.ForEach(func(k, v []byte) error {
			var sum Summary
			if err := json.Unmarshal(v, &sum); err != nil {
				return nil
			}
			if sum.CreatedAt.Before(cutoff) {
				toDelete = append(toDelete, append([]byte{}, k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
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

	// Prune BBolt.
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		var toDelete [][]byte
		err := b.ForEach(func(k, v []byte) error {
			var block Block
			if err := json.Unmarshal(v, &block); err != nil {
				return nil
			}
			if block.IsExpired() {
				toDelete = append(toDelete, append([]byte{}, k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			count++
		}
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
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		var toDelete [][]byte
		err := b.ForEach(func(k, v []byte) error {
			var block Block
			if err := json.Unmarshal(v, &block); err != nil {
				return nil
			}
			if block.Tier == tier && block.CreatedAt.Before(cutoff) {
				toDelete = append(toDelete, append([]byte{}, k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			count++
		}
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

	// Count BBolt entries.
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		return b.ForEach(func(k, v []byte) error {
			var block Block
			if err := json.Unmarshal(v, &block); err != nil {
				return nil
			}
			if block.IsExpired() {
				return nil
			}
			switch block.Tier {
			case TierMedium:
				stats.MediumTermCount++
			case TierLong:
				stats.LongTermCount++
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	stats.TotalCount = stats.ShortTermCount + stats.MediumTermCount + stats.LongTermCount

	// Disk size.
	info, err := os.Stat(s.path)
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

	// Medium and long-term from BBolt.
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		return b.ForEach(func(k, v []byte) error {
			var block Block
			if err := json.Unmarshal(v, &block); err != nil {
				return nil
			}
			if block.IsExpired() {
				return nil
			}
			switch block.Tier {
			case TierMedium:
				cw.MediumTerm = append(cw.MediumTerm, block)
			case TierLong:
				cw.LongTerm = append(cw.LongTerm, block)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
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

func generateBlockID(block *Block) string {
	h := sha256.New()
	h.Write([]byte(block.Content))
	h.Write([]byte(block.Label))
	h.Write([]byte(time.Now().Format(time.RFC3339Nano)))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func containsTag(tags []string, query string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), query) {
			return true
		}
	}
	return false
}
