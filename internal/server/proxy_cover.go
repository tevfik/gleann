package server

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Cover: in a multi-index pass-through answer the best passages overall can all come from one or two indexes
// although another index holds a nearly as good one. With X-Gleann-Cover (or proxy_cover) an index whose best
// passage is close to the overall best keeps that passage in the context, replacing the lowest-ranked extra
// passage of another index, so the model sees every document that is about the question.
//
// "Close" is measured on the raw relevance the searchers report, never on the fused rank score:
//   - vector: the index's best vector distance is at most the overall best distance plus Margin (distance units
//     of the index's metric, smaller is better);
//   - lexical: the index's best BM25 score is at least Share of the overall best BM25 score and at least Floor.
//
// A passage is close when either holds (the vector test only when the search weighs vectors at all, the lexical
// one only when it weighs BM25). The defaults are starting points, not calibrated to any embedding model: BM25
// and distance scales depend on the corpus and the model, so tune them for yours (proxy_cover_margin,
// proxy_cover_share, proxy_cover_floor, or the X-Gleann-Cover-* headers).
const (
	defaultCoverMargin = 0.1
	defaultCoverShare  = 0.6
	defaultCoverFloor  = 1.0
)

// coverConfig enables cover and holds its thresholds.
type coverConfig struct {
	Margin float32 // largest vector distance gap to the overall best that still counts as close
	Share  float32 // smallest fraction of the overall best BM25 score that counts as close
	Floor  float32 // smallest BM25 score that counts as close at all
}

// coverFromRequest returns the cover settings of a request: the configuration (proxy_cover*) with the
// X-Gleann-Cover header on top (true/false), and X-Gleann-Cover-Margin / -Share / -Floor overriding the
// thresholds. It returns nil when cover is off. A header value that does not parse (or a negative number, or a
// share above 1) is ignored, like the other X-Gleann-* search headers.
func coverFromRequest(cfg gleann.Config, r *http.Request) *coverConfig {
	on := cfg.ProxyCover
	if v := strings.TrimSpace(r.Header.Get("X-Gleann-Cover")); v != "" {
		if b, err := strconv.ParseBool(strings.ToLower(v)); err == nil {
			on = b
		} else if strings.EqualFold(v, "yes") || strings.EqualFold(v, "on") {
			on = true
		} else if strings.EqualFold(v, "no") || strings.EqualFold(v, "off") {
			on = false
		}
	}
	if !on {
		return nil
	}
	c := &coverConfig{Margin: defaultCoverMargin, Share: defaultCoverShare, Floor: defaultCoverFloor}
	if cfg.ProxyCoverMargin > 0 {
		c.Margin = cfg.ProxyCoverMargin
	}
	if cfg.ProxyCoverShare > 0 && cfg.ProxyCoverShare <= 1 {
		c.Share = cfg.ProxyCoverShare
	}
	if cfg.ProxyCoverFloor > 0 {
		c.Floor = cfg.ProxyCoverFloor
	}
	num := func(name string) (float32, bool) {
		v := strings.TrimSpace(r.Header.Get(name))
		if v == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(v, 32)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return 0, false
		}
		return float32(f), true
	}
	if f, ok := num("X-Gleann-Cover-Margin"); ok {
		c.Margin = f
	}
	if f, ok := num("X-Gleann-Cover-Share"); ok && f <= 1 {
		c.Share = f
	}
	if f, ok := num("X-Gleann-Cover-Floor"); ok {
		c.Floor = f
	}
	return c
}

// selectProxyHits picks the passages for the context from ranked (every hit of every index, best first): the
// first k (0 = all) up to maxChars characters of text (at least one passage), and, when cover is set, the
// covering passages of the indexes that would have none. A covering passage replaces the lowest-ranked passage
// that is not the best of its own index (so no index loses its only passage, and no covering passage is ever
// replaced), or is added when there is room under k and maxChars; a swap must keep the text within maxChars.
// The chosen passages stay in ranked order.
func selectProxyHits(ranked []proxyHit, k, maxChars int, cover *coverConfig, alpha float32) []proxyHit {
	n := len(ranked)
	if k > 0 && n > k {
		n = k
	}
	used, cut := 0, 0
	for cut < n {
		l := len(ranked[cut].result.Text)
		if maxChars > 0 && cut > 0 && used+l > maxChars {
			break
		}
		used += l
		cut++
	}
	if cover == nil || cut == len(ranked) {
		return ranked[:cut]
	}

	// Each index's best passage is its first one in the ranking.
	bestPos := map[string]int{}
	var order []string // indexes by the rank of their best passage
	bestDist, bestBM := float32(math.Inf(1)), float32(math.Inf(-1))
	for i, h := range ranked {
		if _, ok := bestPos[h.index]; !ok {
			bestPos[h.index] = i
			order = append(order, h.index)
		}
		if d := h.result.VectorDistance; d != nil && *d < bestDist {
			bestDist = *d
		}
		if b := h.result.BM25Score; b != nil && *b > bestBM {
			bestBM = *b
		}
	}
	if len(order) < 2 {
		return ranked[:cut]
	}
	closeEnough := func(h gleann.SearchResult) bool {
		if d := h.VectorDistance; alpha > 0 && d != nil && !math.IsInf(float64(bestDist), 1) && *d <= bestDist+cover.Margin {
			return true
		}
		if b := h.BM25Score; alpha < 1 && b != nil && !math.IsInf(float64(bestBM), -1) &&
			*b >= cover.Floor && *b >= cover.Share*bestBM {
			return true
		}
		return false
	}

	chosen := make([]int, cut)
	for i := range chosen {
		chosen[i] = i
	}
	limit := n // the top-k the answer is allowed to hold
	total := used
	for _, idx := range order {
		pos := bestPos[idx]
		if pos < cut || !closeEnough(ranked[pos].result) {
			continue // already in the context, or not close enough to the best
		}
		l := len(ranked[pos].result.Text)
		if len(chosen) < limit && (maxChars <= 0 || total+l <= maxChars) {
			chosen = append(chosen, pos)
			total += l
			continue
		}
		for v := len(chosen) - 1; v >= 0; v-- {
			victim := ranked[chosen[v]]
			if bestPos[victim.index] == chosen[v] {
				continue // the only passage of its index (or a cover): stays
			}
			vl := len(victim.result.Text)
			if maxChars > 0 && total-vl+l > maxChars {
				continue
			}
			chosen = append(chosen[:v], chosen[v+1:]...)
			chosen = append(chosen, pos)
			total += l - vl
			break
		}
	}
	sort.Ints(chosen)
	out := make([]proxyHit, len(chosen))
	for i, p := range chosen {
		out[i] = ranked[p]
	}
	return out
}
