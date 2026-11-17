package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// LiveSlots returns the subset of slots that a passage currently occupies.
// The vector search uses it to drop hits of dead slots (a slot whose passage
// was removed or replaced; RFC 005 section 3.2) in one indexed lookup.
func (s *Store) LiveSlots(ctx context.Context, slots []int64) (map[int64]bool, error) {
	live := make(map[int64]bool, len(slots))
	for start := 0; start < len(slots); start += maxInParams {
		chunk := slots[start:min(start+maxInParams, len(slots))]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, v := range chunk {
			args[i] = v
		}
		rows, err := s.reader.QueryContext(ctx, `SELECT vec_slot FROM passages WHERE vec_slot IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("looking up live slots: %w", err)
		}
		for rows.Next() {
			var slot int64
			if err := rows.Scan(&slot); err != nil {
				rows.Close()
				return nil, fmt.Errorf("looking up live slots: %w", err)
			}
			live[slot] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("looking up live slots: %w", err)
		}
		rows.Close()
	}
	return live, nil
}

// AllSlots returns every occupied vec_slot in ascending order.
func (s *Store) AllSlots(ctx context.Context) ([]int64, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT vec_slot FROM passages WHERE vec_slot IS NOT NULL ORDER BY vec_slot`)
	if err != nil {
		return nil, fmt.Errorf("listing slots: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var slot int64
		if err := rows.Scan(&slot); err != nil {
			return nil, fmt.Errorf("listing slots: %w", err)
		}
		out = append(out, slot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing slots: %w", err)
	}
	return out, nil
}

// SourceSlots returns the vec_slots of the passages of the given sources
// (ascending) and how many passages those sources hold, slotless ones
// included. An update uses it to learn which slots its removals and
// replacements are about to kill (RFC 005 section 3).
func (s *Store) SourceSlots(ctx context.Context, sources []string) (slots []int64, passages int, err error) {
	for start := 0; start < len(sources); start += maxInParams {
		chunk := sources[start:min(start+maxInParams, len(sources))]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, v := range chunk {
			args[i] = v
		}
		rows, qerr := s.reader.QueryContext(ctx, `SELECT vec_slot FROM passages WHERE source IN (`+placeholders+`)`, args...)
		if qerr != nil {
			return nil, 0, fmt.Errorf("listing slots of sources: %w", qerr)
		}
		for rows.Next() {
			var slot sql.NullInt64
			if err := rows.Scan(&slot); err != nil {
				rows.Close()
				return nil, 0, fmt.Errorf("listing slots of sources: %w", err)
			}
			passages++
			if slot.Valid {
				slots = append(slots, slot.Int64)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("listing slots of sources: %w", err)
		}
		rows.Close()
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	return slots, passages, nil
}
