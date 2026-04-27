package diskann

import "math"

// L2DistanceSquared computes the squared Euclidean distance.
func L2DistanceSquared(a, b []float32) float32 {
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// CosineDistance computes 1 - cosine_similarity.
func CosineDistance(a, b []float32) float32 {
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
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
