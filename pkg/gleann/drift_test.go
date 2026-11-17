//go:build drift

package gleann

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// G6 of RFC 005: no recall drift. A 100K-passage index goes through 50 rounds
// of incremental syncs (1 % of the sources replaced per round, 10 % of the
// sources deleted over the run, the replacements drifting to new topics) and
// is then compared against a fresh build of the same final corpus: recall@10
// of the vector leg against exact search must be within 0.01 of the fresh
// build's.
//
//	go test -tags drift -run TestVectorRecallDoesNotDrift -timeout 90m ./pkg/gleann
//
// GLEANN_DRIFT_PASSAGES and GLEANN_DRIFT_ROUNDS shrink it for a quick check.

const (
	driftDims    = 32
	driftPerSrc  = 10
	driftBaseTag = 100
)

// driftEmbedder maps "t<tag> ..." texts to the centre of the tag plus noise
// that depends on the text: texts of one tag form a cluster, a new tag a new
// cluster (the drift).
type driftEmbedder struct{}

func (driftEmbedder) ModelName() string { return "drift-test" }
func (driftEmbedder) Dimensions() int   { return driftDims }
func (e driftEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	return driftVector(text), nil
}
func (e driftEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = driftVector(t)
	}
	return out, nil
}

func driftVector(text string) []float32 {
	var tag int
	fmt.Sscanf(text, "t%d", &tag)
	cr := rand.New(rand.NewSource(int64(tag)*7919 + 1))
	h := fnv.New64a()
	h.Write([]byte(text))
	nr := rand.New(rand.NewSource(int64(h.Sum64())))
	v := make([]float32, driftDims)
	for i := range v {
		v[i] = float32(cr.NormFloat64()*2 + nr.NormFloat64()*0.5)
	}
	return v
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

type driftPassage struct {
	text string
	vec  []float32
}

func l2(a, b []float32) float32 {
	var s float32
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

func exactTopK(corpus []driftPassage, q []float32, k int) map[string]bool {
	type hit struct {
		text string
		d    float32
	}
	hits := make([]hit, len(corpus))
	for i, p := range corpus {
		hits[i] = hit{p.text, l2(q, p.vec)}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].d < hits[j].d })
	out := make(map[string]bool, k)
	for _, h := range hits[:k] {
		out[h.text] = true
	}
	return out
}

func driftRecall(t *testing.T, dir string, corpus []driftPassage, queries []string) float64 {
	t.Helper()
	ctx := context.Background()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, driftEmbedder{})
	if err := s.Load(ctx, "drift"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()
	var sum float64
	for _, q := range queries {
		qv := driftVector(q)
		want := exactTopK(corpus, qv, 10)
		rs, err := s.Search(ctx, q, WithTopK(10), WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		found := 0
		for _, r := range rs {
			if want[r.Text] {
				found++
			}
		}
		sum += float64(found) / 10
	}
	return sum / float64(len(queries))
}

func TestVectorRecallDoesNotDrift(t *testing.T) {
	initTestHNSW()
	n := envInt("GLEANN_DRIFT_PASSAGES", 100000)
	rounds := envInt("GLEANN_DRIFT_ROUNDS", 50)
	nsrc := n / driftPerSrc
	changePerRound := max(1, nsrc/100)
	deleteTotal := nsrc / 10
	deletePerRound := max(1, deleteTotal/rounds)

	rng := rand.New(rand.NewSource(20260101))
	ctx := context.Background()

	srcItems := map[string][]Item{}
	mkItems := func(src string, tag, rev int) []Item {
		items := make([]Item, driftPerSrc)
		for i := range items {
			items[i] = Item{
				Text:     fmt.Sprintf("t%d %s rev%d part%d", tag, src, rev, i),
				Metadata: map[string]any{"source": src, "chunk_index": i},
			}
		}
		return items
	}
	var names []string
	var initial []Item
	for i := 0; i < nsrc; i++ {
		src := fmt.Sprintf("src%06d.md", i)
		names = append(names, src)
		srcItems[src] = mkItems(src, driftBaseTag+i%driftBaseTag, 0)
		initial = append(initial, srcItems[src]...)
	}

	dir := t.TempDir()
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, driftEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Build(ctx, "drift", initial); err != nil {
		t.Fatalf("Build: %v", err)
	}

	alive := append([]string(nil), names...)
	for r := 1; r <= rounds; r++ {
		rng.Shuffle(len(alive), func(i, j int) { alive[i], alive[j] = alive[j], alive[i] })
		// the first deletePerRound sources are deleted, the next changePerRound
		// replaced with content of a topic that did not exist before
		var removed []string
		for _, src := range alive[:deletePerRound] {
			removed = append(removed, src)
			delete(srcItems, src)
		}
		alive = alive[deletePerRound:]
		var fresh []Item
		for _, src := range alive[:changePerRound] {
			srcItems[src] = mkItems(src, driftBaseTag*2+r, r)
			fresh = append(fresh, srcItems[src]...)
		}
		st, err := b.UpdateIndexWithStats(ctx, "drift", fresh, removed)
		if err != nil {
			t.Fatalf("round %d: %v", r, err)
		}
		if r%10 == 0 || r == rounds {
			t.Logf("round %d: mode=%s embedded=%d passages=%d", r, st.Mode, st.Embedded, st.Passages)
		}
	}

	var final []Item
	var corpus []driftPassage
	keys := make([]string, 0, len(srcItems))
	for k := range srcItems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, it := range srcItems[k] {
			final = append(final, it)
			corpus = append(corpus, driftPassage{it.Text, driftVector(it.Text)})
		}
	}

	// queries on old and on drifted topics
	var queries []string
	for i := 0; i < 300; i++ {
		tag := driftBaseTag + rng.Intn(driftBaseTag)
		if i%2 == 1 {
			tag = driftBaseTag*2 + 1 + rng.Intn(rounds)
		}
		queries = append(queries, fmt.Sprintf("t%d query%d", tag, i))
	}

	incremental := driftRecall(t, dir, corpus, queries)

	freshDir := filepath.Join(t.TempDir(), "fresh")
	fb, err := NewBuilder(Config{IndexDir: freshDir, Backend: "hnsw"}, driftEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fb.Build(ctx, "drift", final); err != nil {
		t.Fatalf("fresh Build: %v", err)
	}
	fresh := driftRecall(t, freshDir, corpus, queries)

	t.Logf("recall@10 after %d incremental rounds: %.4f, fresh build of the same corpus: %.4f (%d passages)", rounds, incremental, fresh, len(final))
	if math.IsNaN(incremental) || incremental < fresh-0.01 {
		t.Fatalf("recall drifted: incremental %.4f vs fresh %.4f (allowed gap 0.01)", incremental, fresh)
	}
}
