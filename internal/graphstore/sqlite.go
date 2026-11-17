package graphstore

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tevfik/gleann/internal/store"
)

// Reads run without a caller context because the RFC 003 section 3.2
// interface has none; they are short indexed queries and CTE walks with a
// depth cap, not long scans.
func readCtx() context.Context { return context.Background() }

const symbolCols = `fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc`

// scanner abstracts *sql.Row and *sql.Rows for the scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

func scanSymbol(sc scanner) (Symbol, error) {
	var s Symbol
	var isTest int
	if err := sc.Scan(&s.FQN, &s.Name, &s.Kind, &s.File, &s.StartLine, &s.EndLine, &s.StartCol, &s.ByteOffset, &isTest, &s.Weight, &s.Doc); err != nil {
		return Symbol{}, err
	}
	s.IsTest = isTest != 0
	return s, nil
}

// symbolSearchLimit mirrors kuzu.SymbolSearch's LIMIT 50 so symbol lookup
// keeps its interactive cost (RFC 003 section 6.2).
const symbolSearchLimit = 50

// ftsJoinLimit bounds how many trigram matches Symbols will join and sort by
// fqn. Above it the fqn-ordered LIKE scan (which stops at the LIMIT early) is
// cheaper than sorting a huge match set, so common substrings fall back to it.
const ftsJoinLimit = 4096

// symbolColsS is symbolCols qualified for the join against symbols_fts, where
// the bare column names would be ambiguous (both tables carry fqn and name).
const symbolColsS = `s.fqn, s.name, s.kind, s.file, s.start_line, s.end_line, s.start_col, s.byte_offset, s.is_test, s.weight, s.doc`

