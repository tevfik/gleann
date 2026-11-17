package hnsw

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// MmapGraph is a zero-copy, memory-mapped representation of the CSRGraph.
// It bypasses the Go Garbage Collector by keeping all node and edge data
// off-heap in a flat `[]byte` slice backed directly by a file on disk. On
// Windows the slice is a heap copy of the file instead and the file is closed
// after loading, so a new generation can be renamed over it (see
// releaseFileAfterLoad in mmap_windows.go).
type MmapGraph struct {
	data     []byte
	file     *os.File
	fileSize int64

	// Extracted header info
	NumNodes    int64
	Dimensions  int
	MaxLevel    int
	EntryPoint  int64
	M           int
	NumStored   int64
	VectorDType string

	// Offsets into the memory-mapped byte slice
	offsetIDMap      int64
	offsetNodeLevels int64
	offsetLevels     int64
	offsetEmbeddings int64

	// Index of stored embeddings: external ID -> byte offset in mg.data of vector start
	storedOffsets map[int64]int64

	// numIDs is the length of the ID map (and of the node levels array).
	numIDs int64

	// levelPos[l] (l >= 1) maps an internal index to its position among the
	// nodes of level l: a CSR level lists only the nodes that reach it, in ID
	// map order, so its NodeOffsets are indexed by that position. Level 0
	// holds every node and needs no map.
	levelPos []map[int64]int64

	// extToInt maps external IDs (the values of the neighbor lists and of
	// the entry point) to internal indices. It is nil when the ID map is the
	// identity (IDs 0..n-1, the usual case), where both coincide.
	extToInt map[int64]int64

	// Caches to avoid repeated slice arithmetic
	levels []mmapLevel
}

type mmapLevel struct {
	numNodes     int64
	offsetNodes  int64 // Points to the start of NodeOffsets array (int64)
	offsetEdges  int64 // Points to the start of Neighbors array (int64)
	numNeighbors int64
}

// OpenMmapGraph opens a CSR binary file and maps it directly into memory.
func OpenMmapGraph(path string) (*MmapGraph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := info.Size()

	// Memory map the file (Read Only); on Windows, load a copy and close it
	// (see releaseFileAfterLoad).
	data, err := mmapFile(f, size)
	if err != nil {
		f.Close()
		return nil, err
	}
	if releaseFileAfterLoad {
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("closing index file: %w", err)
		}
		f = nil
	}

	mg := &MmapGraph{
		data:     data,
		file:     f,
		fileSize: size,
	}

	if err := mg.parseHeaders(); err != nil {
		mg.Close()
		return nil, err
	}

	return mg, nil
}

// OpenMmapGraphFromData initializes an MmapGraph from an existing byte slice in memory.
func OpenMmapGraphFromData(data []byte) (*MmapGraph, error) {
	mg := &MmapGraph{
		data:     data,
		fileSize: int64(len(data)),
	}
	if err := mg.parseHeaders(); err != nil {
		return nil, err
	}
	return mg, nil
}

func (mg *MmapGraph) bytesPerElement() int {
	if mg.VectorDType == VectorDTypeF16 {
		return 2
	}
	return 4
}

