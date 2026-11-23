package benchmark

import (
	"math"
	"strings"
	"testing"
)

// TestHybridAlphaFromLabelRejectsNonFinite: strconv.ParseFloat accepts "NaN",
// "+Inf" and "-Inf". Treating those as weights poisons the report's sort
// comparator, so they must be rejected like any other unparsable label.
func TestHybridAlphaFromLabelRejectsNonFinite(t *testing.T) {
	for _, name := range []string{
		"Hybrid (α=NaN)",
		"Hybrid (α=+Inf)",
		"Hybrid (α=-Inf)",
		"Hybrid (default, α=NaN)",
		"Hybrid (default, α=+Inf)",
	} {
		v, ok := hybridAlphaFromLabel(name)
		if ok {
			t.Errorf("hybridAlphaFromLabel(%q) = (%v, true), want rejection of a non-finite weight", name, v)
			continue
		}
		if v != 0 {
			t.Errorf("hybridAlphaFromLabel(%q) = (%v, false), want 0", name, v)
		}
	}
}

// TestHybridAlphaFromLabelParsesFiniteWeights guards the valid cases.
func TestHybridAlphaFromLabelParsesFiniteWeights(t *testing.T) {
	cases := map[string]float64{
		"Hybrid (α=0.5)":           0.5,
		"Hybrid (default, α=0.7)":  0.7,
		"Hybrid (default, α=0.25)": 0.25,
	}
	for name, want := range cases {
		v, ok := hybridAlphaFromLabel(name)
		if !ok || math.Abs(v-want) > 1e-9 {
			t.Errorf("hybridAlphaFromLabel(%q) = (%v, %v), want (%v, true)", name, v, ok, want)
		}
	}
}

// TestStrategyRankRejectsNonFiniteWeight: an α=NaN row must sort in the
// "other hybrid" bucket with the unweighted fallback (-1), never with a NaN
// weight that breaks sort.Slice's strict weak ordering.
func TestStrategyRankRejectsNonFiniteWeight(t *testing.T) {
	for _, name := range []string{"Hybrid (α=NaN)", "Hybrid (α=+Inf)", "Hybrid (α=-Inf)"} {
		bucket, alpha := strategyRank(name)
		if bucket != 7 {
			t.Errorf("strategyRank(%q) bucket = %d, want 7", name, bucket)
		}
		if math.IsNaN(alpha) || math.IsInf(alpha, 0) {
			t.Errorf("strategyRank(%q) weight = %v, want the finite unweighted fallback -1", name, alpha)
		}
		if alpha != -1 {
			t.Errorf("strategyRank(%q) weight = %v, want -1", name, alpha)
		}
	}
}

// TestRenderMarkdownReportNonFiniteWeightsDeterministic: the report must order
// rows with a non-finite α deterministically instead of poisoning the sort.
func TestRenderMarkdownReportNonFiniteWeightsDeterministic(t *testing.T) {
	names := []string{
		"Hybrid (α=NaN)", "Hybrid (α=+Inf)", "Hybrid (α=0.5)",
		"Hybrid (default, α=0.7)", "BM25", "Ripgrep",
	}
	sums := make(map[string]SummaryResult, len(names))
	for _, n := range names {
		sums[n] = SummaryResult{Strategy: n, AvgRecall5: 50, AvgRecall10: 60, AvgMRR: 0.5}
	}

	first := RenderMarkdownReport(sums, 10)
	if !strings.Contains(first, "Hybrid (α=NaN)") || !strings.Contains(first, "Hybrid (α=+Inf)") {
		t.Fatalf("report lacks the non-finite rows:\n%s", first)
	}
	for i := 0; i < 20; i++ {
		if got := RenderMarkdownReport(sums, 10); got != first {
			t.Fatalf("report order is not deterministic with non-finite weights (iteration %d):\nfirst:\n%s\ngot:\n%s", i, first, got)
		}
	}
	if strings.Index(first, "Hybrid (α=0.5)") < strings.Index(first, "Hybrid (α=NaN)") {
		t.Errorf("an unweighted hybrid row must sort before a finite weight:\n%s", first)
	}
}
