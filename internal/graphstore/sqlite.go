package graphstore

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
// name" of kuzu's three-step lookup (the former internal/graph/kuzu/query.go).
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
	edgePred := "e.dst = s.id"
	if dir == DirectionCallees {
		edgePred = "e.src = s.id AND e.dst_external = 0"
	}

	// Step 1: exact FQN (indexed primary-key probe).
	if seeds, err := g.resolveSeedRows(`SELECT s.fqn FROM symbols s
 WHERE s.fqn = ?
   AND EXISTS (SELECT 1 FROM graph_edges e WHERE e.kind = `+labelCalls+` AND `+edgePred+`)
 ORDER BY s.fqn`, q); err != nil || len(seeds) > 0 {
		return seeds, err
	}

	// Step 2: qualified suffix, only when q carries a qualifier. The three
	// ENDS WITH clauses become literal substr() suffix checks (case-sensitive,
	// like kuzu's ENDS WITH; LIKE would fold ASCII case).
	if strings.ContainsAny(q, ".:/") {
		if seeds, err := g.resolveSeedRows(`SELECT s.fqn FROM symbols s
 WHERE (substr(s.fqn, -length(?)) = ? OR substr(s.fqn, -length(?)) = ? OR substr(s.fqn, -length(?)) = ?)
   AND EXISTS (SELECT 1 FROM graph_edges e WHERE e.kind = `+labelCalls+` AND `+edgePred+`)
 ORDER BY s.fqn`, "::"+q, "::"+q, "."+q, "."+q, "/"+q, "/"+q); err != nil || len(seeds) > 0 {
			return seeds, err
		}
	}

	// Step 3: base name on name, plus the original query on fqn (kuzu's third
	// query matches a.name = baseName OR a.fqn = clean).
	return g.resolveSeedRows(`SELECT s.fqn FROM symbols s
 WHERE (s.name = ? OR s.fqn = ?)
   AND EXISTS (SELECT 1 FROM graph_edges e WHERE e.kind = `+labelCalls+` AND `+edgePred+`)
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

// ResolveDeclarationFiles returns the declaration file paths for symbol q
// following the exact same 3-step hierarchy as ResolveSymbols (exact FQN,
// qualified suffix, base name) but without requiring CALLS edges.
func (g *SQLite) ResolveDeclarationFiles(q string) ([]string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	// Step 1: exact FQN
	files, err := g.resolveDeclarationRows(`SELECT DISTINCT file FROM symbols WHERE fqn = ? ORDER BY file`, q)
	if err != nil || len(files) > 0 {
		return files, err
	}

	// Step 2: qualified suffix (if q contains . or : or /)
	if strings.ContainsAny(q, ".:/") {
		files, err := g.resolveDeclarationRows(`SELECT DISTINCT file FROM symbols
 WHERE (substr(fqn, -length(?)) = ? OR substr(fqn, -length(?)) = ? OR substr(fqn, -length(?)) = ?)
 ORDER BY file`, "::"+q, "::"+q, "."+q, "."+q, "/"+q, "/"+q)
		if err != nil || len(files) > 0 {
			return files, err
		}
	}

	// Step 3: base name
	return g.resolveDeclarationRows(`SELECT DISTINCT file FROM symbols WHERE (name = ? OR fqn = ?) ORDER BY file`, baseName(q), q)
}

func (g *SQLite) resolveDeclarationRows(query string, args ...any) ([]string, error) {
	rows, err := g.reader.QueryContext(readCtx(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("resolving symbol declaration files: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("resolving symbol declaration files: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
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
WITH RECURSIVE start(id) AS (` + nodeIDOf + `),
walk(id, depth) AS (
  SELECT id, 0 FROM start
  UNION
  SELECT e.src, w.depth + 1
  FROM graph_edges e JOIN walk w ON e.dst = w.id
  WHERE e.kind = ` + labelCalls + ` AND w.depth < ?2
)
SELECT s.fqn, s.name, s.kind, s.file, s.start_line, s.is_test, min(w.depth) AS depth
FROM walk w CROSS JOIN symbols s ON s.id = w.id
WHERE w.depth > 0 AND (w.id <> (SELECT id FROM start) OR w.depth = 1)
GROUP BY s.id
ORDER BY depth, s.is_test, s.fqn`

// calleesSQL walks CALLS edges forwards. External targets are excluded from
// the walk: they are not declarations and have no outgoing edges.
const calleesSQL = `
WITH RECURSIVE start(id) AS (` + nodeIDOf + `),
walk(id, depth) AS (
  SELECT id, 0 FROM start
  UNION
  SELECT e.dst, w.depth + 1
  FROM graph_edges e JOIN walk w ON e.src = w.id
  WHERE e.kind = ` + labelCalls + ` AND e.dst_external = 0 AND w.depth < ?2
)
SELECT s.fqn, s.name, s.kind, s.file, s.start_line, s.is_test, min(w.depth) AS depth
FROM walk w CROSS JOIN symbols s ON s.id = w.id
WHERE w.depth > 0 AND (w.id <> (SELECT id FROM start) OR w.depth = 1)
GROUP BY s.id
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
	rows, err := g.queryRead(query, fqn, depth)
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
WITH RECURSIVE start(id) AS (` + nodeIDOf + `),
walk(id, depth, is_test) AS (
  SELECT id, 0, 0 FROM start
  UNION
  SELECT e.src, w.depth + 1, s.is_test
  FROM graph_edges e
  JOIN walk w ON e.dst = w.id
  JOIN symbols s ON s.id = e.src
  WHERE e.kind = ` + labelCalls + ` AND w.is_test = 0 AND w.depth < ?2
)
SELECT s.fqn, min(w.depth) AS depth, s.is_test, s.file
FROM walk w CROSS JOIN symbols s ON s.id = w.id
WHERE w.depth > 0 AND (w.id <> (SELECT id FROM start) OR w.depth = 1)
GROUP BY w.id, s.is_test, s.file
ORDER BY depth, s.fqn`

// Impact reports the blast radius of changing fqn: direct production
// callers, transitive production callers (through production callers only),
// test callers at any level, and the files declaring the production callers.
// Depth follows kuzu.Impact's rule: non-positive or above the cap means 5.
func (g *SQLite) Impact(fqn string, depth int) (*ImpactResult, error) {
	depth = impactDepth(depth)
	rows, err := g.queryRead(impactSQL, fqn, depth)
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
// with batched IN-list queries over graph_edges by src and by dst (ids only;
// FQNs are fetched once per newly reached node). They meet in the
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
	noPath := fmt.Errorf("finding path from %q to %q: no path within %d hops", from, to, maxPathDepth)
	fromID, ok, err := g.nodeID(from)
	if err != nil {
		return nil, fmt.Errorf("finding path from %q to %q: %w", from, to, err)
	}
	if !ok {
		return nil, noPath
	}
	toID, ok, err := g.nodeID(to)
	if err != nil {
		return nil, fmt.Errorf("finding path from %q to %q: %w", from, to, err)
	}
	if !ok {
		return nil, noPath
	}
	pn := &pathNames{g: g, names: map[int64]string{fromID: from, toID: to}}

	type parent struct {
		node int64
		rel  string
	}
	distF := map[int64]int{fromID: 0}
	distB := map[int64]int{toID: 0}
	parF := map[int64]parent{} // node -> predecessor toward from
	parB := map[int64]parent{} // node -> successor toward to
	frontF := []int64{fromID}
	frontB := []int64{toID}
	depthF, depthB := 0, 0
	best := maxPathDepth + 1
	var meet int64
	found := false
	for {
		if best <= depthF+depthB || depthF+depthB >= maxPathDepth || len(frontF) == 0 || len(frontB) == 0 {
			break
		}
		forward := len(frontF) <= len(frontB)
		front, dist, other, par := frontB, distB, distF, parB
		if forward {
			front, dist, other, par = frontF, distF, distB, parF
		}
		level, err := g.pathLevel(front, forward, dist, other, pn)
		if err != nil {
			return nil, fmt.Errorf("finding path from %q to %q: %w", from, to, err)
		}
		depth := depthB + 1
		if forward {
			depth = depthF + 1
		}
		// level holds one winning hop per newly reached node, those the other
		// side reached first and in hop order: the first that closes a
		// shorter path is the meeting node.
		next := make([]int64, 0, len(level))
		for _, h := range level {
			dist[h.nbr] = depth
			par[h.nbr] = parent{h.frontier, h.rel}
			next = append(next, h.nbr)
			if d, ok := other[h.nbr]; ok {
				if c := depth + d; c < best {
					best, meet, found = c, h.nbr, true
				}
			}
		}
		if forward {
			depthF, frontF = depth, next
		} else {
			depthB, frontB = depth, next
		}
	}
	if !found {
		return nil, noPath
	}

	// Reconstruct: walk the forward tree back from meet to from, reverse it,
	// then walk the backward tree forward from meet to to.
	var chain []int64
	for cur := meet; cur != fromID; {
		p, ok := parF[cur]
		if !ok {
			return nil, fmt.Errorf("finding path from %q to %q: broken forward chain at node %d", from, to, cur)
		}
		chain = append(chain, cur, p.node)
		cur = p.node
	}
	for cur := meet; cur != toID; {
		p, ok := parB[cur]
		if !ok {
			return nil, fmt.Errorf("finding path from %q to %q: broken backward chain at node %d", from, to, cur)
		}
		chain = append(chain, cur, p.node)
		cur = p.node
	}
	if err := pn.load(chain); err != nil {
		return nil, fmt.Errorf("finding path from %q to %q: %w", from, to, err)
	}
	var rev []Step
	for cur := meet; cur != fromID; cur = parF[cur].node {
		p := parF[cur]
		rev = append(rev, Step{From: pn.names[p.node], To: pn.names[cur], Relation: p.rel})
	}
	steps := make([]Step, 0, best)
	for i := len(rev) - 1; i >= 0; i-- {
		steps = append(steps, rev[i])
	}
	for cur := meet; cur != toID; cur = parB[cur].node {
		p := parB[cur]
		steps = append(steps, Step{From: pn.names[cur], To: pn.names[p.node], Relation: p.rel})
	}
	return steps, nil
}

// nodeID resolves an FQN to its node id (declared symbol or external target).
func (g *SQLite) nodeID(fqn string) (int64, bool, error) {
	var id int64
	err := g.reader.QueryRowContext(readCtx(), nodeIDOf, fqn).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolving %q: %w", fqn, err)
	}
	return id, true, nil
}