func (mg *MmapGraph) parseHeaders() error {
	dataLen := int64(len(mg.data))
	if dataLen < 44 {
		return fmt.Errorf("file too small to contain header (%d bytes)", dataLen)
	}

	// === Header ===
	offset := int64(0)
	magic := binary.LittleEndian.Uint32(mg.data[offset:])
	offset += 4
	if magic != CSRMagic {
		return fmt.Errorf("invalid magic: 0x%X", magic)
	}

	version := binary.LittleEndian.Uint32(mg.data[offset:])
	offset += 4
	if version != CSRVersion1 && version != CSRVersion2 {
		return fmt.Errorf("unsupported version: %d", version)
	}

	mg.NumNodes = int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	// Dimensions, MaxLevel and M are written as int32 (an empty graph has
	// MaxLevel -1).
	mg.Dimensions = int(int32(binary.LittleEndian.Uint32(mg.data[offset:])))
	offset += 4
	mg.MaxLevel = int(int32(binary.LittleEndian.Uint32(mg.data[offset:])))
	offset += 4
	mg.EntryPoint = int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	mg.M = int(int32(binary.LittleEndian.Uint32(mg.data[offset:])))
	offset += 4
	mg.NumStored = int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	if mg.Dimensions < 0 {
		return fmt.Errorf("invalid dimensions: %d", mg.Dimensions)
	}

	if version >= CSRVersion2 {
		if offset+4 > dataLen {
			return fmt.Errorf("truncated: cannot read dtypeCode at offset %d", offset)
		}
		dtypeCode := binary.LittleEndian.Uint32(mg.data[offset:])
		offset += 4
		if dtypeCode == DTypeCodeF16 {
			mg.VectorDType = VectorDTypeF16
		} else {
			mg.VectorDType = VectorDTypeF32
		}
	} else {
		mg.VectorDType = VectorDTypeF32
	}

	// === ID Map ===
	if offset+8 > dataLen {
		return fmt.Errorf("truncated: cannot read numIDs at offset %d", offset)
	}
	numIDs := int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	if numIDs < 0 || numIDs > (dataLen-offset)/8 {
		return fmt.Errorf("invalid numIDs: %d (would exceed file size)", numIDs)
	}
	mg.offsetIDMap = offset
	mg.numIDs = numIDs
	offset += numIDs * 8

	// === Node Levels ===
	mg.offsetNodeLevels = offset
	if numIDs > (dataLen-offset)/4 {
		return fmt.Errorf("truncated: NodeLevels section exceeds file size")
	}
	offset += numIDs * 4

	// === Per-level Data ===
	if offset+4 > dataLen {
		return fmt.Errorf("truncated: cannot read numLevels at offset %d", offset)
	}
	numLevels := int(binary.LittleEndian.Uint32(mg.data[offset:]))
	offset += 4
	if numLevels < 0 || numLevels > 256 {
		return fmt.Errorf("invalid numLevels: %d", numLevels)
	}
	// The search descends from MaxLevel: it must name a stored level.
	if mg.MaxLevel != numLevels-1 {
		return fmt.Errorf("max level %d does not match %d stored levels", mg.MaxLevel, numLevels)
	}

	mg.offsetLevels = offset
	mg.levels = make([]mmapLevel, numLevels)

	for l := 0; l < numLevels; l++ {
		if offset+8 > dataLen {
			return fmt.Errorf("truncated at level %d: cannot read numNodes", l)
		}
		nn := int64(binary.LittleEndian.Uint64(mg.data[offset:]))
		offset += 8
		if nn < 0 || nn > numIDs {
			return fmt.Errorf("invalid numNodes at level %d: %d", l, nn)
		}

		levelInfo := mmapLevel{
			numNodes:    nn,
			offsetNodes: offset,
		}

		// Skip NodeOffsets array: (nn + 1) * 8 bytes
		if nn+1 > (dataLen-offset)/8 {
			return fmt.Errorf("truncated at level %d: NodeOffsets overflow", l)
		}
		offset += (nn + 1) * 8

		if offset+8 > dataLen {
			return fmt.Errorf("truncated at level %d: cannot read numNeighbors", l)
		}
		numNeighbors := int64(binary.LittleEndian.Uint64(mg.data[offset:]))
		offset += 8
		if numNeighbors < 0 || numNeighbors > (dataLen-offset)/8 {
			return fmt.Errorf("invalid numNeighbors at level %d: %d", l, numNeighbors)
		}

		levelInfo.offsetEdges = offset
		levelInfo.numNeighbors = numNeighbors

		// Skip Neighbors array: numNeighbors * 8 bytes
		offset += numNeighbors * 8

		mg.levels[l] = levelInfo
	}

	mg.indexLevels()

	// === Stored Embeddings ===
	mg.offsetEmbeddings = offset
	vecBytes := int64(mg.Dimensions * mg.bytesPerElement())
	// NumStored comes from the file. The reader tolerates a cut in the
	// stored embeddings (it indexes the complete ones), so a count above
	// what the file holds is not an error, but it must not size the map:
	// one flipped high byte would allocate tens of gigabytes.
	if mg.NumStored < 0 {
		return fmt.Errorf("invalid numStored: %d", mg.NumStored)
	}
	mg.storedOffsets = make(map[int64]int64, min(mg.NumStored, (dataLen-offset)/(8+vecBytes)))
	cur := offset
	for i := int64(0); i < mg.NumStored; i++ {
		if cur+8+vecBytes > dataLen {
			break
		}
		id := int64(binary.LittleEndian.Uint64(mg.data[cur : cur+8]))
		mg.storedOffsets[id] = cur + 8
		cur += 8 + vecBytes
	}

	return nil
}

