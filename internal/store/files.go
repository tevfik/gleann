package store

import (
	"context"
	"database/sql"
	"fmt"
)

// FileRow is the manifest row of one indexed source file (the files table,
// RFC 003 section 3.4). It replaces <name>.manifest.json and the graph hash
// store, and is committed in the same transaction as the file's passages so
// a killed build leaves a consistent, resumable state.
type FileRow struct {
	Path       string
	Size       int64
	MtimeNS    int64
	Hash       string // same value as ManifestEntry.Hash
	GraphHash  string // hash the graph rows were built from; "" when NULL (never built)
	GraphDirty bool   // graph rows need re-extraction (RFC 003 section 6.5)
	IndexedAt  int64  // unix nanoseconds, matching the manifest's ModTime convention
}

const fileCols = `path, size, mtime_ns, hash, graph_hash, graph_dirty, indexed_at`

func scanFile(sc scanner) (FileRow, error) {
	var f FileRow
	var graphHash sql.NullString
	var dirty int
	if err := sc.Scan(&f.Path, &f.Size, &f.MtimeNS, &f.Hash, &graphHash, &dirty, &f.IndexedAt); err != nil {
		return FileRow{}, err
	}
	f.GraphHash = graphHash.String
	f.GraphDirty = dirty != 0
	return f, nil
}

// upsertFile writes the manifest row inside the caller's transaction. On an
// existing row only the content-manifest columns advance: graph_hash and
// graph_dirty belong to the graph delta (RFC 003 section 6.5), which is
// committed by the graph store, not by ApplySource.
func upsertFile(ctx context.Context, tx *sql.Tx, f FileRow) error {
	var graphHash any
	if f.GraphHash != "" {
		graphHash = f.GraphHash
	}
	dirty := 0
	if f.GraphDirty {
		dirty = 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO files (`+fileCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(path) DO UPDATE SET
    size = excluded.size,
    mtime_ns = excluded.mtime_ns,
    hash = excluded.hash,
    indexed_at = excluded.indexed_at`,
		f.Path, f.Size, f.MtimeNS, f.Hash, graphHash, dirty, f.IndexedAt)
	if err != nil {
		return fmt.Errorf("writing files row for %q: %w", f.Path, err)
	}
	return nil
}

func deleteFile(ctx context.Context, tx *sql.Tx, path string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path); err != nil {
		return fmt.Errorf("deleting files row for %q: %w", path, err)
	}
	return nil
}

// Files returns the manifest rows keyed by path.
func (s *Store) Files(ctx context.Context) (map[string]FileRow, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+fileCols+` FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}
	defer rows.Close()
	out := make(map[string]FileRow)
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, fmt.Errorf("listing files: %w", err)
		}
		out[f.Path] = f
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}
	return out, nil
}