// pathNames caches node FQNs for one Path call, fetched only when a tie has
// to be broken by name or a step is reported.
type pathNames struct {
	g     *SQLite
	names map[int64]string
}

// load fetches the FQNs of ids not cached yet: symbols first, then the
// external graph_nodes for the rest.
func (p *pathNames) load(ids []int64) error {
	var missing []int64
	for _, id := range ids {
		if _, ok := p.names[id]; !ok {
			p.names[id] = "" // queued once
			missing = append(missing, id)
		}
	}
	for _, table := range []string{"symbols", "graph_nodes"} {
		if len(missing) == 0 {
			return nil
		}
		const chunk = 512
		for start := 0; start < len(missing); start += chunk {
			end := min(start+chunk, len(missing))
			args := make([]any, end-start)
			for i, id := range missing[start:end] {
				args[i] = id
			}
			rows, err := p.g.reader.QueryContext(readCtx(), `SELECT id, fqn FROM `+table+` WHERE id IN (`+inList(end-start)+`)`, args...)
			if err != nil {
				return fmt.Errorf("reading node names: %w", err)
			}
			for rows.Next() {
				var id int64
				var fqn string
				if err := rows.Scan(&id, &fqn); err != nil {
					rows.Close()
					return fmt.Errorf("reading node names: %w", err)
				}
				p.names[id] = fqn
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return fmt.Errorf("reading node names: %w", err)
			}
			rows.Close()
		}
		rest := missing[:0]
		for _, id := range missing {
			if p.names[id] == "" {
				rest = append(rest, id)
			}
		}
		missing = rest
	}
	if len(missing) > 0 {
		return fmt.Errorf("node %d has no name", missing[0])
	}
	return nil
}

