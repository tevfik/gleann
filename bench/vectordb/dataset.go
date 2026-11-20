package vectordb

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
)

// Dataset contains vectors, metadata, and benchmark queries.
type Dataset struct {
	Dims    int
	IDs     []int64
	Vectors [][]float32
	Metas   []Meta
	Queries [][]float32
	// DupFraction is the share of vectors replaced by near-copies
	// (AddNearDuplicates); 0 for a plain dataset.
	DupFraction float64
}

// AddNearDuplicates replaces the first frac of the vectors with near-copies
// of a few base vectors taken from the end of the set (tiny Gaussian noise,
// renormalised). Real corpora hold such near-copies (license headers,
// boilerplate, repeated table or chart text); they starve graph indexes
// whose neighbour selection rejects candidates closer to each other than to
// the node. Deterministic for a given seed; the metadata is kept.
func AddNearDuplicates(ds *Dataset, frac float64, bases int, seed int64) {
	n := int(frac * float64(len(ds.Vectors)))
	if n <= 0 || bases <= 0 || bases >= len(ds.Vectors) {
		return
	}
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		base := ds.Vectors[len(ds.Vectors)-1-r.Intn(bases)]
		v := make([]float32, len(base))
		for j := range v {
			v[j] = base[j] + float32(r.NormFloat64()*1e-4)
		}
		unitNormalize(v)
		ds.Vectors[i] = v
	}
	ds.DupFraction = frac
}

// GenerateDataset creates a synthetic dataset using a deterministic seeded
// Gaussian mixture (256 clusters, unit-normalised) with metadata and queries
// per RFC 006 section 4.3.
func GenerateDataset(size, dims, numQueries int, seed int64) *Dataset {
	if seed == 0 {
		seed = 42
	}
	r := rand.New(rand.NewSource(seed))

	const numClusters = 256
	centers := make([][]float32, numClusters)
	for c := 0; c < numClusters; c++ {
		vec := make([]float32, dims)
		for d := 0; d < dims; d++ {
			vec[d] = float32(r.NormFloat64())
		}
		unitNormalize(vec)
		centers[c] = vec
	}

	samplePoint := func() []float32 {
		c := r.Intn(numClusters)
		vec := make([]float32, dims)
		const sigma = 0.1
		for d := 0; d < dims; d++ {
			vec[d] = centers[c][d] + float32(r.NormFloat64()*sigma)
		}
		unitNormalize(vec)
		return vec
	}

	ids := make([]int64, size)
	vecs := make([][]float32, size)
	metas := make([]Meta, size)
	for i := 0; i < size; i++ {
		ids[i] = int64(i)
		vecs[i] = samplePoint()

		fileIdx := i / 24
		kind := "code"
		if fileIdx%2 != 0 {
			kind = "prose"
		}
		metas[i] = Meta{
			Source: fmt.Sprintf("file_%06d.go", fileIdx),
			Kind:   kind,
			Tag:    i % 100, // 0..99 uniform distribution
		}
	}

	if numQueries <= 0 {
		numQueries = 1000
	}
	queries := make([][]float32, numQueries)
	for q := 0; q < numQueries; q++ {
		queries[q] = samplePoint()
	}

	return &Dataset{
		Dims:    dims,
		IDs:     ids,
		Vectors: vecs,
		Metas:   metas,
		Queries: queries,
	}
}

func unitNormalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	if norm > 0 {
		inv := 1.0 / norm
		for i := range v {
			v[i] *= inv
		}
	}
}

// LoadFvecs reads vectors from a standard .fvecs file (TEXMEX format).
// Each vector has a 4-byte int32 dimension followed by dim 4-byte float32s.
func LoadFvecs(path string) ([][]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening fvecs file %s: %w", path, err)
	}
	defer f.Close()

	r := bufio.NewReader(f)
	var vecs [][]float32
	var dimBuf [4]byte

	for {
		_, err := io.ReadFull(r, dimBuf[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading fvecs dimension: %w", err)
		}
		dim := int(binary.LittleEndian.Uint32(dimBuf[:]))
		if dim <= 0 || dim > 100000 {
			return nil, fmt.Errorf("invalid dimension in fvecs: %d", dim)
		}

		raw := make([]byte, dim*4)
		if _, err := io.ReadFull(r, raw); err != nil {
			return nil, fmt.Errorf("reading fvecs vector data: %w", err)
		}

		vec := make([]float32, dim)
		for d := 0; d < dim; d++ {
			bits := binary.LittleEndian.Uint32(raw[d*4 : (d+1)*4])
			vec[d] = math.Float32frombits(bits)
		}
		vecs = append(vecs, vec)
	}

	return vecs, nil
}

// SaveFvecs writes vectors to a standard .fvecs file.
func SaveFvecs(path string, vecs [][]float32) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating fvecs file %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	var dimBuf [4]byte

	for _, vec := range vecs {
		dim := uint32(len(vec))
		binary.LittleEndian.PutUint32(dimBuf[:], dim)
		if _, err := w.Write(dimBuf[:]); err != nil {
			return fmt.Errorf("writing fvecs dimension: %w", err)
		}

		raw := make([]byte, len(vec)*4)
		for d, val := range vec {
			bits := math.Float32bits(val)
			binary.LittleEndian.PutUint32(raw[d*4:(d+1)*4], bits)
		}
		if _, err := w.Write(raw); err != nil {
			return fmt.Errorf("writing fvecs vector: %w", err)
		}
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing fvecs file %s: %w", path, err)
	}
	return nil
}

// LoadIvecs reads integer vectors from a standard .ivecs file (TEXMEX format).
// Each entry has a 4-byte int32 dimension followed by dim 4-byte int32s.
func LoadIvecs(path string) ([][]int32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening ivecs file %s: %w", path, err)
	}
	defer f.Close()

	r := bufio.NewReader(f)
	var entries [][]int32
	var dimBuf [4]byte

	for {
		_, err := io.ReadFull(r, dimBuf[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading ivecs dimension: %w", err)
		}
		dim := int(binary.LittleEndian.Uint32(dimBuf[:]))
		if dim <= 0 || dim > 100000 {
			return nil, fmt.Errorf("invalid dimension in ivecs: %d", dim)
		}

		raw := make([]byte, dim*4)
		if _, err := io.ReadFull(r, raw); err != nil {
			return nil, fmt.Errorf("reading ivecs data: %w", err)
		}

		entry := make([]int32, dim)
		for d := 0; d < dim; d++ {
			entry[d] = int32(binary.LittleEndian.Uint32(raw[d*4 : (d+1)*4]))
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// SaveIvecs writes integer vectors to a standard .ivecs file.
func SaveIvecs(path string, entries [][]int32) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating ivecs file %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	var dimBuf [4]byte

	for _, entry := range entries {
		dim := uint32(len(entry))
		binary.LittleEndian.PutUint32(dimBuf[:], dim)
		if _, err := w.Write(dimBuf[:]); err != nil {
			return fmt.Errorf("writing ivecs dimension: %w", err)
		}

		raw := make([]byte, len(entry)*4)
		for d, val := range entry {
			binary.LittleEndian.PutUint32(raw[d*4:(d+1)*4], uint32(val))
		}
		if _, err := w.Write(raw); err != nil {
			return fmt.Errorf("writing ivecs entry: %w", err)
		}
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing ivecs file %s: %w", path, err)
	}
	return nil
}