// indexLevels builds levelPos and, when the ID map is not the identity,
// extToInt. Only the nodes of the upper levels (about 1/M of them) get a
// levelPos entry.
func (mg *MmapGraph) indexLevels() {
	numLevels := len(mg.levels)
	mg.levelPos = make([]map[int64]int64, numLevels)
	next := make([]int64, numLevels)
	for l := 1; l < numLevels; l++ {
		mg.levelPos[l] = make(map[int64]int64, mg.levels[l].numNodes)
	}
	for i := int64(0); i < mg.numIDs; i++ {
		lvl := int(int32(binary.LittleEndian.Uint32(mg.data[mg.offsetNodeLevels+i*4:])))
		for l := 1; l <= lvl && l < numLevels; l++ {
			if next[l] < mg.levels[l].numNodes {
				mg.levelPos[l][i] = next[l]
				next[l]++
			}
		}
		if mg.extToInt == nil && int64(binary.LittleEndian.Uint64(mg.data[mg.offsetIDMap+i*8:])) != i {
			mg.extToInt = make(map[int64]int64, mg.numIDs)
			for j := int64(0); j < mg.numIDs; j++ {
				mg.extToInt[int64(binary.LittleEndian.Uint64(mg.data[mg.offsetIDMap+j*8:]))] = j
			}
		}
	}
}

// internalIndex returns the internal index of an external ID, or -1 when the
// ID is not in the graph.
func (mg *MmapGraph) internalIndex(externalID int64) int64 {
	if mg.extToInt != nil {
		if idx, ok := mg.extToInt[externalID]; ok {
			return idx
		}
		return -1
	}
	if externalID < 0 || externalID >= mg.numIDs {
		return -1
	}
	return externalID
}

// Close unmaps the memory and closes the file.
func (mg *MmapGraph) Close() error {
	var err1, err2 error
	if mg.data != nil && mg.file != nil {
		err1 = munmapFile(mg.data)
	}
	mg.data = nil
	if mg.file != nil {
		err2 = mg.file.Close()
		mg.file = nil
	}
	if err1 != nil {
		return err1
	}
	return err2
}

// GetExternalID returns the external node ID for a given internal index.
func (mg *MmapGraph) GetExternalID(internalIdx int64) int64 {
	if internalIdx < 0 || internalIdx >= mg.NumNodes || internalIdx >= mg.numIDs {
		return -1
	}
	start := mg.offsetIDMap + (internalIdx * 8)
	if start+8 > int64(len(mg.data)) {
		return -1
	}
	return int64(binary.LittleEndian.Uint64(mg.data[start : start+8]))
}

// GetNodeLevel returns the maximum HNSW level for a given internal index.
func (mg *MmapGraph) GetNodeLevel(internalIdx int64) int {
	if internalIdx < 0 || internalIdx >= mg.NumNodes || internalIdx >= mg.numIDs {
		return -1
	}
	start := mg.offsetNodeLevels + (internalIdx * 4)
	if start+4 > int64(len(mg.data)) {
		return -1
	}
	return int(binary.LittleEndian.Uint32(mg.data[start : start+4]))
}

// GetNeighbors returns the internal neighbor indices for a node at a specific level.
// This is the core operation of graph traversal; it allocates only the result.
func (mg *MmapGraph) GetNeighbors(internalIdx int64, level int) []int64 {
	if level < 0 || level >= len(mg.levels) || internalIdx < 0 {
		return nil
	}

	lvl := &mg.levels[level]
	// Level 0 lists every node in ID map order; an upper level lists only
	// the nodes that reach it.
	pos := internalIdx
	if level > 0 {
		p, ok := mg.levelPos[level][internalIdx]
		if !ok {
			return nil
		}
		pos = p
	}
	if pos >= lvl.numNodes {
		return nil
	}

	// 1. Read start and end offsets from NodeOffsets array
	offsetsStart := lvl.offsetNodes + (pos * 8)
	if offsetsStart+16 > int64(len(mg.data)) {
		return nil
	}
	edgeStartPos := int64(binary.LittleEndian.Uint64(mg.data[offsetsStart : offsetsStart+8]))
	edgeEndPos := int64(binary.LittleEndian.Uint64(mg.data[offsetsStart+8 : offsetsStart+16]))

	count := edgeEndPos - edgeStartPos
	if count <= 0 || edgeStartPos < 0 || edgeEndPos > lvl.numNeighbors {
		return nil
	}

	// 2. Read the neighbor IDs directly
	neighbors := make([]int64, count)
	edgesByteStart := lvl.offsetEdges + (edgeStartPos * 8)
	endByte := edgesByteStart + (count * 8)
	if endByte > int64(len(mg.data)) {
		return nil
	}

	// The lists hold external IDs; translate them to internal indices and
	// drop any that are not in the graph.
	n := 0
	for i := int64(0); i < count; i++ {
		bStart := edgesByteStart + (i * 8)
		if idx := mg.internalIndex(int64(binary.LittleEndian.Uint64(mg.data[bStart : bStart+8]))); idx >= 0 {
			neighbors[n] = idx
			n++
		}
	}

	return neighbors[:n]
}

