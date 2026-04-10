package backends

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHNSWFactory_Name(t *testing.T) {
	f := &hnswFactoryAdapter{}
	if got := f.Name(); got != "hnsw" {
		t.Errorf("Name() = %q, want %q", got, "hnsw")
	}
}

func TestHNSWFactory_NewBuilder(t *testing.T) {
	f := &hnswFactoryAdapter{}
	cfg := gleann.Config{
		IndexDir: t.TempDir(),
		Backend:  "hnsw",
	}
	b := f.NewBuilder(cfg)
	if b == nil {
		t.Fatal("NewBuilder returned nil")
	}
}

func TestHNSWFactory_NewSearcher(t *testing.T) {
	f := &hnswFactoryAdapter{}
	cfg := gleann.Config{
		IndexDir: t.TempDir(),
		Backend:  "hnsw",
	}
	s := f.NewSearcher(cfg)
	if s == nil {
		t.Fatal("NewSearcher returned nil")
	}
}

func TestHNSWFactory_NewSearcher_Mmap(t *testing.T) {
	f := &hnswFactoryAdapter{}
	cfg := gleann.Config{
		IndexDir: t.TempDir(),
		Backend:  "hnsw",
		HNSWConfig: gleann.HNSWConfig{
			UseMmap: true,
		},
	}
	s := f.NewSearcher(cfg)
	if s == nil {
		t.Fatal("NewSearcher (mmap) returned nil")
	}
	// Verify it implements LoadFromFile.
	if _, ok := s.(*mmapSearcherAdapter); !ok {
		t.Errorf("expected *mmapSearcherAdapter when UseMmap=true, got %T", s)
	}
}

func TestHNSWFactory_NewSearcher_NoMmap(t *testing.T) {
	f := &hnswFactoryAdapter{}
	cfg := gleann.Config{
		IndexDir: t.TempDir(),
		Backend:  "hnsw",
		HNSWConfig: gleann.HNSWConfig{
			UseMmap: false,
		},
	}
	s := f.NewSearcher(cfg)
	if _, ok := s.(*mmapSearcherAdapter); ok {
		t.Error("expected plain searcherAdapter when UseMmap=false, got mmapSearcherAdapter")
	}
}

func TestToHNSWConfig(t *testing.T) {
	cfg := gleann.Config{
		IndexDir: "/tmp/test",
		Backend:  "hnsw",
		HNSWConfig: gleann.HNSWConfig{
			M:              32,
			EfConstruction: 200,
			EfSearch:       128,
			UseMmap:        true,
			DistanceMetric: "cosine",
		},
	}
	hcfg := toHNSWConfig(cfg)

	if hcfg.IndexDir != "/tmp/test" {
		t.Errorf("IndexDir = %q, want /tmp/test", hcfg.IndexDir)
	}
	if hcfg.HNSWConfig.M != 32 {
		t.Errorf("M = %d, want 32", hcfg.HNSWConfig.M)
	}
	if hcfg.HNSWConfig.EfConstruction != 200 {
		t.Errorf("EfConstruction = %d, want 200", hcfg.HNSWConfig.EfConstruction)
	}
	if hcfg.HNSWConfig.EfSearch != 128 {
		t.Errorf("EfSearch = %d, want 128", hcfg.HNSWConfig.EfSearch)
	}
	if !hcfg.HNSWConfig.UseMmap {
		t.Error("UseMmap should be true")
	}
}
