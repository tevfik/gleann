package gleann

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// filterPoolCap bounds the widen-until-k candidate pool (WP-2.5). With
// metadata filters the pool starts at the filtered retrieveK (at least
// max(4*topK, 50)) and grows (by the observed pass rate, see nextFilterPool) until k results pass the filter, every
// pre-selected match has been examined, the retrieval legs are exhausted, or
// the pool reaches this cap. At 4096 candidates a filter matching 0.25% of
// the corpus still fills k=10 in expectation, while a filter that matches
// nothing costs a bounded ~8K candidate examinations instead of a full scan.
const filterPoolCap = 4096

// filterPreselectCap bounds the exact format-2 pre-selection fallback (WP-2.5):
// it runs only after the widen-until-k loop hits filterPoolCap with fewer than k
// results, reads at most this many matching rows (plus one to detect overflow)
// and verifies them against the engine. A filter matching more rows than this is
// not selective, so the loop's result stands and the scan is abandoned. The
// pre-selection reads only id/vec_slot/source/kind/meta — never passage text.
const filterPreselectCap = 50000

// filterExactMax is the estimated number of matching passages up to which a
// restrictive filter (see filterExactSelectivity) is answered by exact scoring
// of the pre-selected matches, even when the widen-until-k loop already found k
// results. Those k results come from an approximate graph search over a
// widened pool, so they are not necessarily the true top-k among the matches:
// on 100K x 768 default-config hnsw, recall@10 was 0.86 at 1% selectivity and
// 0.93 at 10%, with the loop widening to 4096 candidates (336 ms p50 at 1%).
// The estimate is the loop's observed pass rate times the passage count; the
// loop scores the matches exactly as soon as it says "few" (at most once per
// query), and a pre-selection that finds more than twice this many rows is
// abandoned (the estimate was off): the loop then widens as before.
const filterExactMax = 12000

// filterExactSelectivity: exact scoring applies only to filters matching at
// most 1/filterExactSelectivity of the passages. A less restrictive filter
// leaves the widened pool holding the true top-k (recall >= 0.99 at 50%), and
// the pre-selection scan would only add latency.
const filterExactSelectivity = 5

// filterExactScanMax bounds the index size for which a filter with few matches
// may cost a pass over the passages table although the loop already found k
// results. Without a filter index (preselectIndexed) the SQL pre-selection
// evaluates the filter on every row, so its cost grows with the index, not
// with the matches: about 60 ms at 100K passages (filtbench, 100K x 768).
// Up to this size a missing filter index is created on demand (one pass, once
// per field) and a filter the indexes cannot answer is evaluated on every
// row. Above it only the filter indexes that already exist are used and the
// loop's result stands otherwise, as before; the cap fallback (fewer than k
// results after filterPoolCap) still pre-selects at any size.
const filterExactScanMax = 200_000

// preselectFilterIDs answers a metadata filter from the format-2 record store
// (WP-2.5). It first asks the filter indexes (preselectIndexed), whose cost
// follows the number of matches. When they cannot answer, it compiles the
// filters to a SQL WHERE clause over the passages
// table, reads the matching rows (id, vec_slot, source, kind, meta — never text)
// up to filterPreselectCap+1 and verifies each against the authoritative filter
// engine. The returned map is therefore EXACT: it maps each matching passage's
// searcher ID (vec_slot, or the negative rowid for a slotless one) to its lex
// rowid, so the caller can score the matches directly without a second
// retrieval. An empty map proves that no passage matches.
//
// The second return value reports whether pre-selection ran at all. It is false
// for a passage store that is not the SQLite store (tests), for filters the compiler cannot express (numeric
// comparisons, ext/extension resolution, chunk_index), when the filter matches
// more than filterPreselectCap rows (not selective — the widen loop's result
// stands), and on any store error; the caller then keeps the loop's results.
func (s *LeannSearcher) preselectFilterIDs(ctx context.Context, engine *MetadataFilterEngine) (map[int64]int64, bool) {
	return s.preselectFilterIDsLimit(ctx, engine, filterPreselectCap, true)
}

