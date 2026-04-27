package diskann

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// DiskIndex is the serialized on-disk format for a DiskANN index.
// It combines: Vamana graph topology + PQ codebook + PQ codes + raw embeddings.
//
// Memory layout when loaded:
//   - Graph neighbors (flat arrays): ~R*4 bytes/node  (RAM)
//   - PQ codes: M bytes/node                          (RAM)
//   - PQ codebook: M*K*SubDim*4 bytes                 (RAM)
//   - Raw embeddings: dims*4 bytes/node               (disk/mmap)
//
// Total RAM: ~(R*4 + M) bytes/node. For R=64, M=32: ~288 bytes/node.
// vs HNSW in-memory: ~(M*2*4 + dims*4) bytes/node. For M=32, dims=128: ~768 bytes/node.
type DiskIndex struct {
	// Header
	NumNodes   int64
	Dims       int
	R          int // max out-degree
	Medoid     int64
	PQM        int // number of sub-quantizers
	PQK        int // centroids per sub-quantizer
	PQSubDim   int // sub-vector dimensionality

	// Graph topology: neighbors[i] has neighborOffsets[i]..neighborOffsets[i+1] entries.
	NeighborOffsets []int64
	Neighbors       []int64

	// PQ data
	Codebook *PQCodebook
	PQCodes  [][]byte // PQCodes[nodeIdx] = M-byte code

	// Raw embeddings (all in-memory for Build; mmap'd for search).
	Embeddings [][]float32
}

// DiskANNMagic identifies a gleann DiskANN index file.
const DiskANNMagic uint32 = 0x444B414E // "DKAN"

// DiskANNVersion is the current format version.
const DiskANNVersion uint32 = 1

// BuildDiskIndex constructs a full DiskANN index from embeddings.
func BuildDiskIndex(embeddings [][]float32, cfg DiskANNConfig) (*DiskIndex, error) {
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings")
	}

	dims := len(embeddings[0])
	cfg.Defaults(dims)

	distFunc := GetDistanceFunc(string(cfg.DistanceMetric))

	// Step 1: Build Vamana graph.
	graph := NewVamanaGraph(cfg.R, cfg.L, dims, cfg.Alpha, distFunc)
	graph.Build(embeddings)

	// Step 2: Train PQ codebook.
	pqM := cfg.PQDim
	pqK := cfg.PQCentroids
	pq, err := TrainPQ(embeddings, pqM, pqK)
	if err != nil {
		return nil, fmt.Errorf("pq training: %w", err)
	}

	// Step 3: Encode all vectors.
	pqCodes := pq.EncodeAll(embeddings)

	// Step 4: Flatten graph topology into CSR-like structure.
	n := len(embeddings)
	neighborOffsets := make([]int64, n+1)
	var allNeighbors []int64

	offset := int64(0)
	for i := 0; i < n; i++ {
		neighborOffsets[i] = offset
		nb := graph.GetNeighbors(int64(i))
		allNeighbors = append(allNeighbors, nb...)
		offset += int64(len(nb))
	}
	neighborOffsets[n] = offset

	return &DiskIndex{
		NumNodes:        int64(n),
		Dims:            dims,
		R:               cfg.R,
		Medoid:          graph.Medoid(),
		PQM:             pq.M,
		PQK:             pq.K, // actual K from trained codebook (may be clamped)
		PQSubDim:        pq.SubDim,
		NeighborOffsets: neighborOffsets,
		Neighbors:       allNeighbors,
		Codebook:        pq,
		PQCodes:         pqCodes,
		Embeddings:      embeddings,
	}, nil
}

