package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tevfik/gleann/internal/analysis"
)

// LexQueryOptions configures lexical search execution (RFC 003 section 4.2).
// The zero value keeps today's behaviour: pure OR query, no document frequency cut.
type LexQueryOptions struct {
	// ANDFirst runs the query with all term groups AND-ed first (forms within a
	// group stay OR-ed: ("a" OR "a2") AND "b"); if it returns fewer than k rows,
	// it fills up with the OR query (deduped, keeping AND hits first in their
	// bm25 order, then OR hits; RFC 003 section 4.2 step 2).
	ANDFirst bool

	// DocFreqCut drops query terms whose document frequency is above this
	// fraction of N (total passages in the lexical index), using lex_vocab.
	// For example 0.05, 0.1, 0.2. <= 0 disables the cut. It never drops all
	// terms: at least the rarest term is kept (RFC 003 section 4.2 step 3).
	DocFreqCut float64
}

// LexQuery is one lexical query: the raw query text plus the analyzer that
// turns it into FTS5 terms (RFC 003 section 3.2). The analyzer, not FTS5,
// decides what a term is (section 4); FTS5 only splits the pre-tokenised
// expression on spaces. Options configures AND-first and document frequency cut.
type LexQuery struct {
	Text     string
	Analyzer analysis.Analyzer
	Options  LexQueryOptions
}

// ScoredID is one lexical hit: a passage ID (the lex rowid) and its score.
// Higher is better: Search negates FTS5's bm25(), which is lower-is-better.
type ScoredID struct {
	ID    int64
	Score float32
}

// LexStats describes the persisted lexical index.
type LexStats struct {
	Rows int
}

// Lexical is the persisted keyword index over the same database (RFC 003
// section 3.2). *Store implements it. Search and ScoreIDs answer from the
// contentless FTS5 table lex; Rebuild refills lex from passages.text and
// passages.kind without any embedding.
type Lexical interface {
	// Search returns passage IDs and scores, best first.
	Search(ctx context.Context, q LexQuery, k int) ([]ScoredID, error)
	// ScoreIDs scores only the given passage IDs, for the Scorer.Score path.
	ScoreIDs(ctx context.Context, q LexQuery, ids []int64) (map[int64]float32, error)
	// Rebuild refills lex from passages.text, no embedding.
	Rebuild(ctx context.Context, an analysis.Analyzer) error
	Stats(ctx context.Context) (LexStats, error)
}

var _ Lexical = (*Store)(nil)

