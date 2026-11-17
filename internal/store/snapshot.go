package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MetaSnapshot returns the values of the given metadata keys in one read
// (absent keys are left out of the map), so a reader can compare a handful of
// keys against an earlier snapshot without a query per key. It reads through
// the reader pool, which in WAL mode never blocks a writer, and it sees the
// commits of other processes.
func (s *Store) MetaSnapshot(ctx context.Context, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := s.reader.QueryContext(ctx, `SELECT key, value FROM meta WHERE key IN (`+ph+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading meta keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("reading meta keys: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading meta keys: %w", err)
	}
	return out, nil
}

// HasDocNodes reports whether the document graph has any node. It is an
// indexed probe (LIMIT 1), not a count.
func (s *Store) HasDocNodes(ctx context.Context) (bool, error) {
	var one int
	err := s.reader.QueryRowContext(ctx, `SELECT 1 FROM doc_nodes LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probing document nodes: %w", err)
	}
	return true, nil
}
