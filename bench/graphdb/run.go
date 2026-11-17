package graphdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/graphstore"
)

// Reference is the backend every candidate is compared against.
const Reference = "sqlite"

// searchLimit is the SymbolSearch cap (gleann's symbolSearchLimit).
const searchLimit = 50

// pathMaxDepth is passed to ShortestPath (gleann's maxPathDepth).
const pathMaxDepth = 10

// Config parameterises a run.
type Config struct {
	Sizes    []int    // edge counts; symbols = edges/5, files = edges/500
	Queries  int      // sampled start symbols per query type
	Paths    int      // shortest-path pairs
	Backends []string // registered names; the reference always runs first
	WorkDir  string   // parent of the per-backend scratch directories; "" = os.TempDir
	Log      func(format string, args ...any)
}

// OpStats is the latency distribution of one query type.
type OpStats struct {
	N     int   `json:"n"`
	P50us int64 `json:"p50_us"`
	P95us int64 `json:"p95_us"`
}

// Result is the measurement of one backend at one size.
type Result struct {
	Backend        string             `json:"backend"`
	Edges          int                `json:"edges"`
	LoadMS         float64            `json:"load_ms"`
	UpdateMS       float64            `json:"update_ms"`
	RemoveMS       float64            `json:"remove_ms"`
	DiskBytes      int64              `json:"disk_bytes"`
	PeakRSSDeltaKB int64              `json:"peak_rss_delta_kb"` // 0 where /proc is unavailable
	Latency        map[string]OpStats `json:"latency"`
	Mismatches     map[string]int     `json:"mismatches"` // per check vs the reference; absent = 0
	Compared       map[string]int     `json:"compared"`
	Skipped        map[string]int     `json:"skipped"` // not compared (search results at the cap)
	Examples       []string           `json:"examples,omitempty"`
	Caveats        []string           `json:"caveats,omitempty"`
	Error          string             `json:"error,omitempty"`
}

// TotalMismatches counts every mismatch and error of the result.
func (r *Result) TotalMismatches() int {
	n := 0
	for _, m := range r.Mismatches {
		n += m
	}
	if r.Error != "" {
		n++
	}
	return n
}

// SizeRun is every backend's result at one size.
type SizeRun struct {
	EdgesRequested int       `json:"edges_requested"`
	EdgesStored    int       `json:"edges_stored"`
	Symbols        int       `json:"symbols"`
	Files          int       `json:"files"`
	Results        []*Result `json:"results"`
}

// Report is one invocation of the runner.
type Report struct {
	Time    string    `json:"time"`
	Go      string    `json:"go"`
	Platf   string    `json:"platform"`
	CPUs    int       `json:"cpus"`
	Command string    `json:"command"`
	Queries int       `json:"queries"`
	Paths   int       `json:"paths"`
	Sizes   []SizeRun `json:"sizes"`
}

// answer is a normalised query result reduced to a comparable form.
type answer struct {
	n    int
	sum  [sha256.Size]byte
	err  string
	path int // ShortestPath only
}

