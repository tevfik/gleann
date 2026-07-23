//go:build treesitter

// Package indexer — incremental AST hashing.
//
// FileHashStore persists the SHA-256 of every successfully indexed source
// file. On incremental updates, IndexFiles consults the store to skip files
// whose content has not changed since the last successful write — turning
// a 3-pass parse+DB-write per file into a single hash comparison.
//
// The store is best-effort: if bbolt fails to open, the indexer falls back
// to the legacy "always re-parse" path. If a hash write fails mid-batch,
// the file is still considered indexed (the data is in KuzuDB) but its
// hash is forgotten — the next incremental run will re-parse it once.
package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	fileHashBucket = []byte("indexed_files_v1")
)

// IndexedFile records the content-hash state of one successfully indexed
// source file. Persisted as JSON in bbolt.
type IndexedFile struct {
	Path         string `json:"path"`
	Hash         string `json:"hash"`
	IndexedAt    int64  `json:"indexed_at"`
	Size         int64  `json:"size"`
	SymbolCount  int    `json:"symbol_count"`
	Lang         string `json:"lang"`
}

// FileHashStore is a per-index hash store backed by bbolt. Construct one
// per Indexer; close it when the index is closed.
type FileHashStore struct {
	db   *bolt.DB
	mu   sync.Mutex // guards hashMap (write-through cache) for hot reads
	hash map[string]string
}

// DefaultHashStorePath returns the canonical on-disk location of the hash
// store, alongside the KuzuDB graph directory.
//
//	indexDir/<name>_graph/file_hashes.db
func DefaultHashStorePath(indexDir, name string) string {
	return filepath.Join(indexDir, name+"_file_hashes.db")
}

// NewFileHashStore opens (or creates) a hash store at path.
func NewFileHashStore(path string) (*FileHashStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir for hash store: %w", err)
	}
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		// Recover from a corrupt store by recreating it.
		_ = os.Remove(path)
		db, err = bolt.Open(path, 0o644, &bolt.Options{Timeout: 5 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("open hash store: %w", err)
		}
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(fileHashBucket)
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init hash store bucket: %w", err)
	}

	store := &FileHashStore{db: db, hash: make(map[string]string)}
	// Warm the cache once at startup so the first incremental run is fast.
	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(fileHashBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var rec IndexedFile
			if err := jsonUnmarshal(v, &rec); err == nil {
				store.hash[rec.Path] = rec.Hash
			}
			return nil
		})
	})
	return store, nil
}

// Close releases the underlying bbolt file.
func (s *FileHashStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Get returns the persisted hash for path, or "" if the path is unknown.
// Reads are O(1) from the in-memory cache.
func (s *FileHashStore) Get(path string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hash[path]
}

// IsDirty reports whether the file at fsPath has a content hash that
// differs from the hash stored under key. A file that has never been
// seen is considered dirty. If the file no longer exists on disk,
// IsDirty returns (true, "") so the caller can run Remove to clean
// up the stale record.
//
// `key` is the stable identifier used by Mark (typically the path
// RELATIVE to the index root); `fsPath` is the actual filesystem path
// used for hashing. Passing different keys here will always return
// dirty=true.
func (s *FileHashStore) IsDirty(key, fsPath string) (dirty bool, currentHash string, err error) {
	if s == nil {
		return true, "", nil
	}
	h, hashErr := ComputeFileHash(fsPath)
	if hashErr != nil {
		return true, "", nil
	}
	stored := s.Get(key)
	return stored != h, h, nil
}

// Mark records that path was successfully indexed with the given hash.
// It updates both the persistent store and the in-memory cache. The
// record is keyed by relative path; pass idx.relPath(absPath) from the
// indexer to keep keys stable across absolute-path changes.
func (s *FileHashStore) Mark(path, hash, lang string, size int64, symbolCount int) error {
	if s == nil {
		return nil
	}
	rec := IndexedFile{
		Path:        path,
		Hash:        hash,
		IndexedAt:   time.Now().Unix(),
		Size:        size,
		SymbolCount: symbolCount,
		Lang:        lang,
	}
	data, err := jsonMarshal(rec)
	if err != nil {
		return err
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(fileHashBucket)
		return b.Put([]byte(path), data)
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.hash[path] = hash
	s.mu.Unlock()
	return nil
}

// Remove deletes the record for path. Called when a file is deleted or
// when a parse/import fails irrecoverably. Safe to call on a nil store.
func (s *FileHashStore) Remove(path string) error {
	if s == nil {
		return nil
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(fileHashBucket)
		return b.Delete([]byte(path))
	}); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.hash, path)
	s.mu.Unlock()
	return nil
}

// Clear removes all records. Used on full re-index (IndexDir) to keep the
// store in sync with the database wipe. Implemented as a per-key delete
// (rather than dropping and recreating the bucket) so concurrent readers
// on other goroutines never see a "bucket missing" window.
func (s *FileHashStore) Clear() error {
	if s == nil {
		return nil
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(fileHashBucket)
		if b == nil {
			return nil
		}
		// Collect keys first; deleting during iteration mutates the cursor.
		var keys [][]byte
		if err := b.ForEach(func(k, _ []byte) error {
			keys = append(keys, append([]byte(nil), k...))
			return nil
		}); err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.hash = make(map[string]string)
	s.mu.Unlock()
	return nil
}

// Count returns the number of tracked files (for diagnostics).
func (s *FileHashStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hash)
}

// ComputeFileHash returns the hex SHA-256 of the file's content. It is
// extracted into a top-level helper so the indexer can call it without
// pulling in vault.Tracker (which has its own on-disk schema).
func ComputeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Suppress unused-import warning for context in builds where the hash
// store is constructed without a context.
var _ = context.Background
