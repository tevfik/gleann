package gleann

import (
	"context"
	"path/filepath"
	"testing"
)

// RFC 005 section 3.1: vector_slots is the high-water mark of the slot space.
// A build records it, a dense renumbering restarts it, and an index built
// before the key existed derives it from its highest slot.
func TestFormat2_VectorSlotsKey(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	if got := f2Meta(t, base, metaVectorSlots); got != "4" {
		t.Fatalf("%s after the build = %q, want 4", metaVectorSlots, got)
	}
	if got := f2Meta(t, base, metaVectorMainGeneration); got != "1" {
		t.Fatalf("%s after the build = %q, want 1", metaVectorMainGeneration, got)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil || m.VectorSlots != 4 || m.VectorMainGeneration != 1 {
		t.Fatalf("meta.json = %+v (%v), want VectorSlots 4 and VectorMainGeneration 1", m, err)
	}

	// The key is absent in an index built before RFC 005: derive it.
	st, err := openFormat2Store(base)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetMeta(ctx, metaVectorSlots, ""); err != nil {
		t.Fatal(err)
	}
	got, err := currentVectorSlots(ctx, st)
	if err != nil || got != 4 {
		t.Fatalf("derived vector_slots = %d (%v), want 4", got, err)
	}
	if g := currentVectorMainGeneration(ctx, st); g != 1 {
		t.Errorf("main generation = %d, want 1", g)
	}
}

// An update that rebuilds the vector file densely keeps both keys and the
// meta.json mirror in step with the published state.
func TestFormat2_VectorSlotsAfterUpdate(t *testing.T) {
	t.Setenv("GLEANN_DEAD_MIN", "0") // a four-passage index compacts at the share alone
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	m, err := GetIndexMeta(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if got := f2Meta(t, base, metaVectorSlots); got != "4" || m.VectorSlots != 4 {
		t.Errorf("vector_slots = %q, meta.json %d; want 4", got, m.VectorSlots)
	}
	if got := f2Meta(t, base, metaVectorMainGeneration); got != "2" || m.VectorMainGeneration != 2 {
		t.Errorf("vector_main_generation = %q, meta.json %d; want 2", got, m.VectorMainGeneration)
	}
}