// WriteTo serializes the DiskIndex to a writer.
// Format:
//
//	[magic:4][version:4][numNodes:8][dims:4][R:4][medoid:8]
//	[pqM:4][pqK:4][pqSubDim:4]
//	[neighborOffsets: (N+1)*8 bytes]
//	[neighbors: totalEdges*8 bytes]
//	[PQ centroids: M*K*SubDim*4 bytes]
//	[PQ codes: N*M bytes]
//	[raw embeddings: N*dims*4 bytes]
func (idx *DiskIndex) WriteTo(w io.Writer) (int64, error) {
	var written int64

	// Header.
	if err := binary.Write(w, binary.LittleEndian, DiskANNMagic); err != nil {
		return written, err
	}
	written += 4
	if err := binary.Write(w, binary.LittleEndian, DiskANNVersion); err != nil {
		return written, err
	}
	written += 4
	if err := binary.Write(w, binary.LittleEndian, idx.NumNodes); err != nil {
		return written, err
	}
	written += 8
	if err := binary.Write(w, binary.LittleEndian, int32(idx.Dims)); err != nil {
		return written, err
	}
	written += 4
	if err := binary.Write(w, binary.LittleEndian, int32(idx.R)); err != nil {
		return written, err
	}
	written += 4
	if err := binary.Write(w, binary.LittleEndian, idx.Medoid); err != nil {
		return written, err
	}
	written += 8
	if err := binary.Write(w, binary.LittleEndian, int32(idx.PQM)); err != nil {
		return written, err
	}
	written += 4
	if err := binary.Write(w, binary.LittleEndian, int32(idx.PQK)); err != nil {
		return written, err
	}
	written += 4
	if err := binary.Write(w, binary.LittleEndian, int32(idx.PQSubDim)); err != nil {
		return written, err
	}
	written += 4

	// Neighbor offsets.
	for _, off := range idx.NeighborOffsets {
		if err := binary.Write(w, binary.LittleEndian, off); err != nil {
			return written, err
		}
		written += 8
	}

	// Neighbors.
	for _, nID := range idx.Neighbors {
		if err := binary.Write(w, binary.LittleEndian, nID); err != nil {
			return written, err
		}
		written += 8
	}

	// PQ centroids: [M][K][SubDim] float32.
	for sq := 0; sq < idx.PQM; sq++ {
		for k := 0; k < idx.PQK; k++ {
			for d := 0; d < idx.PQSubDim; d++ {
				if err := binary.Write(w, binary.LittleEndian, idx.Codebook.Centroids[sq][k][d]); err != nil {
					return written, err
				}
				written += 4
			}
		}
	}

	// PQ codes: [N][M] uint8.
	for i := int64(0); i < idx.NumNodes; i++ {
		n, err := w.Write(idx.PQCodes[i])
		written += int64(n)
		if err != nil {
			return written, err
		}
	}

	// Raw embeddings: [N][dims] float32.
	for i := int64(0); i < idx.NumNodes; i++ {
		for d := 0; d < idx.Dims; d++ {
			if err := binary.Write(w, binary.LittleEndian, idx.Embeddings[i][d]); err != nil {
				return written, err
			}
			written += 4
		}
	}

	return written, nil
}

