//go:build eif

package embedding

import "testing"

func TestThreadsFor(t *testing.T) {
	cases := []struct {
		ncpu int
		load float64
		want int
	}{
		{20, 0.3, 20},
		{20, 10.2, 10},
		{20, 19.8, 4},
		{20, -1, 20},
		{2, 0, 2},
		{2, 5, 2},
		{0, 0, 1},
	}
	for _, c := range cases {
		if got := threadsFor(c.ncpu, c.load); got != c.want {
			t.Errorf("threadsFor(%d, %v) = %d, want %d", c.ncpu, c.load, got, c.want)
		}
	}
}
