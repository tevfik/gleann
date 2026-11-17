package gleann

import (
	"testing"
)

func TestIncrementalLimitsFromEnv(t *testing.T) {
	for _, env := range []string{"GLEANN_DELTA_MIN", "GLEANN_DELTA_SHARE", "GLEANN_DEAD_SHARE", "GLEANN_DEAD_SHARE_PRUNED", "GLEANN_DEAD_MIN"} {
		t.Setenv(env, "")
	}
	if got := incrementalLimitsFromEnv(); got != defaultIncrementalLimits() {
		t.Fatalf("no overrides = %+v, want the defaults", got)
	}

	t.Setenv("GLEANN_DELTA_MIN", "10")
	t.Setenv("GLEANN_DELTA_SHARE", "0.5")
	t.Setenv("GLEANN_DEAD_SHARE", "0.25")
	got := incrementalLimitsFromEnv()
	if got.deltaMin != 10 || got.deltaShare != 0.5 || got.deadShare != 0.25 {
		t.Fatalf("overrides = %+v", got)
	}
	// An explicit share without GLEANN_DEAD_MIN is meant literally.
	if got.deadMin != 0 {
		t.Fatalf("deadMin = %d with an explicit share, want 0", got.deadMin)
	}
	t.Setenv("GLEANN_DEAD_MIN", "7")
	if got := incrementalLimitsFromEnv(); got.deadMin != 7 {
		t.Fatalf("deadMin = %d, want 7", got.deadMin)
	}

	// Values that do not parse, or are negative, are ignored.
	t.Setenv("GLEANN_DELTA_MIN", "-3")
	t.Setenv("GLEANN_DELTA_SHARE", "lots")
	t.Setenv("GLEANN_DEAD_SHARE", "")
	t.Setenv("GLEANN_DEAD_SHARE_PRUNED", "-1")
	t.Setenv("GLEANN_DEAD_MIN", "x")
	if got := incrementalLimitsFromEnv(); got != defaultIncrementalLimits() {
		t.Fatalf("bad values = %+v, want the defaults", got)
	}
}

func TestIncrementalLimitsCrossed(t *testing.T) {
	l := incrementalLimits{deltaMin: 10, deltaShare: 0.1, deadMin: 5, deadShare: 0.2, deadSharePruned: 0.05}
	cases := []struct {
		name          string
		delta         int
		live, dead    int64
		pruned        bool
		merge, compac bool
	}{
		{"small delta", 10, 50, 0, false, false, false},
		{"delta above the minimum", 11, 50, 0, false, true, false},
		{"share allows a bigger delta", 150, 2000, 0, false, false, false},
		{"dead below the minimum count", 0, 10, 5, false, false, false},
		{"dead share crossed", 0, 30, 10, false, false, true},
		{"dead share not crossed", 0, 100, 10, false, false, false},
		{"pruned index compacts sooner", 0, 100, 10, true, false, true},
		{"empty index", 0, 0, 0, false, false, false},
	}
	for _, tc := range cases {
		merge, compact := l.crossed(tc.delta, tc.live, tc.dead, tc.pruned)
		if merge != tc.merge || compact != tc.compac {
			t.Errorf("%s: crossed = %v, %v; want %v, %v", tc.name, merge, compact, tc.merge, tc.compac)
		}
	}
}

func TestVectorMetricAndEmptyDelta(t *testing.T) {
	c := Config{Backend: "diskann"}
	c.DiskANNConfig.DistanceMetric = "cosine"
	if c.vectorMetric() != "cosine" || c.prunedIndex() {
		t.Fatalf("diskann metric = %q, pruned %v", c.vectorMetric(), c.prunedIndex())
	}
	h := Config{Backend: "hnsw"}
	h.HNSWConfig.DistanceMetric = "ip"
	h.HNSWConfig.PruneEmbeddings = true
	if h.vectorMetric() != "ip" || !h.prunedIndex() {
		t.Fatalf("hnsw metric = %q, pruned %v", h.vectorMetric(), h.prunedIndex())
	}

	// The empty delta staged with a new main file matches its dtype and metric.
	h.HNSWConfig.VectorDType = deltaDTypeF16
	b := &LeannBuilder{config: h}
	f := b.emptyDeltaFile(4)
	d, err := parseVectorDelta(f.Data)
	if err != nil || f.Suffix != suffixDelta {
		t.Fatalf("empty delta: %v (%s)", err, f.Suffix)
	}
	if d.Len() != 0 || d.dims != 4 || d.dtype != deltaDTypeF16 || d.metric != "ip" {
		t.Fatalf("empty delta = dims %d dtype %s metric %s len %d", d.dims, d.dtype, d.metric, d.Len())
	}
	b.config = c
	if d, _ := parseVectorDelta(b.emptyDeltaFile(3).Data); d.dtype != deltaDTypeF32 || d.metric != "cosine" {
		t.Fatalf("diskann empty delta = %s %s", d.dtype, d.metric)
	}
}
