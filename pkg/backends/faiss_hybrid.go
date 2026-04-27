//go:build cgo && faiss

package backends

import (
	"github.com/tevfik/gleann/internal/backend/faiss"
	"github.com/tevfik/gleann/modules/hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

func init() {
	gleann.RegisterBackend(&hybridFactory{})
}

// hybridFactory registers the "faiss-hybrid" backend which uses FAISS
// for fast index construction and the pure-Go HNSW backend for search
// (supporting CSR pruning, mmap, and SearchWithRecompute).
type hybridFactory struct{}

func (f *hybridFactory) Name() string { return "faiss-hybrid" }

func (f *hybridFactory) NewBuilder(config gleann.Config) gleann.BackendBuilder {
	return faiss.NewHybridBuilder(config)
}

func (f *hybridFactory) NewSearcher(config gleann.Config) gleann.BackendSearcher {
	// The hybrid builder outputs CSR format — identical to the pure-Go HNSW
	// backend. Reuse its searcher for search + SearchWithRecompute + mmap.
	inner := (&hnsw.Factory{}).NewSearcher(toHNSWConfig(config))
	if config.HNSWConfig.UseMmap {
		return &mmapSearcherAdapter{searcherAdapter{inner: inner}}
	}
	return &searcherAdapter{inner: inner}
}
