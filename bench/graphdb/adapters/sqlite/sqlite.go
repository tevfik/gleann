// Package sqlite is the reference adapter: gleann's SQLite code graph
// (internal/graphstore), exactly as the product uses it. Importing the package
// registers the backend as "sqlite".
package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tevfik/gleann/bench/graphdb"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

func init() {
	graphdb.Register("sqlite", func() graphdb.Backend { return &backend{} })
}

type backend struct {
	dir string
	st  *store.Store
	gs  *graphstore.SQLite
}

func (b *backend) Name() string { return "sqlite" }

func (b *backend) Open(dir string) error {
	b.dir = dir
	st, err := store.Open(filepath.Join(dir, "idx", "idx.db"), store.Options{})
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	b.st, b.gs = st, graphstore.New(st)
	return nil
}

func toDelta(d graphdb.FileDelta) graphstore.FileDelta {
	out := graphstore.FileDelta{File: d.File}
	for _, s := range d.Symbols {
		file := s.File
		if file == "" {
			file = d.File
		}
		out.Symbols = append(out.Symbols, graphstore.Symbol{
			FQN: s.FQN, Kind: s.Kind, File: file, Name: s.Name, Weight: 1.0,
			StartLine: s.Line, EndLine: s.Line + 5, StartCol: 1, ByteOffset: s.Line * 40,
		})
	}
	for _, e := range d.Edges {
		out.Edges = append(out.Edges, graphstore.Edge{Src: e.Src, Dst: e.Dst, Kind: "CALLS", Confidence: "extracted"})
	}
	return out
}

func (b *backend) BulkLoad(ctx context.Context, deltas []graphdb.FileDelta) error {
	ds := make([]graphstore.FileDelta, len(deltas))
	for i, d := range deltas {
		ds[i] = toDelta(d)
	}
	return b.gs.ReplaceAll(ctx, ds)
}

func (b *backend) ApplyFileDelta(ctx context.Context, d graphdb.FileDelta) error {
	return b.gs.ApplyFileDelta(ctx, toDelta(d))
}

func (b *backend) RemoveFile(ctx context.Context, file string) error {
	return b.gs.RemoveFile(ctx, file)
}

func hopsFQNs(h []graphstore.Hop) []string {
	out := make([]string, 0, len(h))
	for _, x := range h {
		out = append(out, x.FQN)
	}
	sort.Strings(out)
	return out
}

func (b *backend) Callers(_ context.Context, fqn string, depth int) ([]string, error) {
	h, err := b.gs.Callers(fqn, depth)
	return hopsFQNs(h), err
}

func (b *backend) Callees(_ context.Context, fqn string, depth int) ([]string, error) {
	h, err := b.gs.Callees(fqn, depth)
	return hopsFQNs(h), err
}

func (b *backend) Impact(_ context.Context, fqn string, depth int) (graphdb.ImpactResult, error) {
	r, err := b.gs.Impact(fqn, depth)
	if err != nil {
		return graphdb.ImpactResult{}, err
	}
	var out graphdb.ImpactResult
	for _, l := range [][]string{r.DirectCallers, r.TransitiveCallers, r.TestCallers} {
		for _, s := range l {
			if s != fqn {
				out.Callers = append(out.Callers, s)
			}
		}
	}
	out.Files = append(out.Files, r.AffectedFiles...)
	sort.Strings(out.Callers)
	sort.Strings(out.Files)
	return out, nil
}

func (b *backend) SymbolSearch(_ context.Context, pattern string, limit int) ([]string, error) {
	res, err := b.gs.Symbols(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res))
	for _, s := range res {
		out = append(out, s.FQN)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ShortestPath ignores maxDepth: graphstore.Path has its own fixed cap (10).
func (b *backend) ShortestPath(_ context.Context, from, to string, _ int) (int, error) {
	steps, err := b.gs.Path(from, to)
	if err != nil {
		if strings.Contains(err.Error(), "no path within") {
			return graphdb.NoPath, nil // graphstore reports "no path" as an error
		}
		return 0, err
	}
	return len(steps), nil
}

func (b *backend) Stats(context.Context) (graphdb.Stats, error) {
	s, err := b.gs.Stats()
	return graphdb.Stats{Files: s.Files, Symbols: s.Symbols, CallEdges: s.CallEdges}, err
}

func (b *backend) DiskBytes() (int64, error) { return graphdb.DirSize(b.dir) }

func (b *backend) Close() error {
	if b.st == nil {
		return nil
	}
	err := b.st.Close()
	b.st = nil
	return err
}
