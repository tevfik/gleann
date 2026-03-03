package hnsw

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// MmapGraph is a zero-copy, memory-mapped representation of the CSRGraph.
// It bypasses the Go Garbage Collector by keeping all node and edge data
// off-heap in a flat `[]byte` slice backed directly by a file on disk.
type MmapGraph struct {
	data     []byte
	file     *os.File
	fileSize int64

	// Extracted header info
	NumNodes   int64
	Dimensions int
	MaxLevel   int
	EntryPoint int64
	M          int
	NumStored  int64

	// Offsets into the memory-mapped byte slice
	offsetIDMap      int64
	offsetNodeLevels int64
	offsetLevels     int64
	offsetEmbeddings int64

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

	// Memory map the file (Read Only)
	data, err := mmapFile(f.Fd(), size)
	if err != nil {
		f.Close()
		return nil, err
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

func (mg *MmapGraph) parseHeaders() error {
	if len(mg.data) < 32 {
		return fmt.Errorf("file too small to contain header")
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
	if version != CSRVersion {
		return fmt.Errorf("unsupported version: %d", version)
	}

	mg.NumNodes = int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	mg.Dimensions = int(binary.LittleEndian.Uint32(mg.data[offset:]))
	offset += 4
	mg.MaxLevel = int(binary.LittleEndian.Uint32(mg.data[offset:]))
	offset += 4
	mg.EntryPoint = int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	mg.M = int(binary.LittleEndian.Uint32(mg.data[offset:]))
	offset += 4
	mg.NumStored = int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8

	// === ID Map ===
	numIDs := int64(binary.LittleEndian.Uint64(mg.data[offset:]))
	offset += 8
	mg.offsetIDMap = offset
	offset += numIDs * 8

	// === Node Levels ===
	mg.offsetNodeLevels = offset
	offset += numIDs * 4

	// === Per-level Data ===
	numLevels := int(binary.LittleEndian.Uint32(mg.data[offset:]))
	offset += 4

	mg.offsetLevels = offset
	mg.levels = make([]mmapLevel, numLevels)

	for l := 0; l < numLevels; l++ {
		nn := int64(binary.LittleEndian.Uint64(mg.data[offset:]))
		offset += 8

		levelInfo := mmapLevel{
			numNodes:    nn,
			offsetNodes: offset,
		}

		// Skip NodeOffsets array: (nn + 1) * 8 bytes
		offset += (nn + 1) * 8

		numNeighbors := int64(binary.LittleEndian.Uint64(mg.data[offset:]))
		offset += 8

		levelInfo.offsetEdges = offset
		levelInfo.numNeighbors = numNeighbors

		// Skip Neighbors array: numNeighbors * 8 bytes
		offset += numNeighbors * 8

		mg.levels[l] = levelInfo
	}

	// === Stored Embeddings ===
	mg.offsetEmbeddings = offset

	return nil
}

// Close unmaps the memory and closes the file.
func (mg *MmapGraph) Close() error {
	var err1, err2 error
	if mg.data != nil {
		err1 = munmapFile(mg.data)
		mg.data = nil
	}
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
	start := mg.offsetIDMap + (internalIdx * 8)
	return int64(binary.LittleEndian.Uint64(mg.data[start : start+8]))
}

// GetNodeLevel returns the maximum HNSW level for a given internal index.
func (mg *MmapGraph) GetNodeLevel(internalIdx int64) int {
	start := mg.offsetNodeLevels + (internalIdx * 4)
	return int(binary.LittleEndian.Uint32(mg.data[start : start+4]))
}

// GetNeighbors returns the internal neighbor indices for a node at a specific level.
// This is the core operation of graph traversal, completely GC-free.
func (mg *MmapGraph) GetNeighbors(internalIdx int64, level int) []int64 {
	if level >= len(mg.levels) {
		return nil
	}

	lvl := &mg.levels[level]
	if internalIdx >= lvl.numNodes {
		return nil
	}

	// 1. Read start and end offsets from NodeOffsets array
	offsetsStart := lvl.offsetNodes + (internalIdx * 8)
	edgeStartPos := int64(binary.LittleEndian.Uint64(mg.data[offsetsStart : offsetsStart+8]))
	edgeEndPos := int64(binary.LittleEndian.Uint64(mg.data[offsetsStart+8 : offsetsStart+16]))

	count := edgeEndPos - edgeStartPos
	if count <= 0 {
		return nil
	}

	// 2. Read the neighbor IDs directly
	neighbors := make([]int64, count)
	edgesByteStart := lvl.offsetEdges + (edgeStartPos * 8)

	for i := int64(0); i < count; i++ {
		bStart := edgesByteStart + (i * 8)
		neighbors[i] = int64(binary.LittleEndian.Uint64(mg.data[bStart : bStart+8]))
	}

	return neighbors
}

// GetStoredEmbedding tries to retrieve a pruned stored embedding for an internal index.
func (mg *MmapGraph) GetStoredEmbedding(externalID int64) []float32 {
	if mg.NumStored == 0 {
		return nil
	}

	// Simple linear scan over stored embeddings.
	// In a massive graph, a binary search or hash map approach right in the byte slice might be needed,
	// but strictly according to the current gleann CSR format, we iterate over them.
	offset := mg.offsetEmbeddings
	vecBytes := int64(mg.Dimensions * 4)

	for i := int64(0); i < mg.NumStored; i++ {
		id := int64(binary.LittleEndian.Uint64(mg.data[offset : offset+8]))
		offset += 8

		if id == externalID {
			vec := make([]float32, mg.Dimensions)
			for j := 0; j < mg.Dimensions; j++ {
				fbits := binary.LittleEndian.Uint32(mg.data[offset+(int64(j)*4):])
				vec[j] = math.Float32frombits(fbits)
			}
			return vec
		}
		offset += vecBytes
	}

	return nil
}
