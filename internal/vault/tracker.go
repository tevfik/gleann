package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

var (
	bucketFiles = []byte("files")
	bucketPaths = []byte("paths")
)

// FileRecord represents a tracked file's metadata
type FileRecord struct {
	Hash         string `json:"hash"`
	Path         string `json:"path"`
	LastModified int64  `json:"last_modified"`
	Size         int64  `json:"size"`
}

// Tracker handles bbolt database mapping hash -> file path.
type Tracker struct {
	db *bbolt.DB
}

// DefaultDBPath returns the standard vault database location.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".gleann", "vault")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "vault.db")
}

// NewTracker initializes a new bbolt hash tracker.
func NewTracker(dbPath string) (*Tracker, error) {
	db, err := bbolt.Open(dbPath, 0644, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		// If it's an old SQLite database or corrupted, remove it and try again.
		os.Remove(dbPath)
		db, err = bbolt.Open(dbPath, 0644, &bbolt.Options{Timeout: 1 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("open bbolt after recreate: %w", err)
		}
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketFiles); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketPaths); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Tracker{db: db}, nil
}

// ComputeHash computes the SHA-256 hash of a file's content.
func ComputeHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// UpsertFile computes a file's hash and ensures it's in the DB with its current path.
// This repairs broken references automatically if the file was moved.
func (t *Tracker) UpsertFile(ctx context.Context, path string) (string, error) {
	hash, err := ComputeHash(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	err = t.UpsertRecord(ctx, hash, path, info.ModTime().Unix(), info.Size())
	return hash, err
}

// UpsertRecord directly upserts file metadata and hash without re-reading the file.
func (t *Tracker) UpsertRecord(ctx context.Context, hash, path string, modTime, size int64) error {
	record := FileRecord{
		Hash:         hash,
		Path:         path,
		LastModified: modTime,
		Size:         size,
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	return t.db.Update(func(tx *bbolt.Tx) error {
		filesBucket := tx.Bucket(bucketFiles)
		pathsBucket := tx.Bucket(bucketPaths)

		// Check if a path existed before with a different hash, to clean up old paths index
		oldHash := pathsBucket.Get([]byte(path))
		if oldHash != nil && string(oldHash) != hash {
			// Actually the old record hash is different, so we should allow overwrite,
			// but removing the file record for the old hash is tricky without full tracking.
			// Re-indexing handles overwrites though.
		}

		if err := filesBucket.Put([]byte(hash), data); err != nil {
			return err
		}
		if err := pathsBucket.Put([]byte(path), []byte(hash)); err != nil {
			return err
		}
		return nil
	})
}

// GetPathByHash finds the current actual path of a file, enabling robust recomputations.
func (t *Tracker) GetPathByHash(ctx context.Context, hash string) (string, error) {
	var path string
	err := t.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketFiles)
		val := b.Get([]byte(hash))
		if val == nil {
			return fmt.Errorf("hash not found: %s", hash)
		}
		var record FileRecord
		if err := json.Unmarshal(val, &record); err != nil {
			return err
		}
		path = record.Path
		return nil
	})
	return path, err
}

// GetHashByPath finds the hash mapped to a path (if tracking).
func (t *Tracker) GetHashByPath(ctx context.Context, path string) (string, error) {
	var hash string
	err := t.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPaths)
		val := b.Get([]byte(path))
		if val == nil {
			return fmt.Errorf("path not found: %s", path)
		}
		hash = string(val)
		return nil
	})
	return hash, err
}

// RemoveByHash removes a hash from tracking.
func (t *Tracker) RemoveByHash(ctx context.Context, hash string) error {
	return t.db.Update(func(tx *bbolt.Tx) error {
		filesBucket := tx.Bucket(bucketFiles)
		pathsBucket := tx.Bucket(bucketPaths)

		val := filesBucket.Get([]byte(hash))
		if val != nil {
			var record FileRecord
			if err := json.Unmarshal(val, &record); err == nil {
				pathsBucket.Delete([]byte(record.Path))
			}
		}

		return filesBucket.Delete([]byte(hash))
	})
}

func (t *Tracker) Close() error {
	return t.db.Close()
}