// preselectFilterIDsLimit is preselectFilterIDs with an explicit row limit:
// a filter matching more than limit rows reports ran=false. tablePass says
// whether a pass over the whole passages table is acceptable: to create a
// missing filter index, or to evaluate a filter the indexes cannot answer.
// Without it only existing filter indexes answer.
func (s *LeannSearcher) preselectFilterIDsLimit(ctx context.Context, engine *MetadataFilterEngine, limit int, tablePass bool) (map[int64]int64, bool) {
	sp, ok := s.passages.(*storePassages)
	if !ok {
		return nil, false
	}
	switch allowed, outcome := s.preselectIndexed(ctx, sp, engine, limit, tablePass); outcome {
	case preselectDone:
		return allowed, true
	case preselectNotSelective:
		return nil, false
	}
	if !tablePass {
		return nil, false
	}
	return s.preselectScan(ctx, sp, engine, limit)
}

// preselectScan is the pre-selection without filter indexes: the compiled
// WHERE clause is evaluated on every row of the passages table.
func (s *LeannSearcher) preselectScan(ctx context.Context, sp *storePassages, engine *MetadataFilterEngine, limit int) (map[int64]int64, bool) {
	where, args, ok := compileFilterSQL(engine.Filters, engine.Logic)
	if !ok {
		return nil, false
	}
	ps, err := sp.st.SelectByPredicate(ctx, where, args, limit+1)
	if err != nil {
		// A predicate the store cannot answer (e.g. malformed meta JSON)
		// degrades to the loop, never to wrong results.
		return nil, false
	}
	if len(ps) > limit {
		// The filter is not selective: reading every match would cost more
		// than the loop already spent. Keep the loop's result.
		return nil, false
	}
	allowed := make(map[int64]int64, len(ps))
	for _, p := range ps {
		gp := storePassageToGleann(p)
		if engine.Match(gp.Metadata) {
			allowed[gp.ID] = p.ID
		}
	}
	return allowed, true
}

// filterCandidate is one step of a field's resolution chain, mirroring
// resolveMetadataField: the engine takes the FIRST candidate present in the
// passage metadata. In the format-2 store a meta key lives in the meta JSON
// column; the source column re-attaches as metadata["source"] only when it is
// non-empty (storePassageToGleann).
type filterCandidate struct {
	metaKey string // resolve from json_extract(meta, ?) with the bound path '$."<metaKey>"'
	column  bool   // resolve from the source column (present iff source <> '')
}

// path is the JSON path of the meta key, passed to SQLite as a BOUND
// parameter — never interpolated into the SQL text — so no field name can
// change the statement.
func (c filterCandidate) path() string {
	return "$.\"" + c.metaKey + "\""
}

// filterResolutionChain maps a filter field to its resolution chain. Fields
// whose resolution the SQL compiler cannot mirror exactly (ext/extension
// derive from filepath.Ext, chunk_index is the chunk_no column with numeric
// semantics) return nil and fall back to the loop.
func filterResolutionChain(field string) []filterCandidate {
	switch field {
	case "":
		return nil
	case "source":
		// A passage without a source column keeps a "source" key of its meta
		// JSON as metadata["source"] (storePassageToGleann overrides it only
		// with a non-empty column), so the key comes right after the column.
		return []filterCandidate{{column: true}, {metaKey: "source"}, {metaKey: "file"}}
	case "file":
		return []filterCandidate{{metaKey: "file"}, {column: true}, {metaKey: "source"}}
	case "type":
		return []filterCandidate{{metaKey: "type"}, {metaKey: "kind"}}
	case "kind":
		return []filterCandidate{{metaKey: "kind"}, {metaKey: "type"}}
	case "ext", "extension", "chunk_index":
		return nil
	default:
		return []filterCandidate{{metaKey: field}}
	}
}

