package memory

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// searchFixture builds a manager whose blocks are added oldest first, so the
// last block is the newest.
func searchFixture(t testing.TB, blocks ...Block) *Manager {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	base := time.Now().Add(-time.Hour)
	for i := range blocks {
		b := blocks[i]
		if b.Tier == "" {
			b.Tier = TierLong
		}
		b.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		if err := store.Add(&b); err != nil {
			t.Fatal(err)
		}
	}
	return NewManager(store)
}

func contents(bs []Block) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Content)
	}
	return out
}

func TestSearch_Ranked(t *testing.T) {
	tests := []struct {
		name   string
		blocks []Block
		scope  string
		query  string
		want   []string // exact expected order of contents
	}{
		{
			name:   "word order does not matter",
			blocks: []Block{{Content: "Database migrations must use snake_case names"}},
			query:  "snake_case database",
			want:   []string{"Database migrations must use snake_case names"},
		},
		{
			name:   "snake_case part of identifier",
			blocks: []Block{{Content: "columns are named user_id and order_total"}, {Content: "unrelated note"}},
			query:  "order",
			want:   []string{"columns are named user_id and order_total"},
		},
		{
			name:   "camelCase block found by words",
			blocks: []Block{{Content: "RateLimiter caps requests per client"}, {Content: "unrelated note"}},
			query:  "rate limiter",
			want:   []string{"RateLimiter caps requests per client"},
		},
		{
			name:   "words found by camelCase query",
			blocks: []Block{{Content: "the rate limiter caps requests"}, {Content: "unrelated note"}},
			query:  "RateLimiter",
			want:   []string{"the rate limiter caps requests"},
		},
		{
			name:   "symbols and paths are searched",
			blocks: []Block{{Content: "see here", Symbols: []string{"pkg.TokenBucket.Refill"}, Paths: []string{"internal/limits/bucket.go"}}, {Content: "other"}},
			query:  "refill bucket",
			want:   []string{"see here"},
		},
		{
			name:   "more matching terms first",
			blocks: []Block{{Content: "deploy notes"}, {Content: "deploy staging notes only"}, {Content: "deploy staging database notes"}},
			query:  "deploy staging database",
			want:   []string{"deploy staging database notes", "deploy staging notes only", "deploy notes"},
		},
		{
			name:   "phrase match outranks denser non-phrase match",
			blocks: []Block{{Content: "backoff backoff backoff retry retry retry"}, {Content: "retry backoff is exponential"}},
			query:  "retry backoff",
			want:   []string{"retry backoff is exponential", "backoff backoff backoff retry retry retry"},
		},
		{
			name:   "ties broken by recency",
			blocks: []Block{{Content: "alpha one"}, {Content: "alpha two"}},
			query:  "alpha",
			want:   []string{"alpha two", "alpha one"},
		},
		{
			name:   "turkish lowercase match",
			blocks: []Block{{Content: "veritabanı göçleri snake_case kullanmalı"}, {Content: "başka bir not"}},
			query:  "göçleri veritabanı",
			want:   []string{"veritabanı göçleri snake_case kullanmalı"},
		},
		{
			name:   "empty query returns all newest first",
			blocks: []Block{{Content: "one"}, {Content: "two"}},
			query:  "",
			want:   []string{"two", "one"},
		},
		{
			name:   "stop-word-only query falls back to phrase",
			blocks: []Block{{Content: "this is the way"}, {Content: "nothing relevant"}},
			query:  "the",
			want:   []string{"this is the way"},
		},
		{
			name:   "no match",
			blocks: []Block{{Content: "one"}},
			query:  "zebra",
			want:   nil,
		},
		{
			name: "scope isolation",
			blocks: []Block{
				{Content: "alpha private to repo b", Scope: "repo-b"},
				{Content: "alpha in repo a", Scope: "repo-a"},
				{Content: "alpha global"},
			},
			scope: "repo-a",
			query: "alpha",
			want:  []string{"alpha global", "alpha in repo a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := searchFixture(t, tt.blocks...)
			var got []Block
			var err error
			if tt.scope != "" {
				got, err = m.SearchScoped(tt.scope, tt.query)
			} else {
				got, err = m.Search(tt.query)
			}
			if err != nil {
				t.Fatal(err)
			}
			g := contents(got)
			if fmt.Sprint(g) != fmt.Sprint(tt.want) {
				t.Errorf("got %q, want %q", g, tt.want)
			}
		})
	}
}

func TestSearch_PhraseKeepsTopHit(t *testing.T) {
	m := searchFixture(t,
		Block{Content: "Database migrations must use snake_case names"},
		Block{Content: "database database migrations migrations snake_case"},
	)
	got, _ := m.Search("migrations must use snake_case")
	if len(got) == 0 || got[0].Content != "Database migrations must use snake_case names" {
		t.Fatalf("top hit = %q", contents(got))
	}
}

// Forget must stay strict: a loose multi-word query must not delete blocks
// that merely share one word with it.
func TestForget_RemainsPhraseOnly(t *testing.T) {
	m := searchFixture(t,
		Block{Content: "database migrations use snake_case"},
		Block{Content: "database backups run nightly"},
	)
	n, err := m.Forget("database nightly")
	if err == nil || n != 0 {
		t.Fatalf("Forget deleted %d blocks (err=%v); want none for a non-phrase query", n, err)
	}
	if n, err = m.Forget("database migrations"); err != nil || n != 1 {
		t.Fatalf("Forget phrase: n=%d err=%v", n, err)
	}
	left, _ := m.List("")
	if len(left) != 1 || left[0].Content != "database backups run nightly" {
		t.Fatalf("left = %q", contents(left))
	}
}

func benchRank(b *testing.B, n int) {
	topics := []string{"database", "deploy", "cache", "auth", "queue", "logging", "retry", "schema", "index", "worker"}
	blocks := make([]Block, n)
	for i := range blocks {
		blocks[i] = Block{
			Tier:      TierLong,
			Label:     "fact",
			Content:   fmt.Sprintf("%s %s note %d: RateLimiter settings for service_%d use %s defaults and snake_case names", topics[i%10], topics[(i/10)%10], i, i%50, topics[(i/7)%10]),
			Tags:      []string{topics[i%10]},
			Paths:     []string{fmt.Sprintf("internal/svc%d/handler.go", i%50)},
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute),
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rankBlocks(blocks, "database retry snake_case")
	}
}

func BenchmarkRankBlocks_1K(b *testing.B)  { benchRank(b, 1000) }
func BenchmarkRankBlocks_10K(b *testing.B) { benchRank(b, 10000) }
