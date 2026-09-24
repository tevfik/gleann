package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
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
	os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "vault.db")
}

// NewTracker initializes a new bbolt hash tracker.
func NewTracker(dbPath string) (*Tracker, error) {
	db, err := bbolt.Open(dbPath, 0644, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) || os.IsPermission(err) {
			return nil, fmt.Errorf("open bbolt: %w", err)
		}
		// If it's an old SQLite database or corrupted, remove it and try again.
		os.Remove(dbPath)
		db, err = bbolt.Open(dbPath, 0644, &bbolt.Options{Timeout: 5 * time.Second})
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

// BatchUpsertRecords writes multiple file records into the database in a single transaction.
func (t *Tracker) BatchUpsertRecords(ctx context.Context, records []FileRecord) error {
	if len(records) == 0 {
		return nil
	}
	return t.db.Update(func(tx *bbolt.Tx) error {
		filesBucket := tx.Bucket(bucketFiles)
		pathsBucket := tx.Bucket(bucketPaths)

		for _, record := range records {
			data, err := json.Marshal(record)
			if err != nil {
				continue
			}
			if err := filesBucket.Put([]byte(record.Hash), data); err != nil {
				return err
			}
			if err := pathsBucket.Put([]byte(record.Path), []byte(record.Hash)); err != nil {
				return err
			}
		}
		return nil
	})
}

type pathMtime struct {
	path  string
	mtime time.Time
}

// SortFilesByNewest sorts file paths by modification time in descending order (newest first).
func SortFilesByNewest(paths []string) {
	if len(paths) <= 1 {
		return
	}
	items := make([]pathMtime, len(paths))
	for i, p := range paths {
		var mt time.Time
		if fi, err := os.Stat(p); err == nil {
			mt = fi.ModTime()
		}
		items[i] = pathMtime{path: p, mtime: mt}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].mtime.After(items[j].mtime)
	})
	for i, item := range items {
		paths[i] = item.path
	}
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

// GetRecordByPath returns the FileRecord for path, or nil if not tracked.
func (t *Tracker) GetRecordByPath(ctx context.Context, path string) (*FileRecord, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	var record *FileRecord
	err = t.db.View(func(tx *bbolt.Tx) error {
		pathsBucket := tx.Bucket(bucketPaths)
		if pathsBucket == nil {
			return nil
		}
		hash := pathsBucket.Get([]byte(path))
		if hash == nil && absPath != path {
			hash = pathsBucket.Get([]byte(absPath))
		}
		if hash == nil {
			return nil
		}
		filesBucket := tx.Bucket(bucketFiles)
		if filesBucket == nil {
			return nil
		}
		val := filesBucket.Get(hash)
		if val == nil {
			return nil
		}
		var rec FileRecord
		if err := json.Unmarshal(val, &rec); err != nil {
			return err
		}
		record = &rec
		return nil
	})
	return record, err
}

// ListPathsInDir returns all tracked paths located inside dir.
func (t *Tracker) ListPathsInDir(ctx context.Context, dir string) ([]string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = filepath.Clean(dir)
	}
	var paths []string
	err = t.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketPaths)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			p := string(k)
			absP, err := filepath.Abs(p)
			if err != nil {
				absP = filepath.Clean(p)
			}
			if absP == absDir || strings.HasPrefix(absP, absDir+string(filepath.Separator)) {
				paths = append(paths, p)
			}
			return nil
		})
	})
	return paths, err
}

// RemovePath removes a path from tracking.
func (t *Tracker) RemovePath(ctx context.Context, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	return t.db.Update(func(tx *bbolt.Tx) error {
		pathsBucket := tx.Bucket(bucketPaths)
		if pathsBucket == nil {
			return nil
		}
		pathsBucket.Delete([]byte(path))
		if absPath != path {
			pathsBucket.Delete([]byte(absPath))
		}
		return nil
	})
}

// DetectChangedFiles compares the given currentFiles (all eligible files currently on disk in dir)
// against the tracker's recorded state. It returns:
// - changed: files that are new or whose content (SHA-256) differs from the recorded hash.
// - deleted: files previously tracked under dir that are no longer present in currentFiles.
// It uses fast mtime and file size heuristics to skip SHA-256 computation for untouched files.
func (t *Tracker) DetectChangedFiles(ctx context.Context, dir string, currentFiles []string) (changed []string, deleted []string, err error) {
	currentMap := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		absF, err := filepath.Abs(f)
		if err != nil {
			absF = filepath.Clean(f)
		}
		currentMap[absF] = true
	}

	trackedPaths, err := t.ListPathsInDir(ctx, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("list tracked paths: %w", err)
	}

	for _, tp := range trackedPaths {
		absTP, err := filepath.Abs(tp)
		if err != nil {
			absTP = filepath.Clean(tp)
		}
		if !currentMap[absTP] {
			deleted = append(deleted, tp)
		}
	}

	var touchedRecords []FileRecord
	for _, f := range currentFiles {
		rec, _ := t.GetRecordByPath(ctx, f)
		if rec == nil {
			changed = append(changed, f)
			continue
		}

		info, err := os.Stat(f)
		if err != nil {
			continue // skip broken symlinks or unreadable files
		}

		if info.ModTime().Unix() == rec.LastModified && info.Size() == rec.Size {
			continue
		}

		currHash, err := ComputeHash(f)
		if err != nil || currHash != rec.Hash {
			changed = append(changed, f)
		} else {
			touchedRecords = append(touchedRecords, FileRecord{
				Hash:         rec.Hash,
				Path:         f,
				LastModified: info.ModTime().Unix(),
				Size:         info.Size(),
			})
		}
	}

	if len(touchedRecords) > 0 {
		_ = t.BatchUpsertRecords(ctx, touchedRecords)
	}

	SortFilesByNewest(changed)
	return changed, deleted, nil
}

func (t *Tracker) Close() error {
	return t.db.Close()
}

