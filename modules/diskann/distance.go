package diskann

import "math"

// L2DistanceSquared computes the squared Euclidean distance.
// Uses 8-way loop unrolling to maximize CPU instruction-level parallelism (ILP) and FMA throughput.
func L2DistanceSquared(a, b []float32) float32 {
	n := len(a)
	var s0, s1, s2, s3, s4, s5, s6, s7 float32
	i := 0
	for ; i+7 < n; i += 8 {
		d0 := a[i] - b[i]
		d1 := a[i+1] - b[i+1]
		d2 := a[i+2] - b[i+2]
		d3 := a[i+3] - b[i+3]
		d4 := a[i+4] - b[i+4]
		d5 := a[i+5] - b[i+5]
		d6 := a[i+6] - b[i+6]
		d7 := a[i+7] - b[i+7]

		s0 += d0 * d0
		s1 += d1 * d1
		s2 += d2 * d2
		s3 += d3 * d3
		s4 += d4 * d4
		s5 += d5 * d5
		s6 += d6 * d6
		s7 += d7 * d7
	}
	sum := (s0 + s1) + (s2 + s3) + (s4 + s5) + (s6 + s7)
	for ; i < n; i++ {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// CosineDistance computes 1 - cosine_similarity.
// Uses 4-way loop unrolling for inner dot product and norms.
func CosineDistance(a, b []float32) float32 {
	n := len(a)
	var d0, d1, d2, d3 float32
	var na0, na1, na2, na3 float32
	var nb0, nb1, nb2, nb3 float32
	i := 0
	for ; i+3 < n; i += 4 {
		ai0, bi0 := a[i], b[i]
		ai1, bi1 := a[i+1], b[i+1]
		ai2, bi2 := a[i+2], b[i+2]
		ai3, bi3 := a[i+3], b[i+3]

		d0 += ai0 * bi0
		d1 += ai1 * bi1
		d2 += ai2 * bi2
		d3 += ai3 * bi3

		na0 += ai0 * ai0
		na1 += ai1 * ai1
		na2 += ai2 * ai2
		na3 += ai3 * ai3

		nb0 += bi0 * bi0
		nb1 += bi1 * bi1
		nb2 += bi2 * bi2
		nb3 += bi3 * bi3
	}
	dot := (d0 + d1) + (d2 + d3)
	normA := (na0 + na1) + (na2 + na3)
	normB := (nb0 + nb1) + (nb2 + nb3)
	for ; i < n; i++ {
		ai, bi := a[i], b[i]
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 1.0
	}
	return 1.0 - dot/float32(math.Sqrt(float64(normA)*float64(normB)))
}

// GetDistanceFunc returns a DistanceFunc for the given metric name.
func GetDistanceFunc(metric string) DistanceFunc {
	switch DistanceMetric(metric) {
	case DistanceCosine:
		return CosineDistance
	default:
		return L2DistanceSquared
	}
}
