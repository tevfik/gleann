package backends

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestDiskANNFactory_NewSearcher_Mmap(t *testing.T) {
	factory, err := gleann.GetBackend("diskann")
	if err != nil {
		t.Fatalf("GetBackend(diskann): %v", err)
	}

	// 1. By default, mmap is enabled
	cfg := gleann.DefaultConfig()
	cfg.Backend = "diskann"

	s := factory.NewSearcher(cfg)
	if s == nil {
		t.Fatal("NewSearcher returned nil")
	}
	defer s.Close()

	if _, ok := s.(gleann.MmapBackendSearcher); !ok {
		t.Fatalf("expected searcher to implement gleann.MmapBackendSearcher when mmap is enabled, got %T", s)
	}

	// 2. Disabled via top-level UseMmap = false
	f := false
	cfgNoMmap := gleann.DefaultConfig()
	cfgNoMmap.Backend = "diskann"
	cfgNoMmap.UseMmap = &f

	sNoMmap := factory.NewSearcher(cfgNoMmap)
	if sNoMmap == nil {
		t.Fatal("NewSearcher returned nil")
	}
	defer sNoMmap.Close()

	if _, ok := sNoMmap.(gleann.MmapBackendSearcher); ok {
		t.Errorf("expected searcher NOT to implement gleann.MmapBackendSearcher when UseMmap=false, got %T", sNoMmap)
	}

	// 3. Disabled via legacy DiskANNConfig.UseMmap = false
	cfgLegacy := gleann.DefaultConfig()
	cfgLegacy.Backend = "diskann"
	cfgLegacy.DiskANNConfig.UseMmap = false

	sLegacy := factory.NewSearcher(cfgLegacy)
	if sLegacy == nil {
		t.Fatal("NewSearcher returned nil")
	}
	defer sLegacy.Close()

	if _, ok := sLegacy.(gleann.MmapBackendSearcher); ok {
		t.Errorf("expected searcher NOT to implement gleann.MmapBackendSearcher when DiskANNConfig.UseMmap=false, got %T", sLegacy)
	}
}
