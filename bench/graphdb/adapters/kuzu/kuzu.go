//go:build kuzu

// Package kuzu is the Kùzu candidate adapter (embedded property-graph
// database, CGo). Build with -tags kuzu; importing the package registers the
// backend as "kuzu".
//
// It uses the Kùzu Go binding directly, with its own schema and Cypher, ported
// from the Kùzu code graph gleann used to ship (internal/graph/kuzu, removed):
//   - schema: CodeFile and Symbol node tables, DECLARES and CALLS rel tables;
//   - full load: one CSV COPY FROM per table (PARALLEL=false);
//   - callers/callees at depth N: a breadth-first search in Go over single-hop
//     Cypher queries, the way gleann's Impact and Neighbors did it (Cypher
//     variable-length paths enumerate paths, not nodes, and are far slower);
//   - impact: that caller BFS plus one DECLARES lookup per affected symbol;
//   - shortest path: undirected BFS in Go over single-hop queries, abandoned
//     after 1000 explored nodes (gleann's cap, see Caveats);
//   - one-file update: capture the edges other files point at the file's
//     symbols, DETACH DELETE the file's symbols and node, COPY the new rows,
//     then restore the captured edges whose target symbol still exists.
package kuzu

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gokuzu "github.com/kuzudb/go-kuzu"

	"github.com/tevfik/gleann/bench/graphdb"
)

// pathExploreCap is the number of nodes the shortest-path BFS visits before
// giving up, as in gleann's kuzu.ShortestPath.
const pathExploreCap = 1000

func init() {
	graphdb.Register("kuzu", func() graphdb.Backend { return &backend{} })
}

type backend struct {
	dir  string
	db   *gokuzu.Database
	conn *gokuzu.Connection
}

func (b *backend) Name() string { return "kuzu" }

func (b *backend) Caveats() []string {
	return []string{
		fmt.Sprintf("ShortestPath gives up after exploring %d nodes (gleann's cap), so on large graphs it reports no path where the reference finds one; those show up as path mismatches.", pathExploreCap),
		"Callers/Callees/Impact at depth N are a BFS in Go over single-hop Cypher queries, like gleann's own Kùzu code did.",
	}
}

func (b *backend) Open(dir string) error {
	b.dir = dir
	db, err := gokuzu.OpenDatabase(filepath.Join(dir, "graph"), gokuzu.DefaultSystemConfig())
	if err != nil {
		return fmt.Errorf("opening kuzu database: %w", err)
	}
	conn, err := gokuzu.OpenConnection(db)
	if err != nil {
		db.Close()
		return fmt.Errorf("opening kuzu connection: %w", err)
	}
	b.db, b.conn = db, conn
	for _, ddl := range []string{
		`CREATE NODE TABLE CodeFile(path STRING, lang STRING, PRIMARY KEY (path))`,
		`CREATE NODE TABLE Symbol(fqn STRING, kind STRING, file STRING, line INT64, name STRING, PRIMARY KEY (fqn))`,
		`CREATE REL TABLE DECLARES(FROM CodeFile TO Symbol, MANY_MANY)`,
		`CREATE REL TABLE CALLS(FROM Symbol TO Symbol, MANY_MANY)`,
	} {
		if err := b.exec(ddl); err != nil {
			_ = b.Close()
			return fmt.Errorf("creating schema: %w", err)
		}
	}
	return nil
}

func (b *backend) exec(cypher string) error {
	res, err := b.conn.Query(cypher)
	if err != nil {
		return err
	}
	res.Close()
	return nil
}

// column runs a query and returns the string column col of every row.
func (b *backend) column(cypher, col string) ([]string, error) {
	res, err := b.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var out []string
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprint(m[col]))
	}
	return out, nil
}

// pairs runs a query returning columns s and t and returns them as edges.
func (b *backend) pairs(cypher string) ([]graphdb.Edge, error) {
	res, err := b.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var out []graphdb.Edge
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, err
		}
		out = append(out, graphdb.Edge{Src: fmt.Sprint(m["s"]), Dst: fmt.Sprint(m["t"])})
	}
	return out, nil
}

func (b *backend) count(cypher string) (int, error) {
	res, err := b.conn.Query(cypher)
	if err != nil {
		return 0, err
	}
	defer res.Close()
	if !res.HasNext() {
		return 0, nil
	}
	row, err := res.Next()
	if err != nil {
		return 0, err
	}
	m, err := row.GetAsMap()
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(fmt.Sprint(m["c"]))
	if err != nil {
		return 0, fmt.Errorf("count result %v: %w", m["c"], err)
	}
	return n, nil
}

