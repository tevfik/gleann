package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func f32(v float32) *float32 { return &v }

// ch builds a ranked hit: index, passage id (also its text, padded to size), raw vector distance and BM25 score.
func ch(index string, id int64, size int, dist, bm *float32) proxyHit {
	return proxyHit{index: index, result: gleann.SearchResult{
		ID: id, Text: strings.Repeat("x", size), VectorDistance: dist, BM25Score: bm,
	}}
}

func idsOf(hits []proxyHit) []int64 {
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.result.ID
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectProxyHits(t *testing.T) {
	cov := &coverConfig{Margin: 0.1, Share: 0.6, Floor: 1}
	cases := []struct {
		name     string
		ranked   []proxyHit
		k        int
		maxChars int
		cover    *coverConfig
		alpha    float32
		want     []int64
	}{
		{
			name: "vector: a close index replaces the lowest-ranked extra passage",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.11), nil), ch("a", 3, 10, f32(0.12), nil),
				ch("b", 4, 10, f32(0.15), nil),
			},
			k: 3, cover: cov, alpha: 0.7, want: []int64{1, 2, 4},
		},
		{
			name: "no cover keeps the plain top-k",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.11), nil), ch("a", 3, 10, f32(0.12), nil),
				ch("b", 4, 10, f32(0.15), nil),
			},
			k: 3, cover: nil, alpha: 0.7, want: []int64{1, 2, 3},
		},
		{
			name: "vector: an index beyond the margin is not covered",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.11), nil), ch("a", 3, 10, f32(0.12), nil),
				ch("b", 4, 10, f32(0.45), nil),
			},
			k: 3, cover: cov, alpha: 0.7, want: []int64{1, 2, 3},
		},
		{
			name: "lexical: a share of the best score above the floor is close",
			ranked: []proxyHit{
				ch("a", 1, 10, nil, f32(10)), ch("a", 2, 10, nil, f32(9)), ch("a", 3, 10, nil, f32(8)),
				ch("b", 4, 10, nil, f32(6.5)),
			},
			k: 3, cover: cov, alpha: 0, want: []int64{1, 2, 4},
		},
		{
			name: "lexical: below the share is not close",
			ranked: []proxyHit{
				ch("a", 1, 10, nil, f32(10)), ch("a", 2, 10, nil, f32(9)), ch("a", 3, 10, nil, f32(8)),
				ch("b", 4, 10, nil, f32(5)),
			},
			k: 3, cover: cov, alpha: 0, want: []int64{1, 2, 3},
		},
		{
			name: "lexical: below the floor is not close even at a high share",
			ranked: []proxyHit{
				ch("a", 1, 10, nil, f32(0.9)), ch("a", 2, 10, nil, f32(0.8)), ch("a", 3, 10, nil, f32(0.7)),
				ch("b", 4, 10, nil, f32(0.8)),
			},
			k: 3, cover: cov, alpha: 0, want: []int64{1, 2, 3},
		},
		{
			name: "alpha 1 ignores the lexical test",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), f32(10)), ch("a", 2, 10, f32(0.11), f32(9)), ch("a", 3, 10, f32(0.12), f32(8)),
				ch("b", 4, 10, f32(0.60), f32(9.5)),
			},
			k: 3, cover: cov, alpha: 1, want: []int64{1, 2, 3},
		},
		{
			name: "alpha 0 ignores the vector test",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), f32(10)), ch("a", 2, 10, f32(0.11), f32(9)), ch("a", 3, 10, f32(0.12), f32(8)),
				ch("b", 4, 10, f32(0.11), f32(0.2)),
			},
			k: 3, cover: cov, alpha: 0, want: []int64{1, 2, 3},
		},
		{
			name: "an index's only passage is never removed",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("b", 2, 10, f32(0.11), nil), ch("c", 3, 10, f32(0.12), nil),
			},
			k: 2, cover: cov, alpha: 0.7, want: []int64{1, 2},
		},
		{
			name: "several close indexes take the lowest extras one after another",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.10), nil), ch("a", 3, 10, f32(0.10), nil), ch("a", 4, 10, f32(0.10), nil),
				ch("b", 5, 10, f32(0.12), nil), ch("c", 6, 10, f32(0.13), nil),
			},
			k: 4, cover: cov, alpha: 0.7, want: []int64{1, 2, 5, 6},
		},
		{
			name: "a swap that would exceed max chars is skipped",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.11), nil), ch("a", 3, 10, f32(0.12), nil),
				ch("b", 4, 100, f32(0.12), nil),
			},
			k: 3, maxChars: 40, cover: cov, alpha: 0.7, want: []int64{1, 2, 3},
		},
		{
			name: "a swap within max chars happens",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.11), nil), ch("a", 3, 10, f32(0.12), nil),
				ch("b", 4, 15, f32(0.12), nil),
			},
			k: 3, maxChars: 40, cover: cov, alpha: 0.7, want: []int64{1, 2, 4},
		},
		{
			name: "room under k and max chars: added without a swap",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 30, f32(0.11), nil),
				ch("b", 3, 8, f32(0.12), nil),
			},
			k: 3, maxChars: 25, cover: cov, alpha: 0.7, want: []int64{1, 3},
		},
		{
			name: "one index: nothing to cover",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("a", 2, 10, f32(0.11), nil),
			},
			k: 1, cover: cov, alpha: 0.7, want: []int64{1},
		},
		{
			name: "the index already in the context is left alone",
			ranked: []proxyHit{
				ch("a", 1, 10, f32(0.10), nil), ch("b", 2, 10, f32(0.11), nil), ch("a", 3, 10, f32(0.12), nil),
			},
			k: 2, cover: cov, alpha: 0.7, want: []int64{1, 2},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := idsOf(selectProxyHits(c.ranked, c.k, c.maxChars, c.cover, c.alpha))
			if !sameIDs(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestSelectProxyHitsMaxCharsWithoutCoverKeepsOne(t *testing.T) {
	ranked := []proxyHit{ch("a", 1, 100, nil, nil), ch("a", 2, 10, nil, nil)}
	if got := idsOf(selectProxyHits(ranked, 0, 50, nil, 0.7)); !sameIDs(got, []int64{1}) {
		t.Errorf("got %v, want the first passage only", got)
	}
}

func TestCoverFromRequest(t *testing.T) {
	req := func(h map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	var cfg gleann.Config
	if c := coverFromRequest(cfg, req(nil)); c != nil {
		t.Errorf("off by default, got %+v", c)
	}
	c := coverFromRequest(cfg, req(map[string]string{"X-Gleann-Cover": "true"}))
	if c == nil || c.Margin != defaultCoverMargin || c.Share != defaultCoverShare || c.Floor != defaultCoverFloor {
		t.Fatalf("defaults: %+v", c)
	}
	// Configured thresholds, then header overrides; invalid header values are ignored.
	cfg.ProxyCover, cfg.ProxyCoverMargin, cfg.ProxyCoverShare, cfg.ProxyCoverFloor = true, 0.2, 0.5, 3
	c = coverFromRequest(cfg, req(nil))
	if c == nil || c.Margin != 0.2 || c.Share != 0.5 || c.Floor != 3 {
		t.Fatalf("config: %+v", c)
	}
	c = coverFromRequest(cfg, req(map[string]string{
		"X-Gleann-Cover-Margin": "0.3", "X-Gleann-Cover-Share": "banana", "X-Gleann-Cover-Floor": "-1"}))
	if c == nil || c.Margin != 0.3 || c.Share != 0.5 || c.Floor != 3 {
		t.Fatalf("header overrides: %+v", c)
	}
	if c := coverFromRequest(cfg, req(map[string]string{"X-Gleann-Cover": "false"})); c != nil {
		t.Errorf("X-Gleann-Cover: false must turn it off, got %+v", c)
	}
	if c := coverFromRequest(cfg, req(map[string]string{"X-Gleann-Cover": "maybe"})); c == nil {
		t.Error("an unparsable X-Gleann-Cover is ignored (the config stays on)")
	}
}