func digest(lists ...[]string) answer {
	h := sha256.New()
	n := 0
	for _, l := range lists {
		for _, s := range l {
			h.Write([]byte(s))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
		n += len(l)
	}
	var a answer
	a.n = n
	copy(a.sum[:], h.Sum(nil))
	return a
}

type op struct {
	name string
	run  func(ctx context.Context, b Backend, q string) (answer, bool, error) // answer, capped, error
}

func queryOps() []op {
	list := func(name string, f func(Backend, context.Context, string, int) ([]string, error), depth int) op {
		return op{name, func(ctx context.Context, b Backend, q string) (answer, bool, error) {
			r, err := f(b, ctx, q, depth)
			return digest(r), false, err
		}}
	}
	callers := func(b Backend, ctx context.Context, q string, d int) ([]string, error) { return b.Callers(ctx, q, d) }
	callees := func(b Backend, ctx context.Context, q string, d int) ([]string, error) { return b.Callees(ctx, q, d) }
	impact := func(depth int) op {
		return op{fmt.Sprintf("impact d%d", depth), func(ctx context.Context, b Backend, q string) (answer, bool, error) {
			r, err := b.Impact(ctx, q, depth)
			return digest(r.Callers, r.Files), false, err
		}}
	}
	return []op{
		list("callers d1", callers, 1), list("callers d3", callers, 3),
		list("callees d1", callees, 1), list("callees d3", callees, 3),
		impact(1), impact(3),
		{"search", func(ctx context.Context, b Backend, q string) (answer, bool, error) {
			r, err := b.SymbolSearch(ctx, q, searchLimit)
			return digest(r), len(r) >= searchLimit, err
		}},
	}
}

// plan is everything that is identical for every backend of one size.
type plan struct {
	fx      *graphstore.GraphFixture
	deltas  []FileDelta
	queries []string
	pairs   [][2]string

	// the one-file update: rename symbol oldFQN of renFile to newFQN
	renFile, oldFQN, newFQN string
	update                  FileDelta
	droppedInbound          int
	surviveSrc, surviveDst  string
	removeFile              string
	removedSym              string
}

func newPlan(edges, nQueries, nPaths int) *plan {
	nSyms := max(1, edges/5)
	nFiles := max(1, edges/500)
	fx := graphstore.NewGraphFixture(nSyms, edges, nFiles)
	p := &plan{fx: fx}
	for _, d := range fx.Deltas() {
		fd := FileDelta{File: d.File}
		for _, s := range d.Symbols {
			fd.Symbols = append(fd.Symbols, Symbol{FQN: s.FQN, Name: s.Name, Kind: s.Kind, File: s.File, Line: s.StartLine})
		}
		for _, e := range d.Edges {
			fd.Edges = append(fd.Edges, Edge{Src: e.Src, Dst: e.Dst})
		}
		p.deltas = append(p.deltas, fd)
	}

	rng := rand.New(rand.NewSource(2027))
	nQueries = min(nQueries, nSyms)
	seen := map[int]bool{}
	for len(p.queries) < nQueries {
		i := rng.Intn(nSyms)
		if !seen[i] {
			seen[i] = true
			p.queries = append(p.queries, fx.FQNs[i])
		}
	}
	for len(p.pairs) < nPaths && nSyms > 1 {
		a, b := rng.Intn(nSyms), rng.Intn(nSyms)
		if a != b {
			p.pairs = append(p.pairs, [2]string{fx.FQNs[a], fx.FQNs[b]})
		}
	}

	// The updated symbol: the one with the most edges touching it that come
	// from other files and go out, so inbound handling is exercised.
	idx := make(map[string]int, nSyms)
	for i, f := range fx.FQNs {
		idx[f] = i
	}
	inOther := make([]int, nSyms)
	out := make([]int, nSyms)
	for _, e := range fx.Edges {
		out[idx[e.Src]]++
		if e.File != fx.Files[idx[e.Dst]] {
			inOther[idx[e.Dst]]++
		}
	}
	best := 0
	for i := range nSyms {
		if inOther[i] > 0 && out[i] > 0 && (inOther[best] == 0 || out[best] == 0 || inOther[i]+out[i] > inOther[best]+out[best]) {
			best = i
		}
	}
	p.oldFQN, p.renFile = fx.FQNs[best], fx.Files[best]
	p.newFQN = p.oldFQN + "_r"
	p.droppedInbound = inOther[best]
	upd := FileDelta{File: p.renFile}
	for i, f := range fx.FQNs {
		if fx.Files[i] != p.renFile {
			continue
		}
		name := fx.Names[i]
		if f == p.oldFQN {
			f, name = p.newFQN, name+"_r"
		}
		upd.Symbols = append(upd.Symbols, Symbol{FQN: f, Name: name, Kind: "function", File: p.renFile, Line: fx.Lines[i]})
	}
	rename := func(s string) string {
		if s == p.oldFQN {
			return p.newFQN
		}
		return s
	}
	for _, e := range fx.Edges {
		if e.File == p.renFile {
			upd.Edges = append(upd.Edges, Edge{Src: rename(e.Src), Dst: rename(e.Dst)})
		}
	}
	p.update = upd
	for _, e := range fx.Edges { // an inbound edge that must survive the update
		if e.File != p.renFile && e.Dst != p.oldFQN && fx.Files[idx[e.Dst]] == p.renFile {
			p.surviveSrc, p.surviveDst = e.Src, e.Dst
			break
		}
	}
	// A different file to remove afterwards.
	p.removeFile = fx.Files[(best+nSyms/2+1)%nSyms]
	if p.removeFile == p.renFile {
		p.removeFile = fx.Files[(best+1)%nSyms]
	}
	for i, f := range fx.Files {
		if f == p.removeFile {
			p.removedSym = fx.FQNs[i]
			break
		}
	}
	return p
}

// probes are the post-update / post-remove checks, answered by every backend.
func (p *plan) updateProbes(ctx context.Context, b Backend) ([]answer, error) {
	type probe struct {
		fqn     string
		callers bool
	}
	ps := []probe{{p.oldFQN, true}, {p.oldFQN, false}, {p.newFQN, true}, {p.newFQN, false}}
	if p.surviveDst != "" {
		ps = append(ps, probe{p.surviveDst, true}, probe{p.surviveSrc, false})
	}
	ps = append(ps, probe{p.removedSym, true}, probe{p.removedSym, false})
	var out []answer
	for _, pr := range ps {
		var r []string
		var err error
		if pr.callers {
			r, err = b.Callers(ctx, pr.fqn, 1)
		} else {
			r, err = b.Callees(ctx, pr.fqn, 1)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, digest(r))
	}
	return out, nil
}

// reference holds what the reference backend answered, for parity.
type reference struct {
	ops    map[string][]answer
	path   []answer
	stats  Stats
	probes []answer // after update
	after  Stats    // after update
	rmProb []answer // after remove
	rmStat Stats
}

// Run executes the benchmark for every size and returns the report.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	names := []string{Reference}
	for _, n := range cfg.Backends {
		if n != Reference {
			names = append(names, n)
		}
	}
	for _, n := range names {
		if _, err := New(n); err != nil {
			return nil, err
		}
	}
	rep := &Report{
		Time: time.Now().Format(time.RFC3339), Go: runtime.Version(),
		Platf: runtime.GOOS + "/" + runtime.GOARCH, CPUs: runtime.NumCPU(),
		Queries: cfg.Queries, Paths: cfg.Paths,
	}
	for _, edges := range cfg.Sizes {
		p := newPlan(edges, cfg.Queries, cfg.Paths)
		sr := SizeRun{EdgesRequested: edges, EdgesStored: len(p.fx.Edges), Symbols: p.fx.NSyms, Files: p.fx.NFiles}
		cfg.Log("size %d edges: %d stored, %d symbols, %d files", edges, sr.EdgesStored, sr.Symbols, sr.Files)
		var ref *reference
		for _, name := range names {
			cfg.Log("  backend %s ...", name)
			b, _ := New(name)
			res, r := runBackend(ctx, cfg, b, p, edges, ref)
			if name == Reference {
				ref = r
				if res.Error != "" {
					sr.Results = append(sr.Results, res)
					rep.Sizes = append(rep.Sizes, sr)
					return rep, fmt.Errorf("reference backend failed, nothing to compare against: %s", res.Error)
				}
			}
			sr.Results = append(sr.Results, res)
		}
		rep.Sizes = append(rep.Sizes, sr)
	}
	return rep, nil
}

