package gleann

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

var bucketPassages = []byte("passages")

// PassageManager handles storage and retrieval of text passages.
// It uses bbolt for fast O(1) retrieval and ACID transactions.
type PassageManager struct {
	mu       sync.RWMutex
	basePath string
	db       *bbolt.DB

	// Optional caching for LoadAll() callers like BM25
	cached []Passage
}

// NewPassageManager creates a new PassageManager.
func NewPassageManager(basePath string) *PassageManager {
	return &PassageManager{
		basePath: basePath,
	}
}

func (pm *PassageManager) dbPath() string {
	return pm.basePath + ".passages.db"
}

func (pm *PassageManager) ensureDB() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.db != nil {
		return nil
	}

	dir := filepath.Dir(pm.basePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	options := &bbolt.Options{Timeout: 5 * time.Second}
	db, err := bbolt.Open(pm.dbPath(), 0644, options)
	if err != nil {
		return fmt.Errorf("open bbolt: %w", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketPassages)
		return err
	})
	if err != nil {
		db.Close()
		return fmt.Errorf("create bucket: %w", err)
	}

	pm.db = db
	return nil
}

// Add adds passages to the manager and writes them to disk.
func (pm *PassageManager) Add(items []Item) ([]int64, error) {
	if err := pm.ensureDB(); err != nil {
		return nil, err
	}

	var ids []int64
	var newPassages []Passage

	err := pm.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)

		for _, item := range items {
			seq, err := b.NextSequence()
			if err != nil {
				return err
			}
			id := int64(seq) - 1 // 0-indexed IDs

			ids = append(ids, id)

			passage := Passage{
				ID:       id,
				Text:     item.Text,
				Metadata: item.Metadata,
			}
			newPassages = append(newPassages, passage)

			data, err := json.Marshal(passage)
			if err != nil {
				return fmt.Errorf("marshal passage %d: %w", id, err)
			}

			var key [8]byte
			binary.BigEndian.PutUint64(key[:], uint64(id))

			if err := b.Put(key[:], data); err != nil {
				return fmt.Errorf("put passage %d: %w", id, err)
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	pm.mu.Lock()
	if pm.cached != nil {
		pm.cached = append(pm.cached, newPassages...)
	}
	pm.mu.Unlock()

	return ids, nil
}

// Get retrieves a passage by its ID in O(1).
func (pm *PassageManager) Get(id int64) (Passage, error) {
	if err := pm.ensureDB(); err != nil {
		return Passage{}, err
	}

	pm.mu.RLock()
	if pm.cached != nil && id >= 0 && id < int64(len(pm.cached)) {
		p := pm.cached[id]
		pm.mu.RUnlock()
		return p, nil
	}
	pm.mu.RUnlock()

	var passage Passage
	err := pm.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		var key [8]byte
		binary.BigEndian.PutUint64(key[:], uint64(id))

		val := b.Get(key[:])
		if val == nil {
			return fmt.Errorf("passage %d not found", id)
		}

		return json.Unmarshal(val, &passage)
	})

	return passage, err
}

// GetBatch retrieves multiple passages by their IDs.
func (pm *PassageManager) GetBatch(ids []int64) ([]Passage, error) {
	if err := pm.ensureDB(); err != nil {
		return nil, err
	}

	passages := make([]Passage, len(ids))

	err := pm.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		for i, id := range ids {
			pm.mu.RLock()
			if pm.cached != nil && id >= 0 && id < int64(len(pm.cached)) {
				passages[i] = pm.cached[id]
				pm.mu.RUnlock()
				continue
			}
			pm.mu.RUnlock()

			var key [8]byte
			binary.BigEndian.PutUint64(key[:], uint64(id))

			val := b.Get(key[:])
			if val == nil {
				return fmt.Errorf("passage %d not found", id)
			}

			var passage Passage
			if err := json.Unmarshal(val, &passage); err != nil {
				return err
			}
			passages[i] = passage
		}
		return nil
	})

	return passages, err
}

// GetTexts retrieves text content for the given IDs.
func (pm *PassageManager) GetTexts(ids []int64) ([]string, error) {
	passages, err := pm.GetBatch(ids)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(passages))
	for i, p := range passages {
		texts[i] = p.Text
	}
	return texts, nil
}

// All returns all passages.
func (pm *PassageManager) All() []Passage {
	if err := pm.ensureDB(); err != nil {
		return nil
	}

	pm.mu.RLock()
	if pm.cached != nil {
		defer pm.mu.RUnlock()
		return pm.cached
	}
	pm.mu.RUnlock()

	var passages []Passage
	pm.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)
		if b == nil {
			return nil
		}

		return b.ForEach(func(k, v []byte) error {
			var p Passage
			if err := json.Unmarshal(v, &p); err == nil {
				passages = append(passages, p)
			}
			return nil
		})
	})
	return passages
}

// Count returns the number of passages.
func (pm *PassageManager) Count() int {
	if err := pm.ensureDB(); err != nil {
		return 0
	}

	pm.mu.RLock()
	if pm.cached != nil {
		defer pm.mu.RUnlock()
		return len(pm.cached)
	}
	pm.mu.RUnlock()

	var count int
	pm.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)
		if b != nil {
			count = b.Stats().KeyN
		}
		return nil
	})
	return count
}

// Load prepares the database connection.
func (pm *PassageManager) Load() error {
	return pm.ensureDB()
}

// LoadAll loads all passages into memory (needed for BM25 scoring).
func (pm *PassageManager) LoadAll() error {
	if err := pm.ensureDB(); err != nil {
		return err
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cached != nil {
		return nil // already loaded
	}

	var passages []Passage
	err := pm.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var p Passage
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			passages = append(passages, p)
			return nil
		})
	})

	if err == nil {
		pm.cached = passages
	}
	return err
}

// Close closes the underlying database.
func (pm *PassageManager) Close() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.db != nil {
		err := pm.db.Close()
		pm.db = nil
		return err
	}
	return nil
}

// RemoveBySource removes all passages whose metadata["source"] matches any of
// the given relative paths. Returns the IDs of removed passages.
func (pm *PassageManager) RemoveBySource(sources []string) ([]int64, error) {
	if err := pm.ensureDB(); err != nil {
		return nil, err
	}

	sourceSet := make(map[string]bool, len(sources))
	for _, s := range sources {
		sourceSet[s] = true
	}

	var removedIDs []int64

	err := pm.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPassages)
		if b == nil {
			return nil
		}

		var keysToRemove [][]byte
		err := b.ForEach(func(k, v []byte) error {
			var p Passage
			if err := json.Unmarshal(v, &p); err != nil {
				return nil // skip corrupt entries
			}
			if src, ok := p.Metadata["source"].(string); ok && sourceSet[src] {
				removedIDs = append(removedIDs, p.ID)
				key := make([]byte, len(k))
				copy(key, k)
				keysToRemove = append(keysToRemove, key)
			}
			return nil
		})
		if err != nil {
			return err
		}

		for _, k := range keysToRemove {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Invalidate in-memory cache since passages changed.
	pm.mu.Lock()
	pm.cached = nil
	pm.mu.Unlock()

	return removedIDs, nil
}

// Delete removes all files associated with this passage manager.
func (pm *PassageManager) Delete() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.db != nil {
		pm.db.Close()
		pm.db = nil
	}

	pm.cached = nil
	os.Remove(pm.basePath + ".passages.jsonl") // cleanup old format if present
	os.Remove(pm.basePath + ".passages.idx")   // cleanup old format if present
	return os.Remove(pm.dbPath())
}
