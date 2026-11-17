package hnsw

import (
	"math"
)

// f16ToF32Table is a 256 KiB lookup table precomputed at init time.
// It maps every 16-bit half-precision float bit pattern to its exact float32 value,
// providing fast, allocation-free conversion on the fly.
var f16ToF32Table [65536]float32

func init() {
	for i := 0; i < 65536; i++ {
		f16ToF32Table[i] = computeFloat16ToFloat32(uint16(i))
	}
}

// Float32ToFloat16 converts a float32 to IEEE 754 binary16 (half-precision float)
// using round-to-nearest-even, correctly handling subnormals, infinities, NaNs,
// and signed zeros (-0.0 and +0.0).
func Float32ToFloat16(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 16) & 0x8000)
	exp := int((bits >> 23) & 0xFF)
	frac := bits & 0x007FFFFF

	if exp == 255 {
		if frac == 0 {
			return sign | 0x7C00 // Infinity
		}
		// NaN: preserve upper mantissa bits, ensuring non-zero fraction
		nanFrac := uint16(frac >> 13)
		if nanFrac == 0 {
			nanFrac = 0x0200 // standard quiet NaN
		}
		return sign | 0x7C00 | (nanFrac & 0x03FF)
	}

	e := exp - 127

	var m uint32
	if exp > 0 {
		m = (1 << 23) | frac
	} else {
		if frac == 0 {
			return sign // Zero (+0 or -0)
		}
		m = frac
		e = -126
	}

	// Normal float16 range: e in [-14, 15]
	// Max finite normal is 65504 (0x7BFF). Midpoint to 65536 is 65520.
	// In round-to-nearest-even, values >= 65520 overflow to infinity.
	if e > 15 {
		return sign | 0x7C00
	}

	if e < -14 {
		// Subnormal in float16 (or underflow to zero).
		// In float16, subnormals have exponent 0 and value F16 * 2^(-24).
		// Since value is m * 2^(e - 23), F16 = m * 2^(e + 1) = m >> (-e - 1).
		shift := -e - 1
		if shift >= 32 {
			return sign
		}

		roundBit := (m >> (shift - 1)) & 1
		var sticky uint32
		if shift > 1 {
			sticky = m & ((uint32(1) << (shift - 1)) - 1)
		}
		lsb := (m >> shift) & 1
		roundUp := (roundBit == 1) && (sticky != 0 || lsb == 1)

		res := m >> shift
		if roundUp {
			res++
		}
		// If res rounded up from 0x03FF to 0x0400, it becomes min normal (exp=1, frac=0).
		return sign | uint16(res)
	}

	// Normal float16: e in [-14, 15], target exponent E16 = e + 15.
	// Discard 13 lower bits from the 23-bit mantissa.
	roundBit := (m >> 12) & 1
	sticky := m & 0x0FFF
	lsb := (m >> 13) & 1
	roundUp := (roundBit == 1) && (sticky != 0 || lsb == 1)

	mant := m >> 13
	if roundUp {
		mant++
	}

	// If rounding overflowed mantissa from 11 bits (1 << 11) to 12 bits:
	if mant >= (1 << 11) {
		mant >>= 1
		e++
	}

	exp16 := e + 15
	if exp16 >= 31 {
		return sign | 0x7C00
	}

	return sign | uint16(exp16<<10) | uint16(mant&0x03FF)
}

// Float16ToFloat32 converts an IEEE 754 binary16 uint16 bit pattern to float32.
// It uses the precomputed lookup table for high performance.
func Float16ToFloat32(h uint16) float32 {
	return f16ToF32Table[h]
}

// computeFloat16ToFloat32 computes the float32 value from a binary16 bit pattern.
func computeFloat16ToFloat32(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := (h >> 10) & 0x1F
	frac := uint32(h & 0x03FF)

	var bits uint32
	switch exp {
	case 0:
		if frac == 0 {
			bits = sign
		} else {
			// Subnormal float16 becomes normal float32:
			// Value = frac * 2^(-24).
			shift := 0
			for (frac & 0x0400) == 0 {
				frac <<= 1
				shift++
			}
			frac &= 0x03FF
			exp32 := uint32(113 - shift) // (-14 - shift) + 127
			frac32 := frac << 13
			bits = sign | (exp32 << 23) | frac32
		}
	case 0x1F:
		if frac == 0 {
			bits = sign | 0x7F800000 // Inf
		} else {
			bits = sign | 0x7F800000 | (frac << 13) // NaN
		}
	default:
		exp32 := uint32(exp + 112) // (exp - 15) + 127
		frac32 := frac << 13
		bits = sign | (exp32 << 23) | frac32
	}
	return math.Float32frombits(bits)
}

