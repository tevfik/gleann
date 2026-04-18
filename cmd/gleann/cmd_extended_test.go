package main

import (
	"testing"
	"time"
)

func TestFormatSizeExtended(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatSize(tt.bytes)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestIsCodeExtensionExtended(t *testing.T) {
	codeExts := []string{".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".rs", ".rb", ".php"}
	for _, ext := range codeExts {
		if !isCodeExtension(ext) {
			t.Errorf("isCodeExtension(%q) = false, want true", ext)
		}
	}

	nonCodeExts := []string{".pdf", ".png", ".jpg", ".zip", ".tar", ".exe", ".bin"}
	for _, ext := range nonCodeExts {
		if isCodeExtension(ext) {
			t.Errorf("isCodeExtension(%q) = true, want false", ext)
		}
	}
}

func TestTruncateExtended(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"long", "hello world", 8, "hello..."},
		{"newlines", "line1\nline2", 20, "line1 line2"},
		{"empty", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

func TestFormatAgeExtended(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 3 * time.Hour, "3h"},
		{"days", 48 * time.Hour, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.d)
			if got != tt.want {
				t.Errorf("formatAge(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestParseFriendlyDurationExtended(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"days", "7d", 7 * 24 * time.Hour, false},
		{"weeks", "2w", 14 * 24 * time.Hour, false},
		{"one day", "1d", 24 * time.Hour, false},
		{"invalid", "abc", 0, true},
		{"no unit", "123", 0, true},
		{"with whitespace", "  3d  ", 3 * 24 * time.Hour, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFriendlyDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseFriendlyDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetDeleteArgsExtended(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no delete", []string{"--list"}, 0},
		{"single delete", []string{"--delete", "abc123"}, 1},
		{"multiple delete", []string{"--delete", "abc", "--delete", "def"}, 2},
		{"delete at end", []string{"--delete"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDeleteArgs(tt.args)
			if len(got) != tt.want {
				t.Errorf("len = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestGetFlagExtended(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
		want string
	}{
		{"found", []string{"--model", "bge-m3"}, "--model", "bge-m3"},
		{"not found", []string{"--model", "bge-m3"}, "--provider", ""},
		{"at end", []string{"--model"}, "--model", ""},
		{"empty args", nil, "--model", ""},
		{"multiple flags", []string{"--model", "a", "--host", "b"}, "--host", "b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getFlag(tt.args, tt.flag)
			if got != tt.want {
				t.Errorf("getFlag(%v, %q) = %q, want %q", tt.args, tt.flag, got, tt.want)
			}
		})
	}
}

func TestHasFlagExtended(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
		want bool
	}{
		{"found", []string{"--list", "--json"}, "--json", true},
		{"not found", []string{"--list"}, "--json", false},
		{"empty args", nil, "--json", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasFlag(tt.args, tt.flag)
			if got != tt.want {
				t.Errorf("hasFlag(%v, %q) = %v, want %v", tt.args, tt.flag, got, tt.want)
			}
		})
	}
}

func TestGetConfig(t *testing.T) {
	cfg := getConfig([]string{
		"--model", "nomic-embed-text",
		"--provider", "ollama",
		"--top-k", "20",
		"--host", "http://localhost:11434",
		"--no-mmap",
	})

	if cfg.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("EmbeddingModel = %q", cfg.EmbeddingModel)
	}
	if cfg.EmbeddingProvider != "ollama" {
		t.Errorf("EmbeddingProvider = %q", cfg.EmbeddingProvider)
	}
	if cfg.SearchConfig.TopK != 20 {
		t.Errorf("TopK = %d", cfg.SearchConfig.TopK)
	}
	if cfg.OllamaHost != "http://localhost:11434" {
		t.Errorf("OllamaHost = %q", cfg.OllamaHost)
	}
	if cfg.HNSWConfig.UseMmap {
		t.Error("UseMmap should be false with --no-mmap")
	}
}

func TestGetConfigDefaults(t *testing.T) {
	cfg := getConfig(nil)
	if cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("default EmbeddingModel = %q, want bge-m3", cfg.EmbeddingModel)
	}
	if cfg.SearchConfig.TopK != 10 {
		t.Errorf("default TopK = %d, want 10", cfg.SearchConfig.TopK)
	}
}

func TestGetConfigAdvanced(t *testing.T) {
	cfg := getConfig([]string{
		"--ef-search", "256",
		"--chunk-size", "1024",
		"--chunk-overlap", "100",
		"--batch-size", "32",
		"--concurrency", "4",
		"--hybrid", "0.5",
		"--metric", "cosine",
		"--prune", "0.1",
	})

	if cfg.HNSWConfig.EfSearch != 256 {
		t.Errorf("EfSearch = %d", cfg.HNSWConfig.EfSearch)
	}
	if cfg.ChunkConfig.ChunkSize != 1024 {
		t.Errorf("ChunkSize = %d", cfg.ChunkConfig.ChunkSize)
	}
	if cfg.ChunkConfig.ChunkOverlap != 100 {
		t.Errorf("ChunkOverlap = %d", cfg.ChunkConfig.ChunkOverlap)
	}
	if cfg.BatchSize != 32 {
		t.Errorf("BatchSize = %d", cfg.BatchSize)
	}
	if cfg.Concurrency != 4 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if cfg.HNSWConfig.DistanceMetric != "cosine" {
		t.Errorf("DistanceMetric = %q", cfg.HNSWConfig.DistanceMetric)
	}
}

func TestTaskSortOrderExtended(t *testing.T) {
	tests := []struct {
		status string
		want   int
	}{
		{"running", 0},
		{"queued", 1},
		{"failed", 2},
		{"completed", 3},
		{"unknown", 4},
		{"", 4},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := taskSortOrder(tt.status)
			if got != tt.want {
				t.Errorf("taskSortOrder(%q) = %d, want %d", tt.status, got, tt.want)
			}
		})
	}
}

func TestColorStatusExtended(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"running", "🔄 running"},
		{"queued", "⏳ queued"},
		{"completed", "✅ completed"},
		{"failed", "❌ failed"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := colorStatus(tt.status)
			if got != tt.want {
				t.Errorf("colorStatus(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestTaskAgeExtended(t *testing.T) {
	t.Run("zero time", func(t *testing.T) {
		got := taskAge(time.Time{})
		if got != "-" {
			t.Errorf("taskAge(zero) = %q, want '-'", got)
		}
	})

	t.Run("recent", func(t *testing.T) {
		got := taskAge(time.Now().Add(-10 * time.Second))
		if got != "just now" {
			t.Errorf("taskAge(10s ago) = %q, want 'just now'", got)
		}
	})

	t.Run("minutes", func(t *testing.T) {
		got := taskAge(time.Now().Add(-5 * time.Minute))
		if got != "5m ago" {
			t.Errorf("taskAge(5m ago) = %q, want '5m ago'", got)
		}
	})

	t.Run("hours", func(t *testing.T) {
		got := taskAge(time.Now().Add(-3 * time.Hour))
		if got != "3h ago" {
			t.Errorf("taskAge(3h ago) = %q, want '3h ago'", got)
		}
	})

	t.Run("days", func(t *testing.T) {
		got := taskAge(time.Now().Add(-48 * time.Hour))
		if got != "2d ago" {
			t.Errorf("taskAge(2d ago) = %q, want '2d ago'", got)
		}
	})
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := truncateStr(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

func TestFormatMemAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{48 * time.Hour, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatMemAge(tt.d)
			if got != tt.want {
				t.Errorf("formatMemAge(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatMemSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatMemSize(tt.bytes)
			if got != tt.want {
				t.Errorf("formatMemSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestStrValExtended(t *testing.T) {
	m := map[string]any{
		"name":  "test",
		"count": 42,
	}

	if got := strVal(m, "name"); got != "test" {
		t.Errorf("strVal(name) = %q, want 'test'", got)
	}
	if got := strVal(m, "count"); got != "" {
		t.Errorf("strVal(count) = %q, want '' (not a string)", got)
	}
	if got := strVal(m, "missing"); got != "" {
		t.Errorf("strVal(missing) = %q, want ''", got)
	}
	if got := strVal(nil, "key"); got != "" {
		t.Errorf("strVal(nil, key) = %q, want ''", got)
	}
}
