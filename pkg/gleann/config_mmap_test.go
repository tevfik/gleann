package gleann

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_UnifiedMmapDefault(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.UseMmap != nil {
		t.Errorf("expected top-level UseMmap unset by default, got %v", *cfg.UseMmap)
	}
	if !cfg.HNSWConfig.UseMmap {
		t.Error("expected HNSWConfig.UseMmap to be true by default")
	}
	if !cfg.DiskANNConfig.UseMmap {
		t.Error("expected DiskANNConfig.UseMmap to be true by default")
	}
	if !cfg.MmapEnabled() {
		t.Error("expected MmapEnabled() to be true by default")
	}

	// DiskANN backend
	cfgDisk := DefaultConfig()
	cfgDisk.Backend = "diskann"
	if !cfgDisk.MmapEnabled() {
		t.Error("expected MmapEnabled() to be true for diskann by default")
	}
}

func TestConfig_MmapDisabled(t *testing.T) {
	// Top-level disable
	f := false
	cfg := DefaultConfig()
	cfg.UseMmap = &f
	if cfg.MmapEnabled() {
		t.Error("expected MmapEnabled() to be false when UseMmap is false")
	}

	// Legacy HNSWConfig.UseMmap disable
	cfgHNSW := DefaultConfig()
	cfgHNSW.Backend = "hnsw"
	cfgHNSW.HNSWConfig.UseMmap = false
	if cfgHNSW.MmapEnabled() {
		t.Error("expected MmapEnabled() to be false when HNSWConfig.UseMmap is false")
	}

	// Legacy DiskANNConfig.UseMmap disable
	cfgDisk := DefaultConfig()
	cfgDisk.Backend = "diskann"
	cfgDisk.DiskANNConfig.UseMmap = false
	if cfgDisk.MmapEnabled() {
		t.Error("expected MmapEnabled() to be false when DiskANNConfig.UseMmap is false")
	}
}

func TestConfig_MmapJSONAliases(t *testing.T) {
	// 1. Top-level use_mmap
	raw1 := `{"use_mmap": false}`
	var cfg1 Config
	if err := json.Unmarshal([]byte(raw1), &cfg1); err != nil {
		t.Fatalf("unmarshal raw1: %v", err)
	}
	if cfg1.MmapEnabled() {
		t.Error("expected MmapEnabled() false for raw1")
	}

	// 2. Deprecated hnsw_config.use_mmap
	raw2 := `{"hnsw_config": {"use_mmap": false}}`
	var cfg2 Config
	if err := json.Unmarshal([]byte(raw2), &cfg2); err != nil {
		t.Fatalf("unmarshal raw2: %v", err)
	}
	if cfg2.MmapEnabled() {
		t.Error("expected MmapEnabled() false for raw2")
	}

	// 3. Deprecated diskann_config.use_mmap
	raw3 := `{"backend": "diskann", "diskann_config": {"use_mmap": false}}`
	var cfg3 Config
	if err := json.Unmarshal([]byte(raw3), &cfg3); err != nil {
		t.Fatalf("unmarshal raw3: %v", err)
	}
	if cfg3.MmapEnabled() {
		t.Error("expected MmapEnabled() false for raw3")
	}
}

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		input string
		want  int64
		err   bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"1024", 1024, false},
		{"1KB", 1024, false},
		{"1K", 1024, false},
		{"1KiB", 1024, false},
		{"10MB", 10 * 1024 * 1024, false},
		{"10M", 10 * 1024 * 1024, false},
		{"10MiB", 10 * 1024 * 1024, false},
		{"2GB", 2 * 1024 * 1024 * 1024, false},
		{"2G", 2 * 1024 * 1024 * 1024, false},
		{"2GiB", 2 * 1024 * 1024 * 1024, false},
		{"  500 MB  ", 500 * 1024 * 1024, false},
		{"invalid", 0, true},
		{"-5MB", 0, true},
	}

	for _, tc := range cases {
		got, err := ParseByteSize(tc.input)
		if tc.err && err == nil {
			t.Errorf("ParseByteSize(%q) expected error, got nil", tc.input)
		} else if !tc.err && err != nil {
			t.Errorf("ParseByteSize(%q) unexpected error: %v", tc.input, err)
		} else if !tc.err && got != tc.want {
			t.Errorf("ParseByteSize(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestLeannSearcher_EstimatedSize(t *testing.T) {
	// 1. Nil searcher
	var nilS *LeannSearcher
	if nilS.EstimatedSize() != 0 {
		t.Errorf("expected 0 for nil searcher, got %d", nilS.EstimatedSize())
	}

	// 2. Explicitly set size (e.g. for testing)
	s := NewSearcher(DefaultConfig(), nil)
	s.SetEstimatedSize(12345678)
	if s.EstimatedSize() != 12345678 {
		t.Errorf("expected 12345678, got %d", s.EstimatedSize())
	}

	// 3. Computed from disk files
	tmpDir := t.TempDir()
	indexName := "testidx"
	indexDir := filepath.Join(tmpDir, indexName)
	if err := os.MkdirAll(indexDir, 0755); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(indexDir, indexName)
	if err := os.WriteFile(basePath+".index", make([]byte, 1000), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath+".db", make([]byte, 2000), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.IndexDir = tmpDir
	diskS := NewSearcher(cfg, nil)
	diskS.name = indexName
	if diskS.EstimatedSize() != 3000 {
		t.Errorf("expected estimated size 3000, got %d", diskS.EstimatedSize())
	}
}