// compileFilterSQL compiles filters to a SQL WHERE clause over the passages
// table whose row set is a SUPERSET of the engine's matches: every row the
// engine would match is selected, and rows SQL cannot decide exactly (a
// non-text JSON value where the engine uses fmt.Sprint/numeric coercion) are
// selected too and dropped by the engine verification in preselectFilterIDs.
// A superset keeps the k-guarantee; a subset would silently lose matches.
//
// Compilable: the string-comparison operators (eq, ne, in, nin, contains,
// startswith, endswith, exists) on fields with a mirrorable resolution chain.
// Numeric operators (gt, gte, lt, lte) and unknown operators return ok=false.
// Text comparisons are exact: the engine compares Go strings byte-wise and so
// does SQLite (BINARY collation, BLOB casts for the substring operators).
func compileFilterSQL(filters []MetadataFilter, logic string) (string, []any, bool) {
	if len(filters) == 0 {
		return "", nil, false
	}
	var branches []string
	var args []any
	for _, f := range filters {
		branch, bargs, ok := compileFilterBranch(f)
		if !ok {
			return "", nil, false
		}
		branches = append(branches, "("+branch+")")
		args = append(args, bargs...)
	}
	join := " AND "
	if logic == "or" {
		join = " OR "
	}
	return strings.Join(branches, join), args, true
}

// compileFilterBranch compiles one filter to a superset predicate.
func compileFilterBranch(f MetadataFilter) (string, []any, bool) {
	chain := filterResolutionChain(f.Field)
	if chain == nil {
		return "", nil, false
	}
	// The field name travels as a bound JSON-path parameter, so it cannot
	// escape into the SQL text. Reject keys the JSON path syntax itself
	// cannot quote (the engine would read them as plain map keys, SQL
	// cannot).
	if strings.ContainsAny(f.Field, "\"\\") || strings.IndexFunc(f.Field, func(r rune) bool { return r < 0x20 }) >= 0 {
		return "", nil, false
	}

	if f.Operator == OpExists {
		want, _ := f.Value.(bool) // non-bool means false, as in evaluateFilter
		var parts []string
		var args []any
		for _, c := range chain {
			p, pa := candidatePresent(c)
			parts = append(parts, p)
			args = append(args, pa...)
		}
		if want {
			return strings.Join(parts, " OR "), args, true
		}
		for i, p := range parts {
			parts[i] = "NOT (" + p + ")"
		}
		return strings.Join(parts, " AND "), args, true
	}

	pred, ok := compileStringPred(f.Operator, f.Value)
	if !ok {
		return "", nil, false
	}
	// Resolution: the first present candidate wins. A candidate branch
	// requires its own presence, the absence of every earlier candidate, and
	// either a non-text value (verification decides) or the exact text
	// predicate. The source column is always text, so its branch is exact.
	var parts []string
	var args []any
	for i, c := range chain {
		var b strings.Builder
		var bargs []any
		p, pa := candidatePresent(c)
		b.WriteString(p)
		bargs = append(bargs, pa...)
		for _, prev := range chain[:i] {
			pp, ppa := candidatePresent(prev)
			b.WriteString(" AND NOT (" + pp + ")")
			bargs = append(bargs, ppa...)
		}
		if c.column {
			ps, psa := pred("source", nil)
			b.WriteString(" AND " + ps)
			bargs = append(bargs, psa...)
		} else {
			e, ea := candidateExpr(c)
			t, ta := candidateIsText(c)
			if valueNeedlesDecodeApart(f.Value) {
				// every text value of the key is verified
				b.WriteString(" AND 1")
			} else {
				ps, psa := pred(e, ea)
				b.WriteString(" AND (" + t + " = 0 OR " + ps + ")")
				bargs = append(bargs, ta...)
				bargs = append(bargs, psa...)
			}
		}
		parts = append(parts, "("+b.String()+")")
		args = append(args, bargs...)
	}
	return strings.Join(parts, " OR "), args, true
}

