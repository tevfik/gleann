package gleann

import (
	"context"
	"os"
	"testing"
)

// Doctor accepts sparse slots, reports dead slots and delta entries, and
// flags a delta it cannot read.
func TestIncrementalHealthReportsDeadSlotsAndDelta(t *testing.T) {
	dir := t.TempDir()
	b, _, base := incrBuilder(t, dir)
	ctx := context.Background()
	if _, err := b.UpdateIndexWithStats(ctx, "idx", []Item{{Text: "date syrup cake", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}, []string{"c.md"}); err != nil {
		t.Fatal(err)
	}
	h, err := CheckIndexHealth(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Problems) != 0 || h.DeadSlots != 1 || h.DeltaEntries != 1 || !h.Dense {
		t.Fatalf("health = %+v, want 1 dead slot, 1 delta entry and no problems", h)
	}
	if err := os.WriteFile(base+suffixDelta, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _ = CheckIndexHealth(dir, "idx")
	if len(h.Problems) == 0 {
		t.Fatal("an unreadable delta must be reported")
	}
}