// Symbols searches first-party declarations whose FQN or short name contains
// q, case-insensitive, ordered by FQN and capped at symbolSearchLimit (the
// port of kuzu.SymbolSearch). An empty q matches every symbol.
//
// Substrings of three or more characters are answered by the trigram FTS5
// index over symbols(fqn, name) (symbols_fts): a quoted MATCH is a literal,
// case-insensitive substring search across both columns, so a rare query
// touches only its few postings instead of scanning every symbol. Queries
// shorter than three characters (a trigram needs three) and very common
// substrings use the fqn-ordered LIKE scan, which preserves the exact
// case-insensitive substring semantics and the LIMIT/ORDER BY and stops early
// once the cap is reached.
func (g *SQLite) Symbols(q string) ([]Symbol, error) {
	if q == "" {
		return g.symbolsQuery(`SELECT `+symbolCols+` FROM symbols ORDER BY fqn LIMIT ?`, symbolSearchLimit)
	}
	if len([]rune(q)) < 3 {
		return g.symbolsLike(q)
	}
	match := `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
	var n int
	if err := g.reader.QueryRowContext(readCtx(), `SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH ?`, match).Scan(&n); err != nil {
		return nil, fmt.Errorf("searching symbols for %q: %w", q, err)
	}
	if n > ftsJoinLimit {
		return g.symbolsLike(q)
	}
	return g.symbolsQuery(`SELECT `+symbolColsS+` FROM symbols_fts f
JOIN symbols s ON s.id = f.rowid
WHERE symbols_fts MATCH ? ORDER BY s.fqn LIMIT ?`, match, symbolSearchLimit)
}

// symbolsLike is the case-insensitive substring scan over symbols, ordered by
// fqn so the LIMIT terminates it early for common substrings. likePattern
// escapes the LIKE metacharacters so % and _ in q match literally.
func (g *SQLite) symbolsLike(q string) ([]Symbol, error) {
	p := likePattern(q)
	return g.symbolsQuery(`SELECT `+symbolCols+` FROM symbols
WHERE lower(fqn) LIKE ? ESCAPE '\' OR lower(name) LIKE ? ESCAPE '\'
ORDER BY fqn LIMIT ?`, p, p, symbolSearchLimit)
}

// symbolsQuery runs a symbol SELECT and scans the rows.
func (g *SQLite) symbolsQuery(query string, args ...any) ([]Symbol, error) {
	rows, err := g.reader.QueryContext(readCtx(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching symbols: %w", err)
	}
	defer rows.Close()
	var out []Symbol
	for rows.Next() {
		s, err := scanSymbol(rows)
		if err != nil {
			return nil, fmt.Errorf("searching symbols: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searching symbols: %w", err)
	}
	return out, nil
}

// baseName returns the last '.'-separated, ':'-separated or '/'-separated
// component of q, or q itself when there is no separator. It is the "base
// name" of kuzu's three-step lookup (internal/graph/kuzu/query.go).
func baseName(q string) string {
	parts := strings.FieldsFunc(q, func(r rune) bool {
		return r == ':' || r == '.' || r == '/'
	})
	if len(parts) == 0 {
		return q
	}
	return parts[len(parts)-1]
}

// ResolveSymbols resolves a user-supplied symbol reference to the FQNs of the
// first-party symbols it denotes, replicating the three-step lookup
// kuzu.Callees and kuzu.Callers apply to their argument (query.go):
//
//  1. exact FQN match on fqn;
//  2. when q contains '.', ':' or '/', an fqn suffix match for "::"+q,
//     "."+q and "/"+q;
//  3. the base name matched on name, plus an exact fqn match on q.
//
// A step only accepts a match when the symbol has at least one CALLS edge on
// the side dir selects: kuzu's MATCH (a:Symbol)-[:CALLS]->(b:Symbol) is empty
// for an isolated symbol, so an exact FQN whose symbol has no such neighbour
// falls through to the next step. The first non-empty step wins; a winning
// step that matches several symbols returns all of them (kuzu's union),
// ordered by FQN. Steps 1 and 3 hit indexed columns (fqn is the PRIMARY KEY,
// name has symbols_name); step 2 is a bounded scan over symbols — it cannot
// use an index without a reversed-fqn column, and its cost on the 200K-symbol
// fixture is measured by BenchmarkResolveSuffix.
func (g *SQLite) ResolveSymbols(q string, dir Direction) ([]string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	// edgePred requires the seed to have a CALLS edge on the side dir selects.
	// kuzu's MATCH needs both endpoints to be Symbol nodes, so the callee side
	// also excludes external targets (dst_external): a call to a third-party
	// symbol does not make the caller resolvable, just as the empty MATCH
	// would not.
	edgePred := "e.dst = s.fqn"
	if dir == DirectionCallees {
		edgePred = "e.src = s.fqn AND e.dst_external = 0"
	}

	// Step 1: exact FQN (indexed primary-key probe).
	if seeds, err := g.resolveSeedRows(`SELECT s.fqn FROM symbols s
 WHERE s.fqn = ?
   AND EXISTS (SELECT 1 FROM edges e WHERE e.kind = 'CALLS' AND `+edgePred+`)
 ORDER BY s.fqn`, q); err != nil || len(seeds) > 0 {
		return seeds, err
	}

	// Step 2: qualified suffix, only when q carries a qualifier. The three
	// ENDS WITH clauses become literal substr() suffix checks (case-sensitive,
	// like kuzu's ENDS WITH; LIKE would fold ASCII case).
	if strings.ContainsAny(q, ".:/") {
		if seeds, err := g.resolveSeedRows(`SELECT s.fqn FROM symbols s
 WHERE (substr(s.fqn, -length(?)) = ? OR substr(s.fqn, -length(?)) = ? OR substr(s.fqn, -length(?)) = ?)
   AND EXISTS (SELECT 1 FROM edges e WHERE e.kind = 'CALLS' AND `+edgePred+`)
 ORDER BY s.fqn`, "::"+q, "::"+q, "."+q, "."+q, "/"+q, "/"+q); err != nil || len(seeds) > 0 {
			return seeds, err
		}
	}

	// Step 3: base name on name, plus the original query on fqn (kuzu's third
	// query matches a.name = baseName OR a.fqn = clean).
	return g.resolveSeedRows(`SELECT s.fqn FROM symbols s
 WHERE (s.name = ? OR s.fqn = ?)
   AND EXISTS (SELECT 1 FROM edges e WHERE e.kind = 'CALLS' AND `+edgePred+`)
 ORDER BY s.fqn`, baseName(q), q)
}

// resolveSeedRows runs one resolution step and returns the matched FQNs.
func (g *SQLite) resolveSeedRows(query string, args ...any) ([]string, error) {
	rows, err := g.reader.QueryContext(readCtx(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("resolving symbol reference: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("resolving symbol reference: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolving symbol reference: %w", err)
	}
	return out, nil
}

// SymbolsInFile lists every first-party declaration anchored in one file,
// ordered by FQN. It is the port of kuzu.SymbolsInFile (the DECLARES relation
// folded into symbols.file, RFC 003 section 3.4), without Symbols' LIMIT 50:
// the whole file is requested here, not an interactive symbol lookup.
func (g *SQLite) SymbolsInFile(file string) ([]Symbol, error) {
	file = slashPath(file)
	rows, err := g.reader.QueryContext(readCtx(), `SELECT `+symbolCols+`
FROM symbols WHERE file = ? ORDER BY fqn`, file)
	if err != nil {
		return nil, fmt.Errorf("listing symbols in %q: %w", file, err)
	}
	defer rows.Close()
	var out []Symbol
	for rows.Next() {
		s, err := scanSymbol(rows)
		if err != nil {
			return nil, fmt.Errorf("listing symbols in %q: %w", file, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing symbols in %q: %w", file, err)
	}
	return out, nil
}

// likePattern builds a case-insensitive substring LIKE pattern, escaping the
// LIKE metacharacters so a query containing % or _ matches them literally.
func likePattern(q string) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range strings.ToLower(q) {
		if r == '\\' || r == '%' || r == '_' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}

// callersSQL walks CALLS edges backwards. UNION (not UNION ALL) deduplicates
// (fqn, depth) pairs so cycles terminate, and the depth cap bounds the walk
// (RFC 003 section 3.7). The outer min(depth) gives each symbol its shortest
// distance, i.e. the BFS level. No dst_external filter is needed: an external
// target has no symbols row, so it can never equal a walked FQN. The outer
// join is a CROSS JOIN to pin the small walk as the outer loop: without it
// the planner scans the whole symbols table and bloom-filters against the
// walk, which measured ~15x slower at 20K symbols and only gets worse at
// scale.
//
// The start node is excluded from the result except at depth 1: a direct
// self edge (rec -> rec) must report the symbol as its own neighbour, while
// a longer cycle (a -> b -> a) revisits the start at depth 2 and must not
// (matching kuzu, where MATCH (a)-[:CALLS]->(b) returns rec for the self edge
// but Callers(a) on the cycle returns only b).
const callersSQL = `
WITH RECURSIVE walk(fqn, depth) AS (
  SELECT ?, 0
  UNION
  SELECT e.src, w.depth + 1
  FROM edges e JOIN walk w ON e.dst = w.fqn
  WHERE e.kind = 'CALLS' AND w.depth < ?
)
SELECT s.fqn, s.name, s.kind, s.file, s.start_line, s.is_test, min(w.depth) AS depth
FROM walk w CROSS JOIN symbols s ON s.fqn = w.fqn
WHERE w.depth > 0 AND (w.fqn <> ? OR w.depth = 1)
GROUP BY s.fqn, s.name, s.kind, s.file, s.start_line, s.is_test
ORDER BY depth, s.is_test, s.fqn`

// calleesSQL walks CALLS edges forwards. External targets are excluded from
// the walk: they are not declarations and have no outgoing edges.
const calleesSQL = `
WITH RECURSIVE walk(fqn, depth) AS (
  SELECT ?, 0
  UNION
  SELECT e.dst, w.depth + 1
  FROM edges e JOIN walk w ON e.src = w.fqn
  WHERE e.kind = 'CALLS' AND e.dst_external = 0 AND w.depth < ?
)
SELECT s.fqn, s.name, s.kind, s.file, s.start_line, s.is_test, min(w.depth) AS depth
FROM walk w CROSS JOIN symbols s ON s.fqn = w.fqn
WHERE w.depth > 0 AND (w.fqn <> ? OR w.depth = 1)
GROUP BY s.fqn, s.name, s.kind, s.file, s.start_line, s.is_test
ORDER BY depth, s.is_test, s.fqn`

// Callers returns the symbols that call fqn, transitively up to depth hops
// (depth <= 0 means direct callers only; capped at maxTraversalDepth).
// Results are ordered by distance, production symbols before tests, then
// FQN, so the same graph always gives the same list.
func (g *SQLite) Callers(fqn string, depth int) ([]Hop, error) {
	return g.walk(callersSQL, fqn, walkDepth(depth), "querying callers of %q", fqn)
}

// Callees returns the symbols fqn calls, transitively up to depth hops, with
// the same depth rules and ordering as Callers.
func (g *SQLite) Callees(fqn string, depth int) ([]Hop, error) {
	return g.walk(calleesSQL, fqn, walkDepth(depth), "querying callees of %q", fqn)
}

func (g *SQLite) walk(query, fqn string, depth int, doing string, args ...any) ([]Hop, error) {
	rows, err := g.reader.QueryContext(readCtx(), query, fqn, depth, fqn)
	if err != nil {
		return nil, fmt.Errorf(doing+": %w", append(args, err)...)
	}
	defer rows.Close()
	var out []Hop
	for rows.Next() {
		var h Hop
		var isTest int
		if err := rows.Scan(&h.FQN, &h.Name, &h.Kind, &h.File, &h.Line, &isTest, &h.Depth); err != nil {
			return nil, fmt.Errorf(doing+": %w", append(args, err)...)
		}
		h.IsTest = isTest != 0
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(doing+": %w", append(args, err)...)
	}
	return out, nil
}

// impactSQL walks callers backwards but expands only production symbols
// (w.is_test = 0), matching kuzu.Impact: test callers are reported at the
// level where they appear and never propagate the blast radius further.
// The outer CROSS JOIN pins the walk as the outer loop (see callersSQL).
const impactSQL = `
WITH RECURSIVE walk(fqn, depth, is_test) AS (
  SELECT ?, 0, 0
  UNION
  SELECT e.src, w.depth + 1, s.is_test
  FROM edges e
  JOIN walk w ON e.dst = w.fqn
  JOIN symbols s ON s.fqn = e.src
  WHERE e.kind = 'CALLS' AND w.is_test = 0 AND w.depth < ?
)
SELECT w.fqn, min(w.depth) AS depth, s.is_test, s.file
FROM walk w CROSS JOIN symbols s ON s.fqn = w.fqn
WHERE w.depth > 0 AND (w.fqn <> ? OR w.depth = 1)
GROUP BY w.fqn, s.is_test, s.file
ORDER BY depth, s.fqn`

// Impact reports the blast radius of changing fqn: direct production
// callers, transitive production callers (through production callers only),
// test callers at any level, and the files declaring the production callers.
// Depth follows kuzu.Impact's rule: non-positive or above the cap means 5.
func (g *SQLite) Impact(fqn string, depth int) (*ImpactResult, error) {
	depth = impactDepth(depth)
	rows, err := g.reader.QueryContext(readCtx(), impactSQL, fqn, depth, fqn)
	if err != nil {
		return nil, fmt.Errorf("querying impact of %q: %w", fqn, err)
	}
	defer rows.Close()
	res := &ImpactResult{Symbol: fqn, Depth: depth}
	fileSet := make(map[string]bool)
	for rows.Next() {
		var sym, file string
		var d, isTest int
		if err := rows.Scan(&sym, &d, &isTest, &file); err != nil {
			return nil, fmt.Errorf("querying impact of %q: %w", fqn, err)
		}
		switch {
		case isTest != 0:
			res.TestCallers = append(res.TestCallers, sym)
		case d == 1:
			res.DirectCallers = append(res.DirectCallers, sym)
			fileSet[file] = true
		default:
			res.TransitiveCallers = append(res.TransitiveCallers, sym)
			fileSet[file] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("querying impact of %q: %w", fqn, err)
	}
	for f := range fileSet {
		res.AffectedFiles = append(res.AffectedFiles, f)
	}
	sort.Strings(res.AffectedFiles)
	return res, nil
}

// Path returns a shortest path between two symbols as a list of steps, or an
// error when none exists within maxPathDepth hops. from == to gives an empty
// path.
//
// It is a bidirectional BFS in Go over indexed single-hop neighbour lookups:
// one search grows from `from` following the traversal edges forward, another
// from `to` following them backward, each level expanding the smaller frontier
// with batched IN-list queries over edges(src) and edges_dst. They meet in the
// middle, so a typical path of length L explores roughly two frontiers of
// depth L/2 instead of one of depth L — milliseconds instead of the seconds a
// full depth-10 recursive CTE walk costs on a dense graph. The first meeting
// node whose total distance is <= the sum of the two expanded depths is a true
// shortest path (any shorter path would already have a node visited by both
// sides); the depth cap bounds the work exactly as the CTE's did.
//
// The traversal relation is the one kuzu.ShortestPath used: a CALLS edge is
// walkable forward (CALLS) and backward (CALLED_BY), an IMPLEMENTS edge only
// forward. External targets are never walked forward (dst_external = 0); the
// backward CALLS arm carries no external filter, so an external `from` can
// still reach the code that references it.
func (g *SQLite) Path(from, to string) ([]Step, error) {
	if from == to {
		return nil, nil
	}
	type parent struct{ node, rel string }
	distF := map[string]int{from: 0}
	distB := map[string]int{to: 0}
	parF := map[string]parent{} // node -> predecessor toward from
	parB := map[string]parent{} // node -> successor toward to
	frontF := []string{from}
	frontB := []string{to}
	depthF, depthB := 0, 0
	best := maxPathDepth + 1
	meet := ""
	for {
		if best <= depthF+depthB || depthF+depthB >= maxPathDepth || len(frontF) == 0 || len(frontB) == 0 {
			break
		}
		if len(frontF) <= len(frontB) {
			hops, err := g.pathHops(frontF, true)
			if err != nil {
				return nil, fmt.Errorf("finding path from %q to %q: %w", from, to, err)
			}
			depthF++
			var next []string
			for _, h := range hops {
				n := h.to
				if _, ok := distF[n]; ok {
					continue
				}
				distF[n] = depthF
				parF[n] = parent{h.from, h.rel}
				next = append(next, n)
				if d, ok := distB[n]; ok {
					if c := depthF + d; c < best {
						best, meet = c, n
					}
				}
			}
			frontF = next
		} else {
			hops, err := g.pathHops(frontB, false)
			if err != nil {
				return nil, fmt.Errorf("finding path from %q to %q: %w", from, to, err)
			}
			depthB++
			var next []string
			for _, h := range hops {
				n := h.from
				if _, ok := distB[n]; ok {
					continue
				}
				distB[n] = depthB
				parB[n] = parent{h.to, h.rel}
				next = append(next, n)
				if d, ok := distF[n]; ok {
					if c := d + depthB; c < best {
						best, meet = c, n
					}
				}
			}
			frontB = next
		}
	}
	if meet == "" {
		return nil, fmt.Errorf("finding path from %q to %q: no path within %d hops", from, to, maxPathDepth)
	}

	// Reconstruct: walk the forward tree back from meet to from, reverse it,
	// then walk the backward tree forward from meet to to.
	var rev []Step
	for cur := meet; cur != from; {
		p, ok := parF[cur]
		if !ok {
			return nil, fmt.Errorf("finding path from %q to %q: broken forward chain at %q", from, to, cur)
		}
		rev = append(rev, Step{From: p.node, To: cur, Relation: p.rel})
		cur = p.node
	}
	steps := make([]Step, 0, best)
	for i := len(rev) - 1; i >= 0; i-- {
		steps = append(steps, rev[i])
	}
	for cur := meet; cur != to; {
		p, ok := parB[cur]
		if !ok {
			return nil, fmt.Errorf("finding path from %q to %q: broken backward chain at %q", from, to, cur)
		}
		steps = append(steps, Step{From: cur, To: p.node, Relation: p.rel})
		cur = p.node
	}
	return steps, nil
}

// pathHop is one traversal step discovered while expanding a frontier, always
// read in from->to path direction: for a forward expansion From is the
// frontier node and To the neighbour; for a backward expansion From is the
// predecessor (neighbour) and To the frontier node.
type pathHop struct{ from, to, rel string }

// pathHops expands one frontier level with batched IN-list queries. forward
// selects the outgoing traversal edges of the frontier nodes; backward selects
// the incoming ones (the predecessors). Each query returns (from, to, rel) in
// path direction. The frontier is chunked so the IN-list stays bounded.
func (g *SQLite) pathHops(frontier []string, forward bool) ([]pathHop, error) {
	const chunk = 512
	var hops []pathHop
	for start := 0; start < len(frontier); start += chunk {
		end := min(start+chunk, len(frontier))
		ph := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, end-start)
		for i, n := range frontier[start:end] {
			args[i] = n
		}
		var queries []string
		if forward {
			queries = []string{
				`SELECT src, dst, kind FROM edges WHERE src IN (` + ph + `) AND kind IN ('CALLS', 'IMPLEMENTS') AND dst_external = 0`,
				`SELECT dst, src, 'CALLED_BY' FROM edges WHERE dst IN (` + ph + `) AND kind = 'CALLS'`,
			}
		} else {
			queries = []string{
				`SELECT src, dst, kind FROM edges WHERE dst IN (` + ph + `) AND kind IN ('CALLS', 'IMPLEMENTS') AND dst_external = 0`,
				`SELECT dst, src, 'CALLED_BY' FROM edges WHERE src IN (` + ph + `) AND kind = 'CALLS'`,
			}
		}
		for _, q := range queries {
			rows, err := g.reader.QueryContext(readCtx(), q, args...)
			if err != nil {
				return nil, fmt.Errorf("expanding path frontier: %w", err)
			}
			for rows.Next() {
				var h pathHop
				if err := rows.Scan(&h.from, &h.to, &h.rel); err != nil {
					rows.Close()
					return nil, fmt.Errorf("expanding path frontier: %w", err)
				}
				hops = append(hops, h)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("expanding path frontier: %w", err)
			}
			rows.Close()
		}
	}
	return hops, nil
}

// neighborQueries are the four expansions of kuzu.Neighbors, each returning
// the farther endpoint and its symbol kind. The JOIN on symbols drops
// external targets, which have no row (kuzu's MATCH required both endpoints
// to be Symbol nodes), and the ORDER BY makes the walk deterministic where
// kuzu left it to the engine.
var neighborQueries = []struct {
	relation string
	q        string
}{
	{"CALLS", `SELECT e.dst, s.kind FROM edges e JOIN symbols s ON s.fqn = e.dst
WHERE e.src = ? AND e.kind = 'CALLS' ORDER BY e.dst`},
	{"CALLS", `SELECT e.src, s.kind FROM edges e JOIN symbols s ON s.fqn = e.src
WHERE e.dst = ? AND e.kind = 'CALLS' ORDER BY e.src`},
	{"IMPLEMENTS", `SELECT e.dst, s.kind FROM edges e JOIN symbols s ON s.fqn = e.dst
WHERE e.src = ? AND e.kind = 'IMPLEMENTS' ORDER BY e.dst`},
	{"REFERENCES", `SELECT e.dst, s.kind FROM edges e JOIN symbols s ON s.fqn = e.dst
WHERE e.src = ? AND e.kind = 'REFERENCES' ORDER BY e.dst`},
}

// Neighbors returns the edges around fqn, expanded breadth-first up to
// maxDepth hops (depth <= 0 means 1, capped at 5, as kuzu.Neighbors). The
// walk follows CALLS in both directions and IMPLEMENTS and REFERENCES
// forward only, matching kuzu's expansion; each hit emits an edge (even when
// the endpoint was already reached, as kuzu did) but an endpoint is queued
// only once. External targets never appear: the JOIN drops edges whose far
// end has no symbols row.
func (g *SQLite) Neighbors(fqn string, maxDepth int) ([]GraphEdge, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 5 {
		maxDepth = 5
	}
	var edges []GraphEdge
	visited := map[string]bool{fqn: true}
	frontier := []string{fqn}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, node := range frontier {
			for i, nq := range neighborQueries {
				forward := i != 1 // the second query is CALLS backward
				rows, err := g.reader.QueryContext(readCtx(), nq.q, node)
				if err != nil {
					return nil, fmt.Errorf("listing neighbours of %q: %w", node, err)
				}
				for rows.Next() {
					var other, kind string
					if err := rows.Scan(&other, &kind); err != nil {
						rows.Close()
						return nil, fmt.Errorf("listing neighbours of %q: %w", node, err)
					}
					from, to := node, other
					if !forward {
						from, to = other, node
					}
					edges = append(edges, GraphEdge{
						From: from, To: to, Relation: nq.relation,
						TargetKind: kind, Confidence: "extracted",
					})
					if !visited[other] {
						visited[other] = true
						next = append(next, other)
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return nil, fmt.Errorf("listing neighbours of %q: %w", node, err)
				}
				rows.Close()
			}
		}
		frontier = next
	}
	return edges, nil
}

// AllSymbols returns every first-party declaration, ordered by FQN. Unlike
// Symbols it carries no LIMIT: it feeds bulk consumers (the community and
// PageRank loader of RFC 003 section 3.7), not interactive lookup.
func (g *SQLite) AllSymbols() ([]Symbol, error) {
	rows, err := g.reader.QueryContext(readCtx(), `SELECT `+symbolCols+` FROM symbols ORDER BY fqn`)
	if err != nil {
		return nil, fmt.Errorf("listing all symbols: %w", err)
	}
	defer rows.Close()
	var out []Symbol
	for rows.Next() {
		s, err := scanSymbol(rows)
		if err != nil {
			return nil, fmt.Errorf("listing all symbols: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing all symbols: %w", err)
	}
	return out, nil
}

// AllEdges returns every edge, ordered by src, dst, kind, preserving
// dst_external so bulk consumers can skip external targets (which have no
// symbols row). It feeds the community and PageRank loader (RFC 003 section
// 3.7).
func (g *SQLite) AllEdges() ([]Edge, error) {
	rows, err := g.reader.QueryContext(readCtx(), `SELECT src, dst, kind, dst_external, confidence
FROM edges ORDER BY src, dst, kind`)
	if err != nil {
		return nil, fmt.Errorf("listing all edges: %w", err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		var external int
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind, &external, &e.Confidence); err != nil {
			return nil, fmt.Errorf("listing all edges: %w", err)
		}
		e.DstExternal = external != 0
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing all edges: %w", err)
	}
	return out, nil
}

// DeclaredFQNs maps each requested FQN that has a declaration to the file
// anchoring it; FQNs with no row are absent from the result. It is the
// existence probe the format-2 write path uses to classify an edge target as
// internal (declared somewhere) or external (third-party / standard library)
// before building a FileDelta, and to detect a declaration owned by another
// file (build-tag variants). The IN-list is chunked so the query stays bounded.
func (g *SQLite) DeclaredFQNs(fqns []string) (map[string]string, error) {
	out := make(map[string]string)
	const chunk = 512
	for start := 0; start < len(fqns); start += chunk {
		end := min(start+chunk, len(fqns))
		ph := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, end-start)
		for i, f := range fqns[start:end] {
			args[i] = f
		}
		rows, err := g.reader.QueryContext(readCtx(), `SELECT fqn, file FROM symbols WHERE fqn IN (`+ph+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("looking up declared symbols: %w", err)
		}
		for rows.Next() {
			var fqn, file string
			if err := rows.Scan(&fqn, &file); err != nil {
				rows.Close()
				return nil, fmt.Errorf("looking up declared symbols: %w", err)
			}
			out[fqn] = file
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("looking up declared symbols: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// HasData reports whether the graph or document hierarchy tables hold any
// rows. It is the cheap probe Searcher.Load uses to decide whether a format-2
// index has graph data to serve (RFC 003 section 8 step 7): a store whose
// symbols or doc_nodes are non-empty gets graph queries in every build.
func (g *SQLite) HasData() (bool, error) {
	var n int
	if err := g.reader.QueryRowContext(readCtx(), `SELECT count(*) FROM symbols`).Scan(&n); err != nil {
		return false, fmt.Errorf("counting symbols: %w", err)
	}
	if n > 0 {
		return true, nil
	}
	if err := g.reader.QueryRowContext(readCtx(), `SELECT count(*) FROM doc_nodes`).Scan(&n); err != nil {
		return false, fmt.Errorf("counting document nodes: %w", err)
	}
	return n > 0, nil
}

// Stats counts the graph tables (the port of kuzu.Stats). DeclareEdges is
// the symbol count: RFC 003 section 3.4 replaces the DECLARES relation with
// the symbols.file anchor, one declaration per symbol. The symbol and
// per-kind edge counts are read from the meta counters that ApplyFileDelta,
// RemoveFile and ReplaceAll maintain in their write transactions, so Stats
// is a handful of point lookups instead of a scan over edges (the GROUP BY
// kind used to scan the whole edges_file covering index). Files stays a live
// count: the manifest is small and shared with the passage store.
func (g *SQLite) Stats() (Stats, error) {
	var st Stats
	if err := g.reader.QueryRowContext(readCtx(), `SELECT count(*) FROM files`).Scan(&st.Files); err != nil {
		return Stats{}, fmt.Errorf("counting graph rows: %w", err)
	}
	rows, err := g.reader.QueryContext(readCtx(), `SELECT key, value FROM meta WHERE key IN (?, ?, ?)`,
		store.MetaGraphSymbols, store.MetaGraphEdgesPrefix+"CALLS", store.MetaGraphEdgesPrefix+"IMPLEMENTS")
	if err != nil {
		return Stats{}, fmt.Errorf("counting graph rows: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, 3)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Stats{}, fmt.Errorf("counting graph rows: %w", err)
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return Stats{}, fmt.Errorf("counting graph rows: counter %s = %q: %w", key, value, err)
		}
		seen[key] = true
		switch key {
		case store.MetaGraphSymbols:
			st.Symbols = n
		case store.MetaGraphEdgesPrefix + "CALLS":
			st.CallEdges = n
		case store.MetaGraphEdgesPrefix + "IMPLEMENTS":
			st.ImplementsEdges = n
		}
	}
	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("counting graph rows: %w", err)
	}
	if !seen[store.MetaGraphSymbols] || !seen[store.MetaGraphEdgesPrefix+"CALLS"] || !seen[store.MetaGraphEdgesPrefix+"IMPLEMENTS"] {
		return Stats{}, fmt.Errorf("counting graph rows: graph counters missing; schema not migrated")
	}
	st.DeclareEdges = st.Symbols
	return st, nil
}

// InDegreeTop returns the n most-called symbols (n <= 0 means 10), most
// calls first, ties by FQN. This is the GROUP BY aggregate the MCP risk
// tools ran as Cypher count(caller) (RFC 003 section 6.2); counting edge
// rows matches Kùzu, where one caller symbol can contribute several edges
// from different files. The aggregate runs on edges alone and the LIMIT is
// applied before joining symbols, so only the top n rows pay for the
// symbol lookup; every internal dst has a symbols row (dropUnresolved and
// checkSources guarantee it), so the join drops nothing.
func (g *SQLite) InDegreeTop(n int) ([]InDegree, error) {
	if n <= 0 {
		n = 10
	}
	rows, err := g.reader.QueryContext(readCtx(), `WITH top AS (
  SELECT dst, count(*) AS in_degree
  FROM edges
  WHERE kind = 'CALLS' AND dst_external = 0
  GROUP BY dst
  ORDER BY in_degree DESC, dst
  LIMIT ?
)
SELECT t.dst, s.name, s.kind, s.file, t.in_degree
FROM top t JOIN symbols s ON s.fqn = t.dst
ORDER BY t.in_degree DESC, t.dst`, n)
	if err != nil {
		return nil, fmt.Errorf("aggregating in-degrees: %w", err)
	}
	defer rows.Close()
	var out []InDegree
	for rows.Next() {
		var d InDegree
		if err := rows.Scan(&d.FQN, &d.Name, &d.Kind, &d.File, &d.InDegree); err != nil {
			return nil, fmt.Errorf("aggregating in-degrees: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aggregating in-degrees: %w", err)
	}
	return out, nil
}

// DirtyFiles lists the files whose graph rows need re-extraction, served by
// the partial index files_dirty (RFC 003 sections 3.4 and 6.5). The next
// sync re-extracts each of them and ApplyFileDelta clears the flag.
func (g *SQLite) DirtyFiles() ([]string, error) {
	rows, err := g.reader.QueryContext(readCtx(), `SELECT path FROM files WHERE graph_dirty = 1 ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing dirty files: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("listing dirty files: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing dirty files: %w", err)
	}
	return out, nil
}
