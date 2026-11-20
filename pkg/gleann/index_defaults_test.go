package gleann

import (
	"sync"
	"testing"
)

// A zero Config used to build an HNSW graph without the neighbour heuristic
// (UseHeuristic is a bool) and to search with ef equal to top-k: a much
// poorer index than DefaultConfig. NewBuilder and NewSearcher now fill the
// index settings a caller left at zero, and keep the ones it chose.
func TestWithIndexDefaults(t *testing.T) {
	def := DefaultConfig()

	got := withIndexDefaults(Config{IndexDir: "x"})
	if got.HNSWConfig != def.HNSWConfig || got.DiskANNConfig != def.DiskANNConfig || got.Backend != def.Backend {
		t.Errorf("zero config: hnsw %+v diskann %+v backend %q", got.HNSWConfig, got.DiskANNConfig, got.Backend)
	}
	if got.IndexDir != "x" || got.SearchConfig.TopK != 0 || got.SearchConfig.HybridAlpha != 0 {
		t.Errorf("other settings must be kept: %+v", got)
	}

	// A partly set HNSWConfig keeps an explicit heuristic off; zero sizes are filled.
	part := withIndexDefaults(Config{Backend: "hnsw", HNSWConfig: HNSWConfig{M: 16, UseHeuristic: false, PruneEmbeddings: true}})
	h := part.HNSWConfig
	if h.M != 16 || h.UseHeuristic || !h.PruneEmbeddings || h.EfConstruction != def.HNSWConfig.EfConstruction || h.EfSearch != def.HNSWConfig.EfSearch {
		t.Errorf("partial hnsw config = %+v", h)
	}

	probe := registerDefaultsProbe()
	if _, err := NewBuilder(Config{IndexDir: t.TempDir(), Backend: probe.Name()}, nil); err != nil {
		t.Fatal(err)
	}
	if h := probe.got.HNSWConfig; !h.UseHeuristic || h.M != 32 || h.EfConstruction != 200 {
		t.Errorf("the backend got hnsw config %+v from NewBuilder(zero hnsw config)", h)
	}
	if s := NewSearcher(Config{IndexDir: t.TempDir()}, nil); s.config.HNSWConfig.EfSearch != 128 {
		t.Errorf("NewSearcher(zero config) ef = %d", s.config.HNSWConfig.EfSearch)
	}
}

// defaultsProbe is a backend that records the config NewBuilder hands it.
type defaultsProbe struct{ got Config }

func (p *defaultsProbe) Name() string { return "defaults-probe" }
func (p *defaultsProbe) NewBuilder(c Config) BackendBuilder {
	p.got = c
	return nil
}
func (p *defaultsProbe) NewSearcher(Config) BackendSearcher { return nil }

var (
	defaultsProbeOnce sync.Once
	theDefaultsProbe  = &defaultsProbe{}
)

func registerDefaultsProbe() *defaultsProbe {
	defaultsProbeOnce.Do(func() { RegisterBackend(theDefaultsProbe) })
	return theDefaultsProbe
}
