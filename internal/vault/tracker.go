package vault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// FileRecord represents a tracked file's metadata
type FileRecord struct {
	Hash         string
	Path         string
	LastModified int64
	Size         int64
}

// Tracker handles sqlite database mapping hash -> file path.
type Tracker struct {
	db *sql.DB
}

// DefaultDBPath returns the standard vault database location.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".gleann", "vault")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "vault.db")
}

// NewTracker initializes a new SQLite hash tracker.
func NewTracker(dbPath string) (*Tracker, error) {
	// Add busy_timeout so concurrent writes queue up instead of throwing SQLITE_BUSY
	dsn := dbPath + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := initDB(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Tracker{db: db}, nil
}

const currentSchemaVersion = 1

func initDB(db *sql.DB) error {
	// Create schema_version table
	schemaQuery := `
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY
	);
	`
	if _, err := db.Exec(schemaQuery); err != nil {
		return err
	}

	// Read current version
	var v int
	err := db.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			v = 0 // Brand new database
		} else {
			return err
		}
	}

	// Apply migrations
	for v < currentSchemaVersion {
		if v == 0 {
			// Initial v1 schema
			query := `
			CREATE TABLE IF NOT EXISTS files (
				hash TEXT PRIMARY KEY,
				path TEXT NOT NULL,
				last_modified INTEGER NOT NULL,
				size INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_path ON files(path);
			`
			if _, err := db.Exec(query); err != nil {
				return err
			}
		}
		// Future migrations would go here (e.g., if v == 1 -> upgrade to v=2)
		// ...

		v++
		if _, err := db.Exec("INSERT INTO schema_version (version) VALUES (?)", v); err != nil {
			return err
		}
	}
	return nil
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

	query := `
	INSERT INTO files (hash, path, last_modified, size)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(hash) DO UPDATE SET
		path = excluded.path,
		last_modified = excluded.last_modified,
		size = excluded.size;
	`
	_, err = t.db.ExecContext(ctx, query, hash, path, info.ModTime().Unix(), info.Size())
	return hash, err
}

// GetPathByHash finds the current actual path of a file, enabling robust recomputations.
func (t *Tracker) GetPathByHash(ctx context.Context, hash string) (string, error) {
	var path string
	err := t.db.QueryRowContext(ctx, "SELECT path FROM files WHERE hash = ?", hash).Scan(&path)
	return path, err
}

// GetHashByPath finds the hash mapped to a path (if tracking).
func (t *Tracker) GetHashByPath(ctx context.Context, path string) (string, error) {
	var hash string
	err := t.db.QueryRowContext(ctx, "SELECT hash FROM files WHERE path = ?", path).Scan(&hash)
	return hash, err
}

// RemoveByHash removes a hash from tracking.
func (t *Tracker) RemoveByHash(ctx context.Context, hash string) error {
	_, err := t.db.ExecContext(ctx, "DELETE FROM files WHERE hash = ?", hash)
	return err
}

func (t *Tracker) Close() error {
	return t.db.Close()
}