// pathHop is one traversal step found while expanding a frontier: frontier is
// the expanding side's node, nbr the newly reached one. rel is the relation in
// path direction (forward: frontier -> nbr; backward: nbr -> frontier).
type pathHop struct {
	frontier, nbr, kind int64
	arm                 int // 0: the CALLS/IMPLEMENTS arm, 1: the CALLED_BY arm
	rel                 string
}

// pathLevel expands one frontier level with batched IN-list queries over the
// graph_edges primary key (by src) and graph_edges_dst (by dst), on ids only:
// forward follows the outgoing traversal edges of the frontier nodes, backward
// the incoming ones. Hops to nodes the expanding side already visited are
// dropped; of the hops reaching the same new node only the first in hop order
// is kept (it becomes the node's parent), and the result is in hop order up to
// ties between different new nodes, which only matter for the meeting node.
//
// Hop order is canonical: the CALLS/IMPLEMENTS arm before the CALLED_BY arm,
// then the frontier node's FQN, then the neighbour's FQN (the backward
// CALLS/IMPLEMENTS arm: kind before the neighbour; the forward one: kind
// after it). That is the order the former text-keyed index scans returned,
// so the same shortest path is reported among several; FQNs are fetched only
// to break a real tie, not for every reached node.
func (g *SQLite) pathLevel(frontier []int64, forward bool, seen, other map[int64]int, pn *pathNames) ([]pathHop, error) {
	const chunk = 512
	var hops []pathHop
	for start := 0; start < len(frontier); start += chunk {
		end := min(start+chunk, len(frontier))
		ph := inList(end - start)
		args := make([]any, end-start)
		for i, n := range frontier[start:end] {
			args[i] = n
		}
		// Each query returns (frontier node, neighbour, kind).
		var queries [2]string
		if forward {
			queries = [2]string{
				`SELECT src, dst, kind FROM graph_edges WHERE src IN (` + ph + `) AND kind IN (` + labelCalls + `, ` + labelImplements + `) AND dst_external = 0`,
				`SELECT dst, src, kind FROM graph_edges WHERE dst IN (` + ph + `) AND kind = ` + labelCalls,
			}
		} else {
			queries = [2]string{
				`SELECT dst, src, kind FROM graph_edges WHERE dst IN (` + ph + `) AND kind IN (` + labelCalls + `, ` + labelImplements + `) AND dst_external = 0`,
				`SELECT src, dst, kind FROM graph_edges WHERE src IN (` + ph + `) AND kind = ` + labelCalls,
			}
		}
		for arm, q := range queries {
			rows, err := g.reader.QueryContext(readCtx(), q, args...)
			if err != nil {
				return nil, fmt.Errorf("expanding path frontier: %w", err)
			}
			for rows.Next() {
				h := pathHop{arm: arm}
				if err := rows.Scan(&h.frontier, &h.nbr, &h.kind); err != nil {
					rows.Close()
					return nil, fmt.Errorf("expanding path frontier: %w", err)
				}
				if _, ok := seen[h.nbr]; ok {
					continue
				}
				switch {
				case arm == 1:
					h.rel = "CALLED_BY"
				case h.kind == store.GraphLabelCalls:
					h.rel = "CALLS"
				default:
					h.rel = "IMPLEMENTS"
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

	// One winner per new node: the minimal hop by (arm, frontier FQN, kind).
	// Frontier FQNs are needed only where several frontier nodes reach the
	// same new node.
	byNbr := make(map[int64][]int, len(hops))
	order := make([]int64, 0, len(hops))
	for i, h := range hops {
		if _, ok := byNbr[h.nbr]; !ok {
			order = append(order, h.nbr)
		}
		byNbr[h.nbr] = append(byNbr[h.nbr], i)
	}
	var need []int64
	for _, idx := range byNbr {
		for _, i := range idx[1:] {
			if hops[i].frontier != hops[idx[0]].frontier {
				for _, j := range idx {
					need = append(need, hops[j].frontier)
				}
				break
			}
		}
	}
	if err := pn.load(need); err != nil {
		return nil, fmt.Errorf("expanding path frontier: %w", err)
	}
	key := func(a, b pathHop) int { // hop order without the neighbour
		if c := cmp.Compare(a.arm, b.arm); c != 0 {
			return c
		}
		if a.frontier != b.frontier {
			if c := strings.Compare(pn.names[a.frontier], pn.names[b.frontier]); c != 0 {
				return c
			}
		}
		if a.arm == 0 {
			return cmp.Compare(a.kind, b.kind)
		}
		return 0
	}
	level := make([]pathHop, 0, len(order))
	for _, n := range order {
		idx := byNbr[n]
		w := hops[idx[0]]
		for _, i := range idx[1:] {
			if key(hops[i], w) < 0 {
				w = hops[i]
			}
		}
		level = append(level, w)
	}

	// The caller takes the first hop, in hop order, whose node closes the
	// shortest path. Only nodes the other side reached can, so the full hop
	// order (neighbour FQNs included) is established among those alone and
	// they go first; the order of the rest matters to nothing.
	var cands, rest []pathHop
	var need2 []int64
	for _, h := range level {
		if _, ok := other[h.nbr]; ok {
			cands = append(cands, h)
			need2 = append(need2, h.frontier, h.nbr)
			continue
		}
		rest = append(rest, h)
	}
	if len(cands) > 1 {
		if err := pn.load(need2); err != nil {
			return nil, fmt.Errorf("expanding path frontier: %w", err)
		}
		slices.SortStableFunc(cands, func(a, b pathHop) int {
			if c := cmp.Compare(a.arm, b.arm); c != 0 {
				return c
			}
			if c := strings.Compare(pn.names[a.frontier], pn.names[b.frontier]); c != 0 {
				return c
			}
			if a.arm == 0 && !forward { // backward CALLS/IMPLEMENTS: kind before neighbour
				if c := cmp.Compare(a.kind, b.kind); c != 0 {
					return c
				}
			}
			if c := strings.Compare(pn.names[a.nbr], pn.names[b.nbr]); c != 0 {
				return c
			}
			return cmp.Compare(a.kind, b.kind)
		})
	}
	return append(cands, rest...), nil
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
	{"CALLS", `SELECT s.fqn, s.kind FROM graph_edges e JOIN symbols s ON s.id = e.dst
WHERE e.src = (SELECT id FROM symbols WHERE fqn = ?) AND e.kind = ` + labelCalls + ` ORDER BY s.fqn`},
	{"CALLS", `SELECT s.fqn, s.kind FROM graph_edges e JOIN symbols s ON s.id = e.src
WHERE e.dst IN (` + nodeIDOf + `) AND e.kind = ` + labelCalls + ` ORDER BY s.fqn`},
	{"IMPLEMENTS", `SELECT s.fqn, s.kind FROM graph_edges e JOIN symbols s ON s.id = e.dst
WHERE e.src = (SELECT id FROM symbols WHERE fqn = ?) AND e.kind = ` + labelImplements + ` ORDER BY s.fqn`},
	{"REFERENCES", `SELECT s.fqn, s.kind FROM graph_edges e JOIN symbols s ON s.id = e.dst
WHERE e.src = (SELECT id FROM symbols WHERE fqn = ?) AND e.kind = ` + labelReferences + ` ORDER BY s.fqn`},
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
	rows, err := g.reader.QueryContext(readCtx(), `SELECT sn.fqn, coalesce(ds.fqn, dn.fqn), k.label, e.dst_external, c.label
FROM graph_edges e
JOIN symbols sn ON sn.id = e.src
LEFT JOIN symbols ds ON ds.id = e.dst
LEFT JOIN graph_nodes dn ON dn.id = e.dst
JOIN graph_labels k ON k.id = e.kind
JOIN graph_labels c ON c.id = e.confidence
ORDER BY 1, 2, 3`)
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

// CodeFileCount is the number of files that declare at least one symbol: the
// CodeFile count of the Kùzu graph. Stats.Files counts the shared files table
// instead, which also holds documents and files without symbols.
func (g *SQLite) CodeFileCount() (int, error) {
	var n int
	if err := g.reader.QueryRowContext(readCtx(), `SELECT count(DISTINCT file) FROM symbols`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting code files: %w", err)
	}
	return n, nil
}

// InDegreeTop returns the n most-called symbols (n <= 0 means 10), most
// calls first, ties by FQN. This is the GROUP BY aggregate the MCP risk
// tools ran as Cypher count(caller) (RFC 003 section 6.2); counting edge
// rows matches Kùzu, where one caller symbol can contribute several edges
// from different files. The aggregate runs on edges alone and the LIMIT is
// applied before joining symbols, so only the groups that can still make
// the top n (ties broken by FQN) pay for the symbol lookup; every internal
// dst has a symbols row (insertEdges and checkSources guarantee it), so the
// join drops nothing.
func (g *SQLite) InDegreeTop(n int) ([]InDegree, error) {
	return g.InDegreeTopExcluding(n)
}

// InDegreeTopExcluding is InDegreeTop without the symbols of the given kinds
// (the risk tools drop "macro"). The kind filter needs the symbols row, so
// with a filter the join runs before the LIMIT.
func (g *SQLite) InDegreeTopExcluding(n int, excludeKinds ...string) ([]InDegree, error) {
	if n <= 0 {
		n = 10
	}
	if len(excludeKinds) > 0 {
		return g.inDegreeTopFiltered(n, excludeKinds)
	}
	// Ties are broken by FQN, which needs the symbols row; the cut (the n-th
	// largest in-degree) keeps that join to the groups that can still make
	// the top n.
	rows, err := g.reader.QueryContext(readCtx(), `WITH agg AS MATERIALIZED (
  SELECT dst, count(*) AS in_degree
  FROM graph_edges
  WHERE kind = `+labelCalls+` AND dst_external = 0
  GROUP BY dst
),
cut AS (SELECT min(in_degree) AS c FROM (SELECT in_degree FROM agg ORDER BY in_degree DESC LIMIT ?1))
SELECT s.fqn, s.name, s.kind, s.file, a.in_degree
FROM agg a JOIN symbols s ON s.id = a.dst
WHERE a.in_degree >= (SELECT c FROM cut)
ORDER BY a.in_degree DESC, s.fqn
LIMIT ?1`, n)
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

func (g *SQLite) inDegreeTopFiltered(n int, excludeKinds []string) ([]InDegree, error) {
	args := make([]any, 0, len(excludeKinds)+1)
	marks := make([]string, len(excludeKinds))
	for i, k := range excludeKinds {
		marks[i] = "?"
		args = append(args, k)
	}
	args = append(args, n)
	rows, err := g.reader.QueryContext(readCtx(), `SELECT s.fqn, s.name, s.kind, s.file, count(*) AS in_degree
FROM graph_edges e JOIN symbols s ON s.id = e.dst
WHERE e.kind = `+labelCalls+` AND e.dst_external = 0 AND s.kind NOT IN (`+strings.Join(marks, ",")+`)
GROUP BY e.dst
ORDER BY in_degree DESC, s.fqn
LIMIT ?`, args...)
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

// HasTreeSitterEdges reports whether the code graph contains tree-sitter CALLS edges
// for non-Go source files, or recorded that it was built with tree-sitter.
func (g *SQLite) HasTreeSitterEdges() bool {
	var val string
	if err := g.reader.QueryRowContext(readCtx(), `SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&val); err == nil {
		return val == "1"
	}
	var callsID int
	if err := g.reader.QueryRowContext(readCtx(), `SELECT id FROM graph_labels WHERE label = 'CALLS'`).Scan(&callsID); err != nil {
		return false
	}
	var count int
	_ = g.reader.QueryRowContext(readCtx(), `
		SELECT 1 FROM symbols s
		JOIN graph_edges e ON e.src = s.id
		WHERE s.file NOT LIKE '%.go' AND e.kind = ?
		LIMIT 1
	`, callsID).Scan(&count)
	return count > 0
}

// SetMeta sets a key-value pair in the meta table.
func (g *SQLite) SetMeta(ctx context.Context, key, value string) error {
	_, err := g.writer.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("setting meta %s: %w", key, err)
	}
	return nil
}

// SetMetaTx sets a key-value pair in the meta table within tx.
func (g *SQLite) SetMetaTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("setting meta %s: %w", key, err)
	}
	return nil
}

// ClearMeta deletes a key from the meta table.
func (g *SQLite) ClearMeta(ctx context.Context, key string) error {
	_, err := g.writer.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("clearing meta %s: %w", key, err)
	}
	return nil
}

// ClearMetaTx deletes a key from the meta table within tx.
func (g *SQLite) ClearMetaTx(ctx context.Context, tx *sql.Tx, key string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, key); err != nil {
		return fmt.Errorf("clearing meta %s: %w", key, err)
	}
	return nil
}

// ClearMetaPrefix deletes all keys starting with prefix from the meta table.
func (g *SQLite) ClearMetaPrefix(ctx context.Context, prefix string) error {
	_, err := g.writer.ExecContext(ctx, `DELETE FROM meta WHERE key LIKE ? || '%'`, prefix)
	if err != nil {
		return fmt.Errorf("clearing meta prefix %s: %w", prefix, err)
	}
	return nil
}

// ClearMetaPrefixTx deletes all keys starting with prefix within tx.
func (g *SQLite) ClearMetaPrefixTx(ctx context.Context, tx *sql.Tx, prefix string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key LIKE ? || '%'`, prefix); err != nil {
		return fmt.Errorf("clearing meta prefix %s: %w", prefix, err)
	}
	return nil
}

// applyMetaMutationsTx applies a graph write's metadata mutations inside tx in
// a deterministic order:
//
//  1. prefix deletions (nil value, key ending in "%"), sorted by key;
//  2. exact key deletions (nil value), sorted by key;
//  3. key sets, sorted by key.
//
// The order is a contract, not an optimisation: a full lite build clears
// "graph_nongo_lite:%" and re-sets the per-file "graph_nongo_lite:<rel>"
// markers in one map. Go map iteration order is randomized, so applying the
// map directly lets the prefix deletion run after the marker sets and wipe
// them (rv-lite-nongo-v4): a later Go-only sync then recomputes
// graph_treesitter to "1" beside lite-extracted symbols and silences
// NonGoSymbolNotice. Sorting each phase by key also keeps the write order
// identical between runs.
//
// A nil-valued key ending in "%" is a prefix deletion in the caller's terms:
// its value is a LIKE pattern, so the trailing wildcard is stripped and the
// remainder is passed to ClearMetaPrefixTx (prefix + "%" is the same pattern).
func (g *SQLite) applyMetaMutationsTx(ctx context.Context, tx *sql.Tx, metaMutations map[string]*string) error {
	if len(metaMutations) == 0 {
		return nil
	}
	var prefixDeletes, exactDeletes, sets []string
	for k, v := range metaMutations {
		switch {
		case v == nil && strings.HasSuffix(k, "%"):
			prefixDeletes = append(prefixDeletes, k)
		case v == nil:
			exactDeletes = append(exactDeletes, k)
		default:
			sets = append(sets, k)
		}
	}
	sort.Strings(prefixDeletes)
	sort.Strings(exactDeletes)
	sort.Strings(sets)
	for _, k := range prefixDeletes {
		if err := g.ClearMetaPrefixTx(ctx, tx, strings.TrimSuffix(k, "%")); err != nil {
			return err
		}
	}
	for _, k := range exactDeletes {
		if err := g.ClearMetaTx(ctx, tx, k); err != nil {
			return err
		}
	}
	for _, k := range sets {
		if err := g.SetMetaTx(ctx, tx, k, *metaMutations[k]); err != nil {
			return err
		}
	}
	return nil
}

// HasNonGoFiles reports whether the code graph contains symbols declared in non-Go files.
func (g *SQLite) HasNonGoFiles() (bool, error) {
	return hasNonGoFiles(readCtx(), g.reader)
}

// HasNonGoFilesTx is HasNonGoFiles within tx: uncommitted deltas are visible
// there, while g.reader (WAL) would still see the pre-transaction state.
func (g *SQLite) HasNonGoFilesTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	return hasNonGoFiles(ctx, tx)
}

// rowQueryer is satisfied by *sql.DB and *sql.Tx.
type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func hasNonGoFiles(ctx context.Context, q rowQueryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM symbols WHERE file NOT LIKE '%.go' LIMIT 1`).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking non-Go symbols: %w", err)
	}
	return count > 0, nil
}

// NonGoFileCount returns the count of distinct non-Go files declared in the symbols table.
func (g *SQLite) NonGoFileCount() (int, error) {
	var count int
	err := g.reader.QueryRowContext(readCtx(), `SELECT COUNT(DISTINCT file) FROM symbols WHERE file NOT LIKE '%.go'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting non-Go files: %w", err)
	}
	return count, nil
}

// LiteNonGoCount returns the number of non-Go files in the graph recorded as extracted in lite mode.
func (g *SQLite) LiteNonGoCount() (int, error) {
	return liteNonGoCount(readCtx(), g.reader)
}

// LiteNonGoCountTx is LiteNonGoCount within tx (see HasNonGoFilesTx on why the
// caller-owned transaction must be queried, not g.reader).
func (g *SQLite) LiteNonGoCountTx(ctx context.Context, tx *sql.Tx) (int, error) {
	return liteNonGoCount(ctx, tx)
}

func liteNonGoCount(ctx context.Context, q rowQueryer) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM meta WHERE key LIKE 'graph_nongo_lite:%'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting lite non-Go files: %w", err)
	}
	return count, nil
}

// RecomputeTreeSitterMetaTx recomputes the global graph_treesitter key (and
// clears the lite markers when the graph holds no non-Go declarations) within
// tx. Running it inside every file delta's transaction means a committed
// delta, its graph_nongo_lite marker and the global key are always mutually
// consistent: a failed key write rolls the whole delta back, and no committed
// state can leave a stale "1" beside lite-extracted rows.
func (g *SQLite) RecomputeTreeSitterMetaTx(ctx context.Context, tx *sql.Tx) error {
	hasNonGo, err := g.HasNonGoFilesTx(ctx, tx)
	if err != nil {
		return err
	}
	if !hasNonGo {
		if err := g.ClearMetaTx(ctx, tx, store.MetaGraphTreeSitter); err != nil {
			return err
		}
		return g.ClearMetaPrefixTx(ctx, tx, "graph_nongo_lite:")
	}
	liteCount, err := g.LiteNonGoCountTx(ctx, tx)
	if err != nil {
		return err
	}
	if liteCount > 0 {
		return g.SetMetaTx(ctx, tx, store.MetaGraphTreeSitter, "0")
	}
	return g.SetMetaTx(ctx, tx, store.MetaGraphTreeSitter, "1")
}

// SyncTreeSitterMeta recomputes the graph_treesitter key in its own
// transaction. After a sync each delta has already done this atomically; the
// standalone call converges legacy or manually edited metadata (for example a
// graph built before the key existed) without ever leaving an autocommit
// half-write behind.
func (g *SQLite) SyncTreeSitterMeta(ctx context.Context) error {
	tx, err := g.writer.BeginTx(ctx, nil) // _txlock=immediate
	if err != nil {
		return fmt.Errorf("syncing tree-sitter metadata: beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if err := g.RecomputeTreeSitterMetaTx(ctx, tx); err != nil {
		return fmt.Errorf("syncing tree-sitter metadata: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("syncing tree-sitter metadata: committing: %w", err)
	}
	return nil
}
