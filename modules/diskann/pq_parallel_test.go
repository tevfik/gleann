package diskann

import (
	"math"
	"math/rand"
	"runtime"
	"testing"
)

// TestTrainPQParallelMatchesSequential: the parallel training must give the
// codebook of a sequential run, sub-quantizer after sub-quantizer on one
// random generator (how TrainPQ worked before), whatever GOMAXPROCS is.
func TestTrainPQParallelMatchesSequential(t *testing.T) {
	const n, dims, m, k = 1500, 32, 8, 64
	rng := rand.New(rand.NewSource(5))
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, dims)
		for j := range v {
			v[j] = float32(rng.NormFloat64())
		}
		vecs[i] = v
	}

	// Sequential reference: init and Lloyd of one sub-quantizer, then the next.
	subDim := dims / m
	ref := make([][][]float32, m)
	refRng := rand.New(rand.NewSource(42))
	for sq := 0; sq < m; sq++ {
		sub := make([][]float32, n)
		for i, v := range vecs {
			sub[i] = v[sq*subDim : (sq+1)*subDim]
		}
		ref[sq] = kmeansLloyd(sub, kmeansPlusPlusInit(sub, k, subDim, refRng), subDim)
	}

	for _, procs := range []int{1, 4} {
		prev := runtime.GOMAXPROCS(procs)
		pq, err := TrainPQ(vecs, m, k)
		runtime.GOMAXPROCS(prev)
		if err != nil {
			t.Fatalf("TrainPQ (GOMAXPROCS %d): %v", procs, err)
		}
		for sq := range ref {
			for c := range ref[sq] {
				for d := range ref[sq][c] {
					if math.Float32bits(pq.Centroids[sq][c][d]) != math.Float32bits(ref[sq][c][d]) {
						t.Fatalf("GOMAXPROCS %d: centroid [%d][%d][%d] = %v, sequential run gives %v",
							procs, sq, c, d, pq.Centroids[sq][c][d], ref[sq][c][d])
					}
				}
			}
		}
	}
}