// ReadDiskIndex deserializes a DiskIndex from a reader.
func ReadDiskIndex(r io.Reader) (*DiskIndex, error) {
	var magic, version uint32
	if err := binary.Read(r, binary.LittleEndian, &magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	if magic != DiskANNMagic {
		return nil, fmt.Errorf("bad magic: 0x%08X (expected 0x%08X)", magic, DiskANNMagic)
	}
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	if version != DiskANNVersion {
		return nil, fmt.Errorf("unsupported version: %d", version)
	}

	idx := &DiskIndex{}

	if err := binary.Read(r, binary.LittleEndian, &idx.NumNodes); err != nil {
		return nil, err
	}
	var dims32, r32 int32
	if err := binary.Read(r, binary.LittleEndian, &dims32); err != nil {
		return nil, err
	}
	idx.Dims = int(dims32)
	if err := binary.Read(r, binary.LittleEndian, &r32); err != nil {
		return nil, err
	}
	idx.R = int(r32)
	if err := binary.Read(r, binary.LittleEndian, &idx.Medoid); err != nil {
		return nil, err
	}

	var pqM32, pqK32, pqSubDim32 int32
	if err := binary.Read(r, binary.LittleEndian, &pqM32); err != nil {
		return nil, err
	}
	idx.PQM = int(pqM32)
	if err := binary.Read(r, binary.LittleEndian, &pqK32); err != nil {
		return nil, err
	}
	idx.PQK = int(pqK32)
	if err := binary.Read(r, binary.LittleEndian, &pqSubDim32); err != nil {
		return nil, err
	}
	idx.PQSubDim = int(pqSubDim32)

	// Validate before allocation.
	if idx.NumNodes < 0 || idx.NumNodes > math.MaxInt32 {
		return nil, fmt.Errorf("invalid numNodes: %d", idx.NumNodes)
	}
	if idx.Dims <= 0 || idx.Dims > 65536 {
		return nil, fmt.Errorf("invalid dims: %d", idx.Dims)
	}

	n := idx.NumNodes

	// Neighbor offsets: N+1 entries.
	idx.NeighborOffsets = make([]int64, n+1)
	for i := int64(0); i <= n; i++ {
		if err := binary.Read(r, binary.LittleEndian, &idx.NeighborOffsets[i]); err != nil {
			return nil, fmt.Errorf("read neighbor offset %d: %w", i, err)
		}
	}

	// Neighbors.
	totalEdges := idx.NeighborOffsets[n]
	if totalEdges < 0 || totalEdges > n*int64(idx.R)*2 {
		return nil, fmt.Errorf("invalid total edges: %d", totalEdges)
	}
	idx.Neighbors = make([]int64, totalEdges)
	for i := int64(0); i < totalEdges; i++ {
		if err := binary.Read(r, binary.LittleEndian, &idx.Neighbors[i]); err != nil {
			return nil, fmt.Errorf("read neighbor %d: %w", i, err)
		}
	}

	// PQ centroids.
	idx.Codebook = &PQCodebook{
		M:         idx.PQM,
		K:         idx.PQK,
		SubDim:    idx.PQSubDim,
		Dims:      idx.Dims,
		Centroids: make([][][]float32, idx.PQM),
	}
	for sq := 0; sq < idx.PQM; sq++ {
		idx.Codebook.Centroids[sq] = make([][]float32, idx.PQK)
		for k := 0; k < idx.PQK; k++ {
			centroid := make([]float32, idx.PQSubDim)
			for d := 0; d < idx.PQSubDim; d++ {
				if err := binary.Read(r, binary.LittleEndian, &centroid[d]); err != nil {
					return nil, fmt.Errorf("read centroid: %w", err)
				}
			}
			idx.Codebook.Centroids[sq][k] = centroid
		}
	}

	// PQ codes.
	idx.PQCodes = make([][]byte, n)
	for i := int64(0); i < n; i++ {
		code := make([]byte, idx.PQM)
		if _, err := io.ReadFull(r, code); err != nil {
			return nil, fmt.Errorf("read pq code %d: %w", i, err)
		}
		idx.PQCodes[i] = code
	}

	// Raw embeddings.
	idx.Embeddings = make([][]float32, n)
	for i := int64(0); i < n; i++ {
		vec := make([]float32, idx.Dims)
		for d := 0; d < idx.Dims; d++ {
			if err := binary.Read(r, binary.LittleEndian, &vec[d]); err != nil {
				return nil, fmt.Errorf("read embedding %d: %w", i, err)
			}
		}
		idx.Embeddings[i] = vec
	}

	return idx, nil
}

// GetNeighbors returns the neighbors of node i from the flat arrays.
func (idx *DiskIndex) GetNeighbors(i int64) []int64 {
	start := idx.NeighborOffsets[i]
	end := idx.NeighborOffsets[i+1]
	return idx.Neighbors[start:end]
}

// RAMUsage estimates the RAM needed when embeddings are on disk.
func (idx *DiskIndex) RAMUsage() int64 {
	// Graph: (N+1+totalEdges) * 8 bytes
	graphBytes := int64(len(idx.NeighborOffsets)+len(idx.Neighbors)) * 8
	// PQ codebook: M*K*SubDim*4
	codebookBytes := int64(idx.PQM) * int64(idx.PQK) * int64(idx.PQSubDim) * 4
	// PQ codes: N*M
	codesBytes := idx.NumNodes * int64(idx.PQM)
	return graphBytes + codebookBytes + codesBytes
}

// DiskUsage estimates the total disk footprint.
func (idx *DiskIndex) DiskUsage() int64 {
	return idx.RAMUsage() + idx.NumNodes*int64(idx.Dims)*4 // + raw embeddings
}
