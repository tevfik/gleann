// Command corpusbench builds a lexical-only index (and optionally the code
// graph) of a REAL source tree through indexing.Build, the entry point of
// `gleann index build --instant`, so the part of a build that is neither
// embedding nor vector indexing can be timed, profiled and compared between
// two checkouts: file walk, reading, chunking, tokenization, staging into
// SQLite/FTS5, the manifest, graph extraction and the graph write.
//
//	cd bench/vectordb
//	taskset -c 4-7 go run ./cmd/corpusbench -docs /path/to/repo -graph \
//	    -cpuprofile cpu.prof -memprofile mem.prof -hash
//
// Every status line of the build is printed with the wall time and the CPU
// time (this process and its children: the exact Go resolver runs `go list`)
// spent since the start, so the phases can be read off the output.
//
// -hash prints content hashes of the tables of the index database after the
// build; `-hashdb <file.db>` prints them for an existing database and builds
// nothing. "raw" hashes cover every column, including row ids. "logical"
// hashes replace the passage row id by the stable passage ID, because the
// reader hands files to a worker pool and collects them in completion order:
// with more than one CPU the row ids differ from run to run, the content does
// not. Pin the run to one CPU (taskset -c N) to compare raw hashes.
//
// No model is needed, nothing is read from or written to ~/.gleann/indexes or
// the vault: the index and a private file tracker live under -dir.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

func main() {
	docs := flag.String("docs", "", "source tree to index")
	dir := flag.String("dir", "", "scratch index directory (removed first; default: a new temp dir)")
	name := flag.String("name", "x", "index name")
	mode := flag.String("mode", "code", "index mode: code, docs or all")
	graph := flag.Bool("graph", false, "also build the code graph")
	noTracker := flag.Bool("no-tracker", false, "build without a file tracker (the CLI has one; it adds the file hash to the passage metadata)")
	cpu := flag.String("cpuprofile", "", "write a CPU profile of the build")
	mem := flag.String("memprofile", "", "write an allocation profile of the build")
	hash := flag.Bool("hash", false, "print content hashes of the index tables after the build")
	hashDB := flag.String("hashdb", "", "print content hashes of this index database and exit")
	quiet := flag.Bool("quiet", false, "do not print the build's status lines")
	flag.Parse()

	if *hashDB != "" {
		if err := printHashes(os.Stdout, *hashDB); err != nil {
			fatal("hashing %s: %v", *hashDB, err)
		}
		return
	}
	if *docs == "" {
		fatal("-docs is required")
	}
	if *dir == "" {
		d, err := os.MkdirTemp("", "corpusbench-")
		if err != nil {
			fatal("creating the scratch directory: %v", err)
		}
		*dir = d
	} else {
		if err := os.RemoveAll(*dir); err != nil {
			fatal("clearing %s: %v", *dir, err)
		}
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			fatal("creating %s: %v", *dir, err)
		}
	}

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = *dir
	opts := indexing.BuildOptions{
		Options: indexing.Options{
			Config:    cfg,
			Mode:      indexing.ParseMode(*mode),
			NoPlugins: true,
			NoTracker: *noTracker,
		},
		Name:        *name,
		DocsDir:     *docs,
		InstantOnly: true,
		Graph:       *graph,
	}
	if !*noTracker {
		t, err := vault.NewTracker(filepath.Join(*dir, "vault.db"))
		if err != nil {
			fatal("opening the file tracker: %v", err)
		}
		defer t.Close()
		opts.Tracker = t
	}

	start := time.Now()
	cpu0 := cpuSeconds()
	var lines io.Writer = io.Discard
	if !*quiet {
		lines = &stampWriter{out: os.Stdout, start: start, cpu0: cpu0}
	}
	opts.Stdout, opts.Stderr = lines, lines

	if *cpu != "" {
		f, err := os.Create(*cpu)
		if err != nil {
			fatal("cpuprofile: %v", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fatal("cpuprofile: %v", err)
		}
	}
	res, err := indexing.Build(context.Background(), opts)
	wall := time.Since(start)
	if *cpu != "" {
		pprof.StopCPUProfile()
	}
	if err != nil {
		fatal("build: %v", err)
	}
	if *mem != "" {
		f, err := os.Create(*mem)
		if err != nil {
			fatal("memprofile: %v", err)
		}
		if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
			fatal("memprofile: %v", err)
		}
		f.Close()
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Printf("chunks=%d cpus=%d graph=%v wall=%.2fs cpu=%.2fs alloc=%dMB mallocs=%dM gc=%d peak_rss=%dMB\n",
		res.Chunks, runtime.NumCPU(), *graph, wall.Seconds(), cpuSeconds()-cpu0,
		ms.TotalAlloc>>20, ms.Mallocs/1_000_000, ms.NumGC, peakRSSKB()>>10)

	if *hash {
		if err := printHashes(os.Stdout, filepath.Join(*dir, *name, *name+".db")); err != nil {
			fatal("hashing the index: %v", err)
		}
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "corpusbench: "+format+"\n", a...)
	os.Exit(1)
}

// stampWriter prefixes every line with the wall and CPU seconds since start.
type stampWriter struct {
	mu    sync.Mutex
	out   io.Writer
	start time.Time
	cpu0  float64
	buf   bytes.Buffer
}

func (w *stampWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			return len(p), nil
		}
		line := w.buf.Next(i + 1)
		fmt.Fprintf(w.out, "[wall %7.3fs cpu %7.3fs] %s", time.Since(w.start).Seconds(), cpuSeconds()-w.cpu0, line)
	}
}