// filterDocFreq drops query term groups whose document frequency exceeds
// cut * N, where N is the total row count of the lexical index. It queries
// the fts5vocab table lex_vocab for term document frequencies. It never
// drops all terms: if every term exceeds the cut, it keeps the rarest term
// (RFC 003 section 4.2 step 3).
func (s *Store) filterDocFreq(ctx context.Context, groups [][]string, cut float64) ([][]string, error) {
	if cut <= 0 || len(groups) == 0 {
		return groups, nil
	}
	n := s.lexRows.Load()
	if n <= 0 {
		if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM lex`).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting lexical rows for df cut: %w", err)
		}
		s.lexRows.Store(n)
	}
	if n == 0 {
		return nil, nil
	}
	threshold := cut * float64(n)

	stmt, err := s.reader.PrepareContext(ctx, `SELECT doc FROM lex_vocab WHERE term = ?`)
	if err != nil {
		return nil, fmt.Errorf("preparing lex_vocab query: %w", err)
	}
	defer stmt.Close()

	var kept [][]string
	var minDF int64 = -1
	var rarestGroup []string

	for _, group := range groups {
		var maxDoc int64
		for _, form := range group {
			if form == "" {
				continue
			}
			var doc int64
			err := stmt.QueryRowContext(ctx, form).Scan(&doc)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("querying term doc frequency for %q: %w", form, err)
			}
			if doc > maxDoc {
				maxDoc = doc
			}
		}
		if minDF == -1 || maxDoc < minDF {
			minDF = maxDoc
			rarestGroup = group
		}
		if float64(maxDoc) <= threshold {
			kept = append(kept, group)
		}
	}

	if len(kept) > 0 {
		return kept, nil
	}
	if rarestGroup != nil {
		return [][]string{rarestGroup}, nil
	}
	return nil, nil
}

// searchExpr executes a single FTS5 MATCH query and returns the scored hits.
func (s *Store) searchExpr(ctx context.Context, expr string, k int) ([]ScoredID, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT rowid, bm25(lex) FROM lex WHERE lex MATCH ? ORDER BY bm25(lex), rowid LIMIT ?`, expr, k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScoredID
	for rows.Next() {
		var id int64
		var bm25 float64
		if err := rows.Scan(&id, &bm25); err != nil {
			return nil, fmt.Errorf("scanning hit: %w", err)
		}
		out = append(out, ScoredID{ID: id, Score: float32(-bm25)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Search returns the top-k lexical hits, best first. FTS5's bm25() is
// lower-is-better, so the score is negated to make higher better and match the
// rest of gleann's scorers. Ties are ordered by rowid for a deterministic list
// (RFC 003 section 4.2). A query that tokenises to nothing (empty, only stop
// words, punctuation or one-character words) returns no rows without running
// SQL: FTS5 rejects an empty MATCH expression with a syntax error.
//
// When q.Options.DocFreqCut > 0, query terms whose document frequency exceeds
// that fraction of N are dropped, keeping at least the rarest term (step 3).
// When q.Options.ANDFirst is set, all term groups are AND-ed first; if fewer
// than k hits return, the remaining slots are filled with the OR query (step 2).
func (s *Store) Search(ctx context.Context, q LexQuery, k int) ([]ScoredID, error) {
	if k <= 0 {
		return nil, nil
	}
	groups := q.Analyzer.QueryTokens(q.Text)
	var cleanGroups [][]string
	for _, g := range groups {
		var forms []string
		for _, f := range g {
			if f != "" {
				forms = append(forms, f)
			}
		}
		if len(forms) > 0 {
			cleanGroups = append(cleanGroups, forms)
		}
	}
	groups = cleanGroups
	if len(groups) == 0 {
		return nil, nil
	}

	if q.Options.DocFreqCut > 0 {
		var err error
		groups, err = s.filterDocFreq(ctx, groups, q.Options.DocFreqCut)
		if err != nil {
			return nil, fmt.Errorf("lexical search: %w", err)
		}
		if len(groups) == 0 {
			return nil, nil
		}
	}

	if q.Options.ANDFirst {
		andExpr := analysis.MatchExprAND(groups)
		if andExpr == "" {
			return nil, nil
		}
		andHits, err := s.searchExpr(ctx, andExpr, k)
		if err != nil {
			return nil, fmt.Errorf("lexical search (AND): %w", err)
		}
		if len(andHits) >= k {
			return andHits, nil
		}

		// Fewer than k hits returned: fill up with the OR query (deduped,
		// keeping AND hits first in their bm25 order, then OR hits).
		orExpr := analysis.MatchExpr(groups)
		if orExpr == "" || orExpr == andExpr {
			return andHits, nil
		}
		orHits, err := s.searchExpr(ctx, orExpr, k)
		if err != nil {
			return nil, fmt.Errorf("lexical search (OR fill): %w", err)
		}

		seen := make(map[int64]bool, len(andHits)+len(orHits))
		for _, h := range andHits {
			seen[h.ID] = true
		}
		out := andHits
		var fill []ScoredID
		for _, h := range orHits {
			if !seen[h.ID] {
				seen[h.ID] = true
				fill = append(fill, h)
				if len(out)+len(fill) == k {
					break
				}
			}
		}
		// The AND and OR bm25 scores come from two queries and are not
		// comparable. Shift the fill below the weakest AND hit so that the
		// scores keep the returned order: callers that re-sort by score
		// (format-2 SearchBM25) must not undo AND-first.
		if len(andHits) > 0 && len(fill) > 0 {
			floor := andHits[len(andHits)-1].Score
			if top := fill[0].Score; top >= floor {
				shift := top - floor + 1e-3
				for i := range fill {
					fill[i].Score -= shift
				}
			}
		}
		return append(out, fill...), nil
	}

	expr := analysis.MatchExpr(groups)
	if expr == "" {
		return nil, nil
	}
	out, err := s.searchExpr(ctx, expr, k)
	if err != nil {
		return nil, fmt.Errorf("lexical search: %w", err)
	}
	return out, nil
}

// ScoreIDs scores the query against only the given passage IDs and returns a
// score per matched ID (higher is better). IDs with no match are absent from
// the map. The IN list is chunked to maxInParams placeholders so a large
// candidate set stays below SQLite's host-parameter limit. An empty query or
// empty ID list returns an empty map without running SQL.
func (s *Store) ScoreIDs(ctx context.Context, q LexQuery, ids []int64) (map[int64]float32, error) {
	out := make(map[int64]float32)
	if len(ids) == 0 {
		return out, nil
	}
	groups := q.Analyzer.QueryTokens(q.Text)
	if q.Options.DocFreqCut > 0 {
		var err error
		groups, err = s.filterDocFreq(ctx, groups, q.Options.DocFreqCut)
		if err != nil {
			return nil, fmt.Errorf("scoring passage ids: %w", err)
		}
	}
	expr := analysis.MatchExpr(groups)
	if expr == "" {
		return out, nil
	}
	for start := 0; start < len(ids); start += maxInParams {
		chunk := ids[start:min(start+maxInParams, len(ids))]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, expr)
		for _, id := range chunk {
			args = append(args, id)
		}
		if err := func() error {
			rows, err := s.reader.QueryContext(ctx,
				fmt.Sprintf(`SELECT rowid, bm25(lex) FROM lex WHERE lex MATCH ? AND rowid IN (%s)`, placeholders), args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id int64
				var bm25 float64
				if err := rows.Scan(&id, &bm25); err != nil {
					return err
				}
				out[id] = float32(-bm25)
			}
			return rows.Err()
		}(); err != nil {
			return nil, fmt.Errorf("scoring passage ids: %w", err)
		}
	}
	return out, nil
}

// TermScore is one query term's share of a passage's lexical score.
type TermScore struct {
	Term  string
	TF    int
	Score float64
}

// ExplainTerms breaks the lexical score of passage id down by query term. Each
// term group (a term with its folded forms) is scored on its own, which is its
// exact contribution because FTS5's bm25 is a sum over the query's phrases;
// TF counts the group's forms among the passage's tokens. The groups are the
// ones Search and ScoreIDs use (after the document frequency cut). Terms that
// do not occur in the passage come last with score 0.
func (s *Store) ExplainTerms(ctx context.Context, q LexQuery, id int64) ([]TermScore, error) {
	ps, err := s.GetMany(ctx, []int64{id})
	if err != nil {
		return nil, fmt.Errorf("explaining terms: %w", err)
	}
	if len(ps) == 0 {
		return nil, nil
	}
	groups := q.Analyzer.QueryTokens(q.Text)
	if q.Options.DocFreqCut > 0 {
		groups, err = s.filterDocFreq(ctx, groups, q.Options.DocFreqCut)
		if err != nil {
			return nil, fmt.Errorf("explaining terms: %w", err)
		}
	}
	counts := make(map[string]int)
	for _, tok := range q.Analyzer.Tokens(ps[0].Text, analysis.Kind(ps[0].Kind)) {
		counts[tok]++
	}
	var out []TermScore
	for _, g := range groups {
		var forms []string
		for _, f := range g {
			if f != "" {
				forms = append(forms, f)
			}
		}
		if len(forms) == 0 {
			continue
		}
		ts := TermScore{Term: forms[0]}
		for _, f := range forms {
			ts.TF += counts[f]
		}
		scores, err := s.scoreGroup(ctx, forms, id)
		if err != nil {
			return nil, fmt.Errorf("explaining terms: %w", err)
		}
		ts.Score = float64(scores)
		out = append(out, ts)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// scoreGroup scores one term group (forms OR-ed) against one passage; 0 when
// the passage does not match.
func (s *Store) scoreGroup(ctx context.Context, forms []string, id int64) (float32, error) {
	expr := analysis.MatchExpr([][]string{forms})
	if expr == "" {
		return 0, nil
	}
	var bm25 float64
	err := s.reader.QueryRowContext(ctx, `SELECT bm25(lex) FROM lex WHERE lex MATCH ? AND rowid = ?`, expr, id).Scan(&bm25)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return float32(-bm25), nil
}

// Rebuild deletes every lex row and re-inserts tokens computed from
// passages.text and passages.kind, then records the analyzer identity and
// language in meta, all in one transaction: an interrupted rebuild leaves the
// previous lexical index intact, and readers see either the old or the new
// one. It needs no embedding and no source files (RFC 003 section 4.1 item
// 1): a stored analyzer or language mismatch is repaired from the stored text.
func (s *Store) Rebuild(ctx context.Context, an analysis.Analyzer) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rebuilding lexical index: beginning transaction: %w", err)
	}
	defer tx.Rollback()
	// contentless_delete=1 makes a plain DELETE work on the contentless table.
	if _, err := tx.ExecContext(ctx, `DELETE FROM lex`); err != nil {
		return fmt.Errorf("rebuilding lexical index: clearing lex: %w", err)
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO lex(rowid, tokens) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("rebuilding lexical index: preparing insert: %w", err)
	}
	defer ins.Close()
	// The passages are read through the same transaction, so the scan sees
	// exactly the rows being indexed; SQLite allows the insert into another
	// table while the scan is open.
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, text FROM passages ORDER BY id`)
	if err != nil {
		return fmt.Errorf("rebuilding lexical index: reading passages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind, text string
		if err := rows.Scan(&id, &kind, &text); err != nil {
			return fmt.Errorf("rebuilding lexical index: scanning passage: %w", err)
		}
		tokens := strings.Join(an.Tokens(text, analysis.Kind(kind)), " ")
		if tokens == "" {
			continue
		}
		if _, err := ins.ExecContext(ctx, id, tokens); err != nil {
			return fmt.Errorf("rebuilding lexical index: inserting lex row %d: %w", id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rebuilding lexical index: reading passages: %w", err)
	}
	for k, v := range map[string]string{"analyzer": analysis.ID, "language": an.Language} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return fmt.Errorf("rebuilding lexical index: writing meta %q: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rebuilding lexical index: committing: %w", err)
	}
	s.lexRows.Store(0)
	return nil
}

// Stats reports the number of rows in the lexical index.
func (s *Store) Stats(ctx context.Context) (LexStats, error) {
	var n int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM lex`).Scan(&n); err != nil {
		return LexStats{}, fmt.Errorf("counting lexical rows: %w", err)
	}
	s.lexRows.Store(int64(n))
	return LexStats{Rows: n}, nil
}
