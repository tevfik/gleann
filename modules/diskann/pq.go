package diskann

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// PQCodebook implements Product Quantization for approximate distance computation.
// It splits D-dimensional vectors into M sub-vectors, each quantized independently
// using k-means with K centroids. A vector is encoded as M bytes (when K≤256).
//
// Memory: M*K*subDim float32 centroids (~256KB for M=32, K=256, subDim=4).
// Encoded vector: M bytes (vs D*4 bytes raw).
type PQCodebook struct {
	// M is the number of sub-quantizers.
	M int
	// K is the number of centroids per sub-quantizer.
	K int
	// SubDim is the dimensionality of each sub-vector.
	SubDim int
	// Dims is the original vector dimensionality (M * SubDim).
	Dims int

	// Centroids[m][k] is the k-th centroid for the m-th sub-quantizer.
	// Shape: [M][K][SubDim]
	Centroids [][][]float32
}

// TrainPQ trains a PQ codebook from training vectors using k-means.
func TrainPQ(vectors [][]float32, m, k int) (*PQCodebook, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("no training vectors")
	}
	dims := len(vectors[0])
	if dims%m != 0 {
		return nil, fmt.Errorf("dims (%d) must be divisible by m (%d)", dims, m)
	}
	if k > 256 {
		return nil, fmt.Errorf("k (%d) must be ≤ 256 for uint8 codes", k)
	}
	if k > len(vectors) {
		k = len(vectors)
	}

	subDim := dims / m
	pq := &PQCodebook{
		M:         m,
		K:         k,
		SubDim:    subDim,
		Dims:      dims,
		Centroids: make([][][]float32, m),
	}

	rng := rand.New(rand.NewSource(42))

	// Train each sub-quantizer independently.
	for sq := 0; sq < m; sq++ {
		// Extract sub-vectors.
		subVecs := make([][]float32, len(vectors))
		offset := sq * subDim
		for i, vec := range vectors {
			subVecs[i] = vec[offset : offset+subDim]
		}

		// K-means clustering.
		centroids := kmeansSubVectors(subVecs, k, subDim, rng)
		pq.Centroids[sq] = centroids
	}

	return pq, nil
}

// Encode encodes a vector into M PQ codes (one byte per sub-quantizer).
func (pq *PQCodebook) Encode(vec []float32) []byte {
	codes := make([]byte, pq.M)
	for sq := 0; sq < pq.M; sq++ {
		offset := sq * pq.SubDim
		sub := vec[offset : offset+pq.SubDim]
		codes[sq] = pq.nearestCentroid(sq, sub)
	}
	return codes
}

// EncodeAll encodes multiple vectors.
func (pq *PQCodebook) EncodeAll(vectors [][]float32) [][]byte {
	codes := make([][]byte, len(vectors))
	for i, vec := range vectors {
		codes[i] = pq.Encode(vec)
	}
	return codes
}

// BuildDistanceTable precomputes distances from a query to all centroids.
// Returns table[m][k] = distance from query sub-vector m to centroid k.
// This enables Asymmetric Distance Computation (ADC): O(M) per candidate.
func (pq *PQCodebook) BuildDistanceTable(query []float32, distFunc DistanceFunc) [][]float32 {
	table := make([][]float32, pq.M)
	for sq := 0; sq < pq.M; sq++ {
		offset := sq * pq.SubDim
		qSub := query[offset : offset+pq.SubDim]
		table[sq] = make([]float32, pq.K)
		for k := 0; k < pq.K; k++ {
			table[sq][k] = distFunc(qSub, pq.Centroids[sq][k])
		}
	}
	return table
}

// ADCDistance computes the approximate distance from a query to a PQ-encoded
// vector using a precomputed distance table. O(M) operations.
func ADCDistance(table [][]float32, codes []byte) float32 {
	var dist float32
	for sq, code := range codes {
		dist += table[sq][code]
	}
	return dist
}