// clean strips what Kùzu's CSV importer cannot take, like gleann's writer did.
func clean(s string) string {
	r := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ", ",", ";", `\`, "", `"`, "")
	return strings.TrimSpace(r.Replace(s))
}

// copyCSV writes the rows to a temporary CSV and COPYs it into table.
func (b *backend) copyCSV(table string, header []string, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}
	f, err := os.CreateTemp(b.dir, table+"_*.csv")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	w := csv.NewWriter(f)
	err = w.Write(header)
	for i := 0; err == nil && i < len(rows); i++ {
		err = w.Write(rows[i])
	}
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing %s csv: %w", table, err)
	}
	if err := b.exec(fmt.Sprintf(`COPY %s FROM %q (HEADER=true, PARALLEL=false)`, table, path)); err != nil {
		return fmt.Errorf("copy into %s: %w", table, err)
	}
	return nil
}

func (b *backend) load(deltas []graphdb.FileDelta, extra []graphdb.Edge) error {
	var files, syms, decls, calls [][]string
	for _, d := range deltas {
		files = append(files, []string{clean(d.File), "go"})
		for _, s := range d.Symbols {
			file := s.File
			if file == "" {
				file = d.File
			}
			syms = append(syms, []string{clean(s.FQN), clean(s.Kind), clean(file), strconv.FormatInt(s.Line, 10), clean(s.Name)})
			decls = append(decls, []string{clean(file), clean(s.FQN)})
		}
		for _, e := range d.Edges {
			calls = append(calls, []string{clean(e.Src), clean(e.Dst)})
		}
	}
	for _, e := range extra {
		calls = append(calls, []string{clean(e.Src), clean(e.Dst)})
	}
	for _, t := range []struct {
		table  string
		header []string
		rows   [][]string
	}{
		{"CodeFile", []string{"path", "lang"}, files},
		{"Symbol", []string{"fqn", "kind", "file", "line", "name"}, syms},
		{"DECLARES", []string{"from", "to"}, decls},
		{"CALLS", []string{"from", "to"}, calls},
	} {
		if err := b.copyCSV(t.table, t.header, t.rows); err != nil {
			return err
		}
	}
	return nil
}

func (b *backend) BulkLoad(_ context.Context, deltas []graphdb.FileDelta) error {
	return b.load(deltas, nil)
}

func (b *backend) RemoveFile(_ context.Context, file string) error {
	file = clean(file)
	// Relationships are deleted explicitly before the nodes, as gleann's
	// RemoveFileSymbols did: DETACH DELETE left a stale row in the CALLS
	// count (observed with go-kuzu 0.11.3), so counts drifted from the data.
	for _, q := range []string{
		`MATCH (s:Symbol {file: %q})-[r:CALLS]->() DELETE r`,
		`MATCH (s:Symbol {file: %q})<-[r:CALLS]-() DELETE r`,
		`MATCH (f:CodeFile {path: %q})-[r:DECLARES]->() DELETE r`,
		`MATCH (s:Symbol {file: %q}) DELETE s`,
		`MATCH (f:CodeFile {path: %q}) DELETE f`,
	} {
		if err := b.exec(fmt.Sprintf(q, file)); err != nil {
			return err
		}
	}
	return nil
}

func (b *backend) ApplyFileDelta(ctx context.Context, d graphdb.FileDelta) error {
	file := clean(d.File)
	// Edges other files own that point into this file. DETACH DELETE drops
	// them with the symbols, so capture them first.
	inbound, err := b.pairs(fmt.Sprintf(
		`MATCH (a:Symbol)-[:CALLS]->(t:Symbol {file: %q}) WHERE a.file <> %q RETURN a.fqn AS s, t.fqn AS t`, file, file))
	if err != nil {
		return fmt.Errorf("capturing inbound edges: %w", err)
	}
	if err := b.RemoveFile(ctx, d.File); err != nil {
		return fmt.Errorf("removing old rows: %w", err)
	}
	declared := make(map[string]bool, len(d.Symbols))
	for _, s := range d.Symbols {
		declared[clean(s.FQN)] = true
	}
	owned := make(map[[2]string]bool, len(d.Edges))
	for _, e := range d.Edges {
		owned[[2]string{clean(e.Src), clean(e.Dst)}] = true
	}
	var restore []graphdb.Edge
	for _, e := range inbound {
		k := [2]string{e.Src, e.Dst}
		if declared[e.Dst] && !owned[k] {
			owned[k] = true
			restore = append(restore, e)
		}
	}
	return b.load([]graphdb.FileDelta{d}, restore)
}

// bfs walks single-hop CALLS queries level by level up to depth and returns
// the visited set without the start symbol.
func (b *backend) bfs(fqn string, depth int, forward bool) ([]string, error) {
	q := `MATCH (a:Symbol)-[:CALLS]->(b:Symbol {fqn: %q}) RETURN DISTINCT a.fqn AS fqn`
	if forward {
		q = `MATCH (a:Symbol {fqn: %q})-[:CALLS]->(b:Symbol) RETURN DISTINCT b.fqn AS fqn`
	}
	seen := map[string]bool{fqn: true}
	frontier := []string{fqn}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, n := range frontier {
			nb, err := b.column(fmt.Sprintf(q, n), "fqn")
			if err != nil {
				return nil, err
			}
			for _, x := range nb {
				if !seen[x] {
					seen[x] = true
					next = append(next, x)
				}
			}
		}
		frontier = next
	}
	delete(seen, fqn)
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func (b *backend) Callers(_ context.Context, fqn string, depth int) ([]string, error) {
	return b.bfs(fqn, depth, false)
}

func (b *backend) Callees(_ context.Context, fqn string, depth int) ([]string, error) {
	return b.bfs(fqn, depth, true)
}

func (b *backend) Impact(ctx context.Context, fqn string, depth int) (graphdb.ImpactResult, error) {
	callers, err := b.Callers(ctx, fqn, depth)
	if err != nil {
		return graphdb.ImpactResult{}, err
	}
	files := map[string]bool{}
	for _, c := range callers {
		paths, err := b.column(fmt.Sprintf(`MATCH (f:CodeFile)-[:DECLARES]->(s:Symbol {fqn: %q}) RETURN f.path AS p`, c), "p")
		if err != nil {
			return graphdb.ImpactResult{}, err
		}
		for _, p := range paths {
			files[p] = true
		}
	}
	out := graphdb.ImpactResult{Callers: callers}
	for f := range files {
		out.Files = append(out.Files, f)
	}
	sort.Strings(out.Files)
	return out, nil
}

func (b *backend) SymbolSearch(_ context.Context, pattern string, limit int) ([]string, error) {
	p := strings.ToLower(pattern)
	out, err := b.column(fmt.Sprintf(
		`MATCH (s:Symbol) WHERE lower(s.fqn) CONTAINS %q OR lower(s.name) CONTAINS %q RETURN s.fqn AS fqn ORDER BY s.fqn LIMIT %d`,
		p, p, limit), "fqn")
	sort.Strings(out)
	return out, err
}

func (b *backend) ShortestPath(_ context.Context, from, to string, maxDepth int) (int, error) {
	if from == to {
		return 0, nil
	}
	dist := map[string]int{from: 0}
	queue := []string{from}
	for len(queue) > 0 && len(dist) < pathExploreCap {
		cur := queue[0]
		queue = queue[1:]
		if dist[cur] >= maxDepth {
			continue
		}
		for _, q := range []string{
			`MATCH (a:Symbol {fqn: %q})-[:CALLS]->(b:Symbol) RETURN b.fqn AS fqn`,
			`MATCH (a:Symbol)-[:CALLS]->(b:Symbol {fqn: %q}) RETURN a.fqn AS fqn`,
		} {
			nb, err := b.column(fmt.Sprintf(q, cur), "fqn")
			if err != nil {
				return 0, err
			}
			for _, x := range nb {
				if _, ok := dist[x]; ok {
					continue
				}
				dist[x] = dist[cur] + 1
				if x == to {
					return dist[x], nil
				}
				queue = append(queue, x)
			}
		}
	}
	return graphdb.NoPath, nil
}

func (b *backend) Stats(context.Context) (graphdb.Stats, error) {
	var s graphdb.Stats
	var err error
	if s.Files, err = b.count(`MATCH (f:CodeFile) RETURN count(f) AS c`); err != nil {
		return s, err
	}
	if s.Symbols, err = b.count(`MATCH (s:Symbol) RETURN count(s) AS c`); err != nil {
		return s, err
	}
	s.CallEdges, err = b.count(`MATCH (a:Symbol)-[r:CALLS]->(b:Symbol) RETURN count(r) AS c`)
	return s, err
}

func (b *backend) DiskBytes() (int64, error) { return graphdb.DirSize(b.dir) }

func (b *backend) Close() error {
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
	if b.db != nil {
		b.db.Close()
		b.db = nil
	}
	return nil
}