// Float32SliceToFloat16 converts a slice of float32 to float16 ([]uint16).
func Float32SliceToFloat16(src []float32) []uint16 {
	if src == nil {
		return nil
	}
	dst := make([]uint16, len(src))
	for i, v := range src {
		dst[i] = Float32ToFloat16(v)
	}
	return dst
}

// Float16SliceToFloat32 converts a slice of float16 ([]uint16) to float32.
func Float16SliceToFloat32(src []uint16) []float32 {
	if src == nil {
		return nil
	}
	dst := make([]float32, len(src))
	for i, v := range src {
		dst[i] = f16ToF32Table[v]
	}
	return dst
}

// L2DistanceSquaredF16 computes the squared L2 distance between float32 query `a`
// and float16 vector `b`, converting components on the fly without keeping a float32 copy.
func L2DistanceSquaredF16(a []float32, b []uint16) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var sum float32
	for i := range a {
		bf := f16ToF32Table[b[i]]
		d := a[i] - bf
		sum += d * d
	}
	return sum
}

// CosineDistanceF16 computes cosine distance: 1 - cosine_similarity on the fly.
func CosineDistanceF16(a []float32, b []uint16) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var dot, normA, normB float32
	for i := range a {
		bf := f16ToF32Table[b[i]]
		dot += a[i] * bf
		normA += a[i] * a[i]
		normB += bf * bf
	}
	denom := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
	if denom == 0 {
		return float32(math.MaxFloat32)
	}
	return 1.0 - dot/denom
}

// InnerProductDistanceF16 computes negative inner product distance on the fly.
func InnerProductDistanceF16(a []float32, b []uint16) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var dot float32
	for i := range a {
		bf := f16ToF32Table[b[i]]
		dot += a[i] * bf
	}
	return -dot
}

// L2DistanceSquaredF16F16 computes squared L2 distance between two float16 vectors.
func L2DistanceSquaredF16F16(a, b []uint16) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var sum float32
	for i := range a {
		af := f16ToF32Table[a[i]]
		bf := f16ToF32Table[b[i]]
		d := af - bf
		sum += d * d
	}
	return sum
}

// CosineDistanceF16F16 computes cosine distance between two float16 vectors.
func CosineDistanceF16F16(a, b []uint16) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var dot, normA, normB float32
	for i := range a {
		af := f16ToF32Table[a[i]]
		bf := f16ToF32Table[b[i]]
		dot += af * bf
		normA += af * af
		normB += bf * bf
	}
	denom := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
	if denom == 0 {
		return float32(math.MaxFloat32)
	}
	return 1.0 - dot/denom
}

// InnerProductDistanceF16F16 computes negative inner product distance between two float16 vectors.
func InnerProductDistanceF16F16(a, b []uint16) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var dot float32
	for i := range a {
		af := f16ToF32Table[a[i]]
		bf := f16ToF32Table[b[i]]
		dot += af * bf
	}
	return -dot
}

// GetDistanceFuncF16 returns the distance function between float32 query and float16 vector.
func GetDistanceFuncF16(metric string) func([]float32, []uint16) float32 {
	switch metric {
	case "cosine":
		return CosineDistanceF16
	case "ip", "inner_product":
		return InnerProductDistanceF16
	default:
		return L2DistanceSquaredF16
	}
}

// GetDistanceFuncF16F16 returns the distance function between two float16 vectors.
func GetDistanceFuncF16F16(metric string) func([]uint16, []uint16) float32 {
	switch metric {
	case "cosine":
		return CosineDistanceF16F16
	case "ip", "inner_product":
		return InnerProductDistanceF16F16
	default:
		return L2DistanceSquaredF16F16
	}
}