// nearestCentroid finds the closest centroid for a sub-vector.
func (pq *PQCodebook) nearestCentroid(sq int, sub []float32) byte {
	bestK := 0
	bestDist := float32(math.MaxFloat32)
	for k := 0; k < pq.K; k++ {
		d := L2DistanceSquared(sub, pq.Centroids[sq][k])
		if d < bestDist {
			bestDist = d
			bestK = k
		}
	}
	return byte(bestK)
}

// kmeansSubVectors runs k-means on sub-vectors and returns K centroids.
func kmeansSubVectors(subVecs [][]float32, k, subDim int, rng *rand.Rand) [][]float32 {
	n := len(subVecs)

	// Initialize centroids with k-means++ for better convergence.
	centroids := kmeansPlusPlusInit(subVecs, k, subDim, rng)

	assignments := make([]int, n)
	maxIter := 25

	for iter := 0; iter < maxIter; iter++ {
		changed := 0

		// Assign each vector to nearest centroid.
		for i, sub := range subVecs {
			bestK := 0
			bestDist := float32(math.MaxFloat32)
			for j := 0; j < k; j++ {
				d := L2DistanceSquared(sub, centroids[j])
				if d < bestDist {
					bestDist = d
					bestK = j
				}
			}
			if assignments[i] != bestK {
				changed++
				assignments[i] = bestK
			}
		}

		// Converged if <0.1% changed.
		if changed < n/1000+1 && iter > 3 {
			break
		}

		// Recompute centroids.
		counts := make([]int, k)
		newCentroids := make([][]float32, k)
		for j := 0; j < k; j++ {
			newCentroids[j] = make([]float32, subDim)
		}
		for i, sub := range subVecs {
			c := assignments[i]
			counts[c]++
			for d := 0; d < subDim; d++ {
				newCentroids[c][d] += sub[d]
			}
		}
		for j := 0; j < k; j++ {
			if counts[j] > 0 {
				for d := 0; d < subDim; d++ {
					newCentroids[j][d] /= float32(counts[j])
				}
				centroids[j] = newCentroids[j]
			}
			// Empty cluster: keep old centroid.
		}
	}

	return centroids
}

// kmeansPlusPlusInit implements k-means++ initialization.
func kmeansPlusPlusInit(subVecs [][]float32, k, subDim int, rng *rand.Rand) [][]float32 {
	n := len(subVecs)
	centroids := make([][]float32, 0, k)

	// Pick first centroid randomly.
	first := make([]float32, subDim)
	copy(first, subVecs[rng.Intn(n)])
	centroids = append(centroids, first)

	// Distance from each point to nearest centroid.
	minDist := make([]float32, n)
	for i := range minDist {
		minDist[i] = math.MaxFloat32
	}

	for len(centroids) < k {
		// Update min distances to include the last-added centroid.
		last := centroids[len(centroids)-1]
		var totalDist float64
		for i, sub := range subVecs {
			d := L2DistanceSquared(sub, last)
			if d < minDist[i] {
				minDist[i] = d
			}
			totalDist += float64(minDist[i])
		}

		if totalDist == 0 {
			// All remaining points are identical; duplicate centroids.
			c := make([]float32, subDim)
			copy(c, subVecs[rng.Intn(n)])
			centroids = append(centroids, c)
			continue
		}

		// Sample next centroid proportional to squared distance.
		threshold := rng.Float64() * totalDist
		var cumDist float64
		chosen := n - 1
		for i := 0; i < n; i++ {
			cumDist += float64(minDist[i])
			if cumDist >= threshold {
				chosen = i
				break
			}
		}

		c := make([]float32, subDim)
		copy(c, subVecs[chosen])
		centroids = append(centroids, c)
	}

	return centroids
}

// ADCDistanceSorted computes ADC distances for multiple candidates and returns
// them sorted. Used for PQ-based candidate prefiltering.
func ADCDistanceSorted(table [][]float32, candidates []int64, codes [][]byte) []Candidate {
	result := make([]Candidate, len(candidates))
	for i, id := range candidates {
		result[i] = Candidate{ID: id, Distance: ADCDistance(table, codes[id])}
	}
	sort.Slice(result, func(a, b int) bool {
		return result[a].Distance < result[b].Distance
	})
	return result
}