// checker records mismatches of one backend against the reference.
type checker struct {
	res *Result
}

func (c *checker) note(check, detail string) {
	c.res.Mismatches[check]++
	if len(c.res.Examples) < 12 {
		c.res.Examples = append(c.res.Examples, check+": "+detail)
	}
}

func (c *checker) compare(check, detail string, got, want answer) {
	c.res.Compared[check]++
	if got.err != "" {
		c.note(check, detail+": error: "+got.err)
		return
	}
	if got.n != want.n || got.sum != want.sum {
		c.note(check, fmt.Sprintf("%s: %d results, reference %d", detail, got.n, want.n))
	}
}

func runBackend(ctx context.Context, cfg Config, b Backend, p *plan, edges int, ref *reference) (*Result, *reference) {
	res := &Result{
		Backend: b.Name(), Edges: edges,
		Latency: map[string]OpStats{}, Mismatches: map[string]int{}, Compared: map[string]int{}, Skipped: map[string]int{},
	}
	if c, ok := b.(Caveater); ok {
		res.Caveats = c.Caveats()
	}
	isRef := ref == nil
	out := &reference{ops: map[string][]answer{}}
	chk := &checker{res}
	fail := func(format string, args ...any) (*Result, *reference) {
		res.Error = fmt.Sprintf(format, args...)
		_ = b.Close()
		return res, out
	}

	dir, err := os.MkdirTemp(cfg.WorkDir, "graphdb-"+b.Name()+"-")
	if err != nil {
		return fail("creating scratch dir: %v", err)
	}
	defer os.RemoveAll(dir)

	debug.FreeOSMemory()
	resetPeakRSS()
	rss0 := currentRSSKB()

	if err := b.Open(dir); err != nil {
		return fail("open: %v", err)
	}
	start := time.Now()
	if err := b.BulkLoad(ctx, p.deltas); err != nil {
		return fail("bulk load: %v", err)
	}
	res.LoadMS = ms(time.Since(start))

	st, err := b.Stats(ctx)
	if err != nil {
		return fail("stats: %v", err)
	}
	want := Stats{Files: p.fx.NFiles, Symbols: p.fx.NSyms, CallEdges: len(p.fx.Edges)}
	if st != want {
		chk.note("stats", fmt.Sprintf("after load %+v, want %+v", st, want))
	}
	res.Compared["stats"]++
	out.stats = st

	// Warm up each op once on a few queries so one-time cache effects are not
	// part of the timed distribution.
	ops := queryOps()
	for _, o := range ops {
		for _, q := range p.queries[:min(20, len(p.queries))] {
			_, _, _ = o.run(ctx, b, q)
		}
	}
	if len(p.pairs) > 0 {
		_, _ = b.ShortestPath(ctx, p.pairs[0][0], p.pairs[0][1], pathMaxDepth)
	}

	for _, o := range ops {
		lat := make([]time.Duration, 0, len(p.queries))
		var answers []answer
		for i, q := range p.queries {
			t := time.Now()
			a, capped, err := o.run(ctx, b, q)
			lat = append(lat, time.Since(t))
			if err != nil {
				a.err = err.Error()
			}
			if isRef {
				a.err = ""
				if capped {
					a.path = -2 // marks "capped": candidates skip the comparison
				}
				answers = append(answers, a)
				if err != nil {
					return fail("%s %s: %v", o.name, q, err)
				}
				continue
			}
			w := ref.ops[o.name][i]
			if w.path == -2 {
				res.Skipped[o.name]++ // result at the cap; the reference's subset is arbitrary for other stores
				continue
			}
			chk.compare(o.name, q, a, w)
		}
		out.ops[o.name] = answers
		res.Latency[o.name] = percentiles(lat)
	}

	// shortest path
	lat := make([]time.Duration, 0, len(p.pairs))
	for i, pr := range p.pairs {
		t := time.Now()
		n, err := b.ShortestPath(ctx, pr[0], pr[1], pathMaxDepth)
		lat = append(lat, time.Since(t))
		a := answer{path: n}
		if err != nil {
			a.err = err.Error()
		}
		if isRef {
			if err != nil {
				return fail("path %v: %v", pr, err)
			}
			out.path = append(out.path, a)
			continue
		}
		res.Compared["path"]++
		switch w := ref.path[i]; {
		case a.err != "":
			chk.note("path", fmt.Sprintf("%s -> %s: error: %s", pr[0], pr[1], a.err))
		case a.path != w.path:
			chk.note("path", fmt.Sprintf("%s -> %s: length %d, reference %d (-1 = no path)", pr[0], pr[1], a.path, w.path))
		}
	}
	res.Latency["path"] = percentiles(lat)

	// stats latency
	t := time.Now()
	st2, err := b.Stats(ctx)
	res.Latency["stats"] = OpStats{N: 1, P50us: time.Since(t).Microseconds(), P95us: time.Since(t).Microseconds()}
	if err != nil || st2 != st {
		chk.note("stats", fmt.Sprintf("drifted between load and queries: %+v vs %+v (err %v)", st2, st, err))
	}

	// one-file update
	t = time.Now()
	if err := b.ApplyFileDelta(ctx, p.update); err != nil {
		return fail("apply file delta: %v", err)
	}
	res.UpdateMS = ms(time.Since(t))
	st, err = b.Stats(ctx)
	if err != nil {
		return fail("stats after update: %v", err)
	}
	probes, err := p.updateProbes(ctx, b)
	if err != nil {
		return fail("probes after update: %v", err)
	}
	if isRef {
		out.after, out.probes = st, probes
		exp := Stats{Files: p.fx.NFiles, Symbols: p.fx.NSyms, CallEdges: len(p.fx.Edges) - p.droppedInbound}
		if st != exp {
			chk.note("update", fmt.Sprintf("reference stats after update %+v, expected %+v", st, exp))
		}
	} else {
		res.Compared["update"]++
		if st != ref.after {
			chk.note("update", fmt.Sprintf("stats after update %+v, reference %+v", st, ref.after))
		}
		for i, a := range probes {
			chk.compare("update", fmt.Sprintf("probe %d", i), a, ref.probes[i])
		}
	}

	// remove a different file
	t = time.Now()
	if err := b.RemoveFile(ctx, p.removeFile); err != nil {
		return fail("remove file: %v", err)
	}
	res.RemoveMS = ms(time.Since(t))
	st, err = b.Stats(ctx)
	if err != nil {
		return fail("stats after remove: %v", err)
	}
	probes, err = p.updateProbes(ctx, b)
	if err != nil {
		return fail("probes after remove: %v", err)
	}
	if isRef {
		out.rmStat, out.rmProb = st, probes
	} else {
		res.Compared["remove"]++
		st.Files, ref.rmStat.Files = 0, 0 // whether RemoveFile keeps the file row is backend-defined
		if st != ref.rmStat {
			chk.note("remove", fmt.Sprintf("stats after remove %+v, reference %+v", st, ref.rmStat))
		}
		for i, a := range probes {
			chk.compare("remove", fmt.Sprintf("probe %d", i), a, ref.rmProb[i])
		}
	}

	if peak := peakRSSKB(); peak > 0 {
		res.PeakRSSDeltaKB = max(0, peak-rss0)
	}
	if err := b.Close(); err != nil {
		return fail("close: %v", err)
	}
	if res.DiskBytes, err = b.DiskBytes(); err != nil {
		return fail("disk size: %v", err)
	}
	for k, v := range res.Mismatches {
		if v == 0 {
			delete(res.Mismatches, k)
		}
	}
	return res, out
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func percentiles(ds []time.Duration) OpStats {
	if len(ds) == 0 {
		return OpStats{}
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(q float64) int64 { return s[min(len(s)-1, int(float64(len(s))*q))].Microseconds() }
	return OpStats{N: len(s), P50us: at(0.50), P95us: at(0.95)}
}

// ParseSizes parses a comma-separated list of positive integers.
func ParseSizes(raw string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%q is not a positive integer", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no sizes given")
	}
	return out, nil
}
