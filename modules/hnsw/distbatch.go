package hnsw

import "reflect"

// isL2Func reports whether f is L2DistanceSquared itself (not a wrapper).
func isL2Func(f DistanceFunc) bool {
	return f != nil && reflect.ValueOf(f).Pointer() == reflect.ValueOf(DistanceFunc(L2DistanceSquared)).Pointer()
}

// isBuiltinDist reports whether f is one of the package's own metrics. Those
// give f(a, b) and f(b, a) the same bits: a difference and its negation have
// the same square, a product does not depend on the order of its factors, and
// the sums run over the same index order either way.
func isBuiltinDist(f DistanceFunc) bool {
	if f == nil {
		return false
	}
	p := reflect.ValueOf(f).Pointer()
	for _, builtin := range []DistanceFunc{L2DistanceSquared, CosineDistance, InnerProductDistance} {
		if p == reflect.ValueOf(builtin).Pointer() {
			return true
		}
	}
	return false
}

// l2Dist4 computes L2DistanceSquared(q, a), (q, b), (q, c) and (q, d) in one
// pass. Every sum is accumulated in the same order as L2DistanceSquared, so
// each result is bit-identical to the single call; the four independent
// dependency chains let the CPU overlap the float adds that make the scalar
// loop latency-bound. All five slices must have the same non-zero length.
func l2Dist4(q, a, b, c, d []float32) (float32, float32, float32, float32) {
	n := len(q)
	a, b, c, d = a[:n], b[:n], c[:n], d[:n]
	var s0, s1, s2, s3 float32
	for i, x := range q {
		d0 := x - a[i]
		d1 := x - b[i]
		d2 := x - c[i]
		d3 := x - d[i]
		s0 += d0 * d0
		s1 += d1 * d1
		s2 += d2 * d2
		s3 += d3 * d3
	}
	return s0, s1, s2, s3
}

// l2Dist3 is l2Dist4 for three vectors: the three-wide end of a batch.
func l2Dist3(q, a, b, c []float32) (float32, float32, float32) {
	n := len(q)
	a, b, c = a[:n], b[:n], c[:n]
	var s0, s1, s2 float32
	for i, x := range q {
		d0 := x - a[i]
		d1 := x - b[i]
		d2 := x - c[i]
		s0 += d0 * d0
		s1 += d1 * d1
		s2 += d2 * d2
	}
	return s0, s1, s2
}

// l2Dist2 is l2Dist4 for two vectors: the two-wide end of a batch.
func l2Dist2(q, a, b []float32) (float32, float32) {
	n := len(q)
	a, b = a[:n], b[:n]
	var s0, s1 float32
	for i, x := range q {
		d0 := x - a[i]
		d1 := x - b[i]
		s0 += d0 * d0
		s1 += d1 * d1
	}
	return s0, s1
}

// plainL2Node reports whether n holds a float32 vector of d values, which is
// what the batched kernels take.
func plainL2Node(n *Node, d int) bool {
	return len(n.VectorF16) == 0 && len(n.Vector) == d
}

// distBatch sets out[i] = g.vectorNodeDist(q, nodes[i]) for every node. With
// the plain L2 metric, float32 vectors of the query's size are processed four
// at a time (l2Dist4) and the last two or three together as well (l2Dist2,
// l2Dist3); every other case takes the scalar path. The values are identical
// either way.
func (g *Graph) distBatch(q []float32, nodes []*Node, out []float32) {
	i := 0
	if g.batchL2 && len(q) > 0 {
		for ; i+4 <= len(nodes); i += 4 {
			n0, n1, n2, n3 := nodes[i], nodes[i+1], nodes[i+2], nodes[i+3]
			if len(n0.VectorF16)|len(n1.VectorF16)|len(n2.VectorF16)|len(n3.VectorF16) != 0 ||
				len(n0.Vector) != len(q) || len(n1.Vector) != len(q) || len(n2.Vector) != len(q) || len(n3.Vector) != len(q) {
				break
			}
			out[i], out[i+1], out[i+2], out[i+3] = l2Dist4(q, n0.Vector, n1.Vector, n2.Vector, n3.Vector)
		}
		// Two or three nodes are left only when the loop above ran to its end.
		switch len(nodes) - i {
		case 3:
			n0, n1, n2 := nodes[i], nodes[i+1], nodes[i+2]
			if plainL2Node(n0, len(q)) && plainL2Node(n1, len(q)) && plainL2Node(n2, len(q)) {
				out[i], out[i+1], out[i+2] = l2Dist3(q, n0.Vector, n1.Vector, n2.Vector)
				i += 3
			}
		case 2:
			n0, n1 := nodes[i], nodes[i+1]
			if plainL2Node(n0, len(q)) && plainL2Node(n1, len(q)) {
				out[i], out[i+1] = l2Dist2(q, n0.Vector, n1.Vector)
				i += 2
			}
		}
	}
	for ; i < len(nodes); i++ {
		out[i] = g.vectorNodeDist(q, nodes[i])
	}
}