// candidatePresent is the SQL presence test of one resolution candidate,
// mirroring the ok of resolveMetadataField: a meta key is present when
// json_type is non-NULL (a JSON null counts as present, the engine sees a nil
// value with ok=true); the source column is present when non-empty.
func candidatePresent(c filterCandidate) (string, []any) {
	if c.column {
		return "source <> ''", nil
	}
	return "json_type(meta, ?) IS NOT NULL", []any{c.path()}
}

func candidateExpr(c filterCandidate) (string, []any) {
	return "json_extract(meta, ?)", []any{c.path()}
}

// candidateIsText is 1 when the candidate's value is a JSON string that SQL
// reads as the engine does. The engine's fmt.Sprint of a string is the string
// itself, so text values compare exactly in SQL; every other JSON type goes to
// engine verification, and so does a key that occurs twice in the object (Go
// keeps the last value, json_type and json_extract the first; json_remove
// deletes the first occurrence only).
func candidateIsText(c filterCandidate) (string, []any) {
	p := c.path()
	return "(json_type(meta, ?) = 'text' AND json_type(json_remove(meta, ?), ?) IS NULL)", []any{p, p, p}
}

// needleDecodesApart reports whether a needle can tell SQL's reading of a JSON
// string from Go's apart: Go reads a byte that is not UTF-8 as U+FFFD, SQLite
// keeps it. Such a needle makes the text comparison unreliable on the rows
// that have such bytes (and there is no telling which), so the rows of the key
// go to engine verification.
func needleDecodesApart(s string) bool {
	return !utf8.ValidString(s) || strings.ContainsRune(s, utf8.RuneError)
}

// valueNeedlesDecodeApart applies needleDecodesApart to the needles of a
// filter value: the value itself, or the items of an in/nin list.
func valueNeedlesDecodeApart(value any) bool {
	switch v := value.(type) {
	case []any:
		for _, it := range v {
			if needleDecodesApart(fmt.Sprint(it)) {
				return true
			}
		}
	case []string:
		for _, it := range v {
			if needleDecodesApart(it) {
				return true
			}
		}
	}
	return needleDecodesApart(fmt.Sprint(value))
}

// compileStringPred returns a predicate builder over a SQL value expression
// and its bound args, mirroring evaluateFilter's string operators. The builder
// interleaves the expression's args with its own in placeholder order; a
// constant predicate (empty needle, empty list) embeds no expression and so
// consumes no expression args. The BLOB casts make instr/substr byte-wise like
// Go's strings package (SQLite's TEXT variants count characters). An empty
// needle matches everything, as strings.Contains/HasPrefix/HasSuffix do.
func compileStringPred(op FilterOperator, value any) (func(expr string, exprArgs []any) (string, []any), bool) {
	needle := fmt.Sprint(value)
	constant := func(sql string) func(string, []any) (string, []any) {
		return func(string, []any) (string, []any) { return sql, nil }
	}
	switch op {
	case OpEqual:
		return func(e string, ea []any) (string, []any) {
			return "CAST(" + e + " AS TEXT) = ?", append(append([]any(nil), ea...), needle)
		}, true
	case OpNotEqual:
		return func(e string, ea []any) (string, []any) {
			return "CAST(" + e + " AS TEXT) <> ?", append(append([]any(nil), ea...), needle)
		}, true
	case OpIn, OpNotIn:
		var items []string
		switch v := value.(type) {
		case []any:
			for _, it := range v {
				items = append(items, fmt.Sprint(it))
			}
		case []string:
			items = v
		default:
			return nil, false // compareIn matches nothing; let the loop decide
		}
		if len(items) == 0 {
			// compareIn over an empty list is false for every value, so in
			// matches nothing and nin matches every present value.
			if op == OpNotIn {
				return constant("1"), true
			}
			return constant("0"), true
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(items)), ",")
		kw := " IN ("
		if op == OpNotIn {
			kw = " NOT IN ("
		}
		return func(e string, ea []any) (string, []any) {
			args := append([]any(nil), ea...)
			for _, it := range items {
				args = append(args, it)
			}
			return "CAST(" + e + " AS TEXT)" + kw + ph + ")", args
		}, true
	case OpContains:
		if needle == "" {
			return constant("1"), true
		}
		return func(e string, ea []any) (string, []any) {
			return "instr(CAST(" + e + " AS BLOB), CAST(? AS BLOB)) > 0", append(append([]any(nil), ea...), needle)
		}, true
	case OpStartsWith:
		if needle == "" {
			return constant("1"), true
		}
		return func(e string, ea []any) (string, []any) {
			return "substr(CAST(" + e + " AS BLOB), 1, length(CAST(? AS BLOB))) = CAST(? AS BLOB)", append(append([]any(nil), ea...), needle, needle)
		}, true
	case OpEndsWith:
		if needle == "" {
			return constant("1"), true
		}
		return func(e string, ea []any) (string, []any) {
			return "substr(CAST(" + e + " AS BLOB), -length(CAST(? AS BLOB))) = CAST(? AS BLOB)", append(append([]any(nil), ea...), needle, needle)
		}, true
	default:
		return nil, false
	}
}