// cpuSeconds is the user+system CPU time of this process and of the children
// it has waited for, from /proc/self/stat (Linux; 0 elsewhere).
func cpuSeconds() float64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	// The command name (field 2) may contain spaces: fields are counted after
	// its closing parenthesis, where field 3 (state) comes first.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	const utime = 14 - 3 // utime, stime, cutime, cstime are fields 14 to 17
	if len(f) < utime+4 {
		return 0
	}
	var ticks int64
	for _, s := range f[utime : utime+4] {
		n, _ := strconv.ParseInt(s, 10, 64)
		ticks += n
	}
	return float64(ticks) / 100 // USER_HZ is 100 on every Linux architecture Go supports
}

// peakRSSKB is VmHWM from /proc/self/status (Linux; 0 elsewhere).
func peakRSSKB() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				n, _ := strconv.ParseInt(f[0], 10, 64)
				return n
			}
		}
	}
	return 0
}

// ── content hashes ────────────────────────────────────────────────────

// volatileColumns hold the time of the build, not content.
var volatileColumns = map[string]bool{"files.indexed_at": true}

// printHashes prints one sha256 per table of the index database at dbPath.
//
// raw: every ordinary table, all columns, rows sorted by all columns; the two
// FTS5 indexes (lex over the passage tokens, symbols_fts over the symbol
// names) as their fts5vocab 'instance' listing, which is every (term, rowid,
// column, position) the index holds.
//
// logical: the same content without the passage row id, which depends on the
// order the reader's workers finish in: passages keyed by pid, and the lex
// instances with the rowid replaced by the pid.
func printHashes(w io.Writer, dbPath string) error {
	st, err := store.Open(dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer st.Close()
	ctx := context.Background()
	// One connection: the TEMP vocab tables below exist only on it.
	c, err := st.ReaderDB().Conn(ctx)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer c.Close()

	tables, err := ordinaryTables(ctx, c)
	if err != nil {
		return err
	}
	for _, t := range tables {
		cols, err := tableColumns(ctx, c, t)
		if err != nil {
			return err
		}
		q := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(t) + " ORDER BY " + strings.Join(cols, ", ")
		if err := hashQuery(ctx, c, w, "raw", t, q); err != nil {
			return err
		}
	}
	for _, fts := range []string{"lex", "symbols_fts"} {
		if _, err := c.ExecContext(ctx, "CREATE VIRTUAL TABLE temp."+fts+"_inst USING fts5vocab(main, "+fts+", 'instance')"); err != nil {
			return fmt.Errorf("creating the instance vocabulary of %s: %w", fts, err)
		}
		q := "SELECT term, doc, col, offset FROM temp." + fts + "_inst ORDER BY term, doc, col, offset"
		if err := hashQuery(ctx, c, w, "raw", fts, q); err != nil {
			return err
		}
	}
	if err := hashQuery(ctx, c, w, "raw", "lex_vocab", "SELECT term, doc, cnt FROM lex_vocab ORDER BY term"); err != nil {
		return err
	}

	if err := hashQuery(ctx, c, w, "logical", "passages",
		"SELECT pid, source, chunk_no, vec_slot, kind, text, meta FROM passages ORDER BY pid"); err != nil {
		return err
	}
	return hashQuery(ctx, c, w, "logical", "lex",
		"SELECT p.pid, i.term, i.col, i.offset FROM temp.lex_inst i JOIN passages p ON p.id = i.doc ORDER BY p.pid, i.offset, i.term")
}

func quote(ident string) string { return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"` }

// ordinaryTables lists the rowid and WITHOUT ROWID tables, without the
// virtual tables and their shadow tables (page layout, not content).
func ordinaryTables(ctx context.Context, c *sql.Conn) ([]string, error) {
	rows, err := c.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()
	var all, virtual []string
	for rows.Next() {
		var name string
		var ddl sql.NullString
		if err := rows.Scan(&name, &ddl); err != nil {
			return nil, fmt.Errorf("listing tables: %w", err)
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(ddl.String)), "CREATE VIRTUAL") {
			virtual = append(virtual, name)
			continue
		}
		all = append(all, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	var out []string
	for _, t := range all {
		shadow := strings.HasPrefix(t, "sqlite_")
		for _, v := range virtual {
			shadow = shadow || strings.HasPrefix(t, v+"_")
		}
		if !shadow {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, nil
}

func tableColumns(ctx context.Context, c *sql.Conn, table string) ([]string, error) {
	rows, err := c.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, fmt.Errorf("reading the columns of %s: %w", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("reading the columns of %s: %w", table, err)
		}
		if !volatileColumns[table+"."+name] {
			cols = append(cols, quote(name))
		}
	}
	return cols, rows.Err()
}

// hashQuery prints the row count and the sha256 of the rows of query, each
// value written with its type and length so no two row sets collide by
// concatenation.
func hashQuery(ctx context.Context, c *sql.Conn, w io.Writer, kind, label, query string) error {
	rows, err := c.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("hashing %s: %w", label, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("hashing %s: %w", label, err)
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	h := sha256.New()
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("hashing %s: %w", label, err)
		}
		for _, v := range vals {
			switch x := v.(type) {
			case nil:
				fmt.Fprint(h, "n;")
			case []byte:
				fmt.Fprintf(h, "b%d:", len(x))
				h.Write(x)
			case string:
				fmt.Fprintf(h, "s%d:", len(x))
				io.WriteString(h, x)
			default:
				fmt.Fprintf(h, "%T:%v;", x, x)
			}
		}
		fmt.Fprint(h, "\n")
		n++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("hashing %s: %w", label, err)
	}
	fmt.Fprintf(w, "hash %-7s %-16s rows=%-8d sha256=%s\n", kind, label, n, hex.EncodeToString(h.Sum(nil)))
	return nil
}