// GetStoredEmbedding tries to retrieve a stored embedding for an external ID.
func (mg *MmapGraph) GetStoredEmbedding(externalID int64) []float32 {
	if mg.NumStored == 0 || mg.storedOffsets == nil {
		return nil
	}

	offset, ok := mg.storedOffsets[externalID]
	if !ok {
		return nil
	}
	vecBytes := int64(mg.Dimensions * mg.bytesPerElement())
	if offset+vecBytes > int64(len(mg.data)) {
		return nil
	}

	vec := make([]float32, mg.Dimensions)
	if mg.VectorDType == VectorDTypeF16 {
		for j := 0; j < mg.Dimensions; j++ {
			hbits := binary.LittleEndian.Uint16(mg.data[offset+int64(j*2):])
			vec[j] = f16ToF32Table[hbits]
		}
	} else {
		for j := 0; j < mg.Dimensions; j++ {
			fbits := binary.LittleEndian.Uint32(mg.data[offset+int64(j*4):])
			vec[j] = math.Float32frombits(fbits)
		}
	}
	return vec
}

// DistanceToStoredEmbedding computes the distance between query and a stored embedding directly
// from memory-mapped data without allocating a full float32 slice.
func (mg *MmapGraph) DistanceToStoredEmbedding(query []float32, externalID int64, metric DistanceMetric) (float32, bool) {
	if mg.NumStored == 0 || mg.storedOffsets == nil {
		return 0, false
	}
	offset, ok := mg.storedOffsets[externalID]
	if !ok {
		return 0, false
	}
	vecBytes := int64(mg.Dimensions * mg.bytesPerElement())
	if offset+vecBytes > int64(len(mg.data)) || len(query) != mg.Dimensions {
		return 0, false
	}

	if mg.VectorDType == VectorDTypeF16 {
		switch metric {
		case "cosine":
			var dot, normA, normB float32
			for j := 0; j < mg.Dimensions; j++ {
				hbits := binary.LittleEndian.Uint16(mg.data[offset+int64(j*2):])
				bf := f16ToF32Table[hbits]
				dot += query[j] * bf
				normA += query[j] * query[j]
				normB += bf * bf
			}
			denom := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
			if denom == 0 {
				return float32(math.MaxFloat32), true
			}
			return 1.0 - dot/denom, true
		case "ip", "inner_product":
			var dot float32
			for j := 0; j < mg.Dimensions; j++ {
				hbits := binary.LittleEndian.Uint16(mg.data[offset+int64(j*2):])
				bf := f16ToF32Table[hbits]
				dot += query[j] * bf
			}
			return -dot, true
		default: // L2
			var sum float32
			for j := 0; j < mg.Dimensions; j++ {
				hbits := binary.LittleEndian.Uint16(mg.data[offset+int64(j*2):])
				bf := f16ToF32Table[hbits]
				d := query[j] - bf
				sum += d * d
			}
			return sum, true
		}
	} else {
		switch metric {
		case "cosine":
			var dot, normA, normB float32
			for j := 0; j < mg.Dimensions; j++ {
				fbits := binary.LittleEndian.Uint32(mg.data[offset+int64(j*4):])
				bf := math.Float32frombits(fbits)
				dot += query[j] * bf
				normA += query[j] * query[j]
				normB += bf * bf
			}
			denom := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
			if denom == 0 {
				return float32(math.MaxFloat32), true
			}
			return 1.0 - dot/denom, true
		case "ip", "inner_product":
			var dot float32
			for j := 0; j < mg.Dimensions; j++ {
				fbits := binary.LittleEndian.Uint32(mg.data[offset+int64(j*4):])
				bf := math.Float32frombits(fbits)
				dot += query[j] * bf
			}
			return -dot, true
		default: // L2
			var sum float32
			for j := 0; j < mg.Dimensions; j++ {
				fbits := binary.LittleEndian.Uint32(mg.data[offset+int64(j*4):])
				bf := math.Float32frombits(fbits)
				d := query[j] - bf
				sum += d * d
			}
			return sum, true
		}
	}
}