// nextFilterPool sizes the next round of the widen-until-k loop. Doubling
// alone re-ran the whole retrieval up to seven times for a 3% filter (four
// times the cost of an unfiltered query). Instead the observed pass rate
// (passed of examined candidates) estimates the pool that yields k, with a
// 1.5x margin; with no pass yet the pool grows 8x. The pool always at least
// doubles and never exceeds filterPoolCap.
func nextFilterPool(pool, passed, examined, k int) int {
	next := pool * 8
	if passed > 0 && examined > 0 {
		next = int(math.Ceil(float64(k) * float64(examined) / float64(passed) * 1.5))
	}
	return min(max(next, pool*2), filterPoolCap)
}

// filterExactMatches estimates how many passages match the filter from the last
// round of the widen-until-k loop: passed of examined candidates matched, in an
// index of total passages. It returns -1 when no estimate is possible.
func filterExactMatches(passed, examined, total int) int {
	if examined <= 0 || total <= 0 {
		return -1
	}
	return int(math.Ceil(float64(passed) / float64(examined) * float64(total)))
}

// topScores returns the n best scored entries of scores (the map itself when it
// has at most n), ties broken by the lower ID so the cut is deterministic.
func topScores(scores map[int64]float32, n int) map[int64]float32 {
	if len(scores) <= n {
		return scores
	}
	ids := make([]int64, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return ids[i] < ids[j]
	})
	out := make(map[int64]float32, n)
	for _, id := range ids[:n] {
		out[id] = scores[id]
	}
	return out
}

// filterFewMatches reports whether the filter is estimated to match at most
// filterExactMax passages (see filterExactMatches) on a format-2 index, so that
// exact scoring of the pre-selected matches is cheaper and more accurate than
// widening the approximate pool further. tablePass says whether the index is
// small enough for the pre-selection to cost a pass over the passages table
// (filterExactScanMax); a larger index is pre-selected from its existing
// filter indexes only.
func (s *LeannSearcher) filterFewMatches(passed, examined int) (few, tablePass bool) {
	if s.storeFormat != 2 {
		return false, false
	}
	total := s.passages.Count()
	return filterExactWorthIt(passed, examined, total), total <= filterExactScanMax
}

// filterExactWorthIt is the decision behind filterFewMatches: passed of
// examined candidates matched in an index of total passages.
func filterExactWorthIt(passed, examined, total int) bool {
	// No candidate passed yet: assume one did. A filter whose matches rank
	// deep (or are rare) then goes to exact scoring after the first round
	// instead of widening to filterPoolCap; if the estimate is off, the
	// pre-selection finds too many rows and the loop widens as before.
	est := filterExactMatches(max(passed, 1), examined, total)
	// A filter matching a fifth of the index or more is not restrictive: the
	// widened pool holds the true top-k (recall >= 0.99 at 50%).
	return est >= 0 && est <= filterExactMax && est*filterExactSelectivity <= total
}
