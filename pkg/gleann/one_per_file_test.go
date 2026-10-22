package gleann

import "testing"

func TestOnePerFile(t *testing.T) {
	in := []SearchResult{
		{ID: 1, Metadata: map[string]any{"file": "a.go"}},
		{ID: 2, Metadata: map[string]any{"file": "a.go"}},
		{ID: 3, Metadata: map[string]any{"source": "b.go"}},
		{ID: 4, Metadata: map[string]any{"source": "b.go"}},
		{ID: 5, Metadata: nil},
		{ID: 6, Metadata: nil},
	}
	out := onePerFile(in)
	want := []int64{1, 3, 5, 6}
	if len(out) != len(want) {
		t.Fatalf("got %d results, want %d", len(out), len(want))
	}
	for i, r := range out {
		if r.ID != want[i] {
			t.Errorf("out[%d].ID = %d, want %d", i, r.ID, want[i])
		}
	}
}
