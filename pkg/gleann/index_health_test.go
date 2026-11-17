package gleann

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCheckIndexHealth_FreshIndexIsHealthy(t *testing.T) {
	_, dir, _ := buildFour(t)
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if h.NeedsRepair() || !h.Dense || h.Passages != 4 || h.MaxID != 3 {
		t.Errorf("fresh index reported unhealthy: %+v", h)
	}
}

// Removing passages directly, without the builder, leaves dead vector slots
// and a stale passage count in meta.json.
func gapIndex(t *testing.T) (*LeannBuilder, string, string) {
	t.Helper()
	builder, dir, base := buildFour(t)
	pm := testPassageStore(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.RemoveBySource([]string{"a.md"}); err != nil {
		t.Fatal(err)
	}
	_ = pm.Close()
	return builder, dir, base
}

func TestCheckIndexHealth_FindsGapsAndStaleCount(t *testing.T) {
	_, dir, _ := gapIndex(t)
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	// Dead slots below the high-water mark are a normal state (RFC 005).
	if !h.NeedsRepair() || !h.Repairable || !h.Dense || h.DeadSlots != 2 {
		t.Fatalf("index with a stale count not flagged as repairable: %+v", h)
	}
	all := strings.Join(h.Problems, "\n")
	for _, want := range []string{"metadata records 4 passages but the store holds 2"} {
		if !strings.Contains(all, want) {
			t.Errorf("problems %q lack %q", all, want)
		}
	}
}

func TestCheckIndexHealth_MissingVectorFile(t *testing.T) {
	_, dir, base := buildFour(t)
	if err := os.Remove(base + ".index"); err != nil {
		t.Fatal(err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if !h.NeedsRepair() || !h.Repairable || !strings.Contains(strings.Join(h.Problems, "\n"), "vector index file is missing") {
		t.Errorf("missing vector file not flagged: %+v", h)
	}
}

func TestCheckIndexHealth_MissingPassageStoreIsNotRepairable(t *testing.T) {
	_, dir, base := buildFour(t)
	if err := os.Remove(base + ".db"); err != nil {
		t.Fatal(err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if !h.NeedsRepair() || h.Repairable {
		t.Errorf("a missing passage store cannot be repaired from itself: %+v", h)
	}
}

func TestCheckIndexHealth_EmptiedIndexIsHealthy(t *testing.T) {
	builder, dir, _ := buildFour(t)
	if err := builder.UpdateIndex(context.Background(), "idx", nil, []string{"a.md", "b.md", "c.md"}); err != nil {
		t.Fatal(err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if h.NeedsRepair() {
		t.Errorf("an emptied index is a valid state: %+v", h)
	}
}

func TestCompact_RepairsGapsAndMissingVectorFile(t *testing.T) {
	builder, dir, base := gapIndex(t)
	if err := os.Remove(base + ".index"); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Compact(context.Background(), "idx"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if h.NeedsRepair() || h.Passages != 2 || !h.Dense {
		t.Errorf("index still unhealthy after Compact: %+v", h)
	}
	texts, _ := passageTexts(t, base)
	if len(texts) != 2 || texts[0] != "gamma from file-b" || texts[1] != "delta from file-c" {
		t.Errorf("Compact must keep the passages as they are: %v", texts)
	}
}

func TestCompact_RefusesAnEmptyStore(t *testing.T) {
	builder, _, _ := buildFour(t)
	if err := builder.UpdateIndex(context.Background(), "idx", nil, []string{"a.md", "b.md", "c.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Compact(context.Background(), "idx"); err == nil {
		t.Error("Compact of an index without passages must fail")
	}
}

// idSpan is what the searcher uses on every load, so it must agree with the
// full scan while reading only the key count and the last key.
func TestIDSpan(t *testing.T) {
	_, _, base := buildFour(t)
	pm := testPassageStore(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	n, max, err := pm.idSpan()
	_ = pm.Close()
	if err != nil || n != 4 || max != 3 {
		t.Errorf("idSpan on a fresh index = (%d, %d, %v), want (4, 3, nil)", n, max, err)
	}

	_, _, gapBase := gapIndex(t)
	gpm := testPassageStore(gapBase)
	if err := gpm.Load(); err != nil {
		t.Fatal(err)
	}
	defer gpm.Close()
	n, max, err = gpm.idSpan()
	if err != nil || n != 2 || max != 3 {
		t.Errorf("idSpan on a gapped index = (%d, %d, %v), want (2, 3, nil)", n, max, err)
	}
	// The dead slots 0 and 1 are below the high-water mark 4.
	if dense, _ := gpm.IsDense(); !dense {
		t.Error("dead slots below the high-water mark are dense")
	}
}
