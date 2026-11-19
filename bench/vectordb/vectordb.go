// Package vectordb is a pluggable benchmark harness for vector databases.
// It measures a candidate store against gleann's vector backends (modules/hnsw,
// modules/diskann) on the same deterministic synthetic dataset: load time,
// cold start latency, ef-sweep search latency and recall@10 at concurrency 1
// and 8, filtered search latency and recall against exact filtered truth,
// file-level passage updates, memory, and disk footprint.
//
// A candidate implements Store in its own sub-package of adapters/ and registers
// itself by name with Register. The harness lives in a nested Go module so
// candidate dependencies (and CGo or Docker clients) never reach gleann's root
// go.mod.
package vectordb

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Store is the unified vector database interface (RFC 006 section 4.2).
type Store interface {
	Name() string
	Caveats() []string // documented differences, listed in the report
	Open(ctx context.Context, dir string, p Params) error
	Insert(ctx context.Context, ids []int64, vecs [][]float32, meta []Meta) error // bulk load
	Upsert(ctx context.Context, ids []int64, vecs [][]float32, meta []Meta) error // incremental
	Delete(ctx context.Context, ids []int64) error
	Flush(ctx context.Context) error // make writes visible and durable
	Search(ctx context.Context, q []float32, k int, f *Filter) ([]int64, error)
	Stats(ctx context.Context) (Stats, error) // count, disk bytes, server RSS if known
	Close() error
}

// EfSearchSetter is an optional interface implemented by stores that can
// update their search beam width (ef/ef_search/nprobe) without reopening.
type EfSearchSetter interface {
	SetEfSearch(ef int)
}

// Params configures index build and search parameters.
type Params struct {
	Dims              int
	Metric            string // "cosine" | "l2" | "ip"
	M, EfConstruction int
	EfSearch          int // swept by the runner, see RFC 006 section 4.4
}

// Meta holds passage-level metadata for filtering and updates.
type Meta struct {
	Source string // file path, for file-level updates and filters
	Kind   string // "code" | "prose"
	Tag    int    // 0..99, for filters of known selectivity
}

// Filter specifies metadata predicates for filtered search.
type Filter struct {
	Source string // exact match on Meta.Source if non-empty
	Kind   string // exact match on Meta.Kind if non-empty
	// TagMax matches Meta.Tag < TagMax if > 0.
	// E.g. TagMax=1 gives 1% selectivity (Tag=0), TagMax=10 gives 10%, TagMax=50 gives 50%.
	TagMax int
}

// Matches returns true if the given Meta satisfies the filter predicates.
func (f *Filter) Matches(m Meta) bool {
	if f == nil {
		return true
	}
	if f.Source != "" && m.Source != f.Source {
		return false
	}
	if f.Kind != "" && m.Kind != f.Kind {
		return false
	}
	if f.TagMax > 0 && m.Tag >= f.TagMax {
		return false
	}
	return true
}

// Stats holds store-reported metrics.
type Stats struct {
	Count     int64
	DiskBytes int64
	ServerRSS int64 // bytes; 0 if embedded or in-process
}

// Factory constructs a fresh, unopened Store.
type Factory func() Store

var (
	regMu    sync.Mutex
	registry = map[string]Factory{}
)

// Register makes a store available under name. Adapters call it from init.
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("vectordb: store registered twice: " + name)
	}
	registry[name] = f
}

// New builds the store registered under name.
func New(name string) (Store, error) {
	regMu.Lock()
	f, ok := registry[name]
	regMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown store %q (available: %v)", name, Names())
	}
	return f(), nil
}

// Names returns sorted names of all registered stores.
func Names() []string {
	regMu.Lock()
	defer regMu.Unlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
