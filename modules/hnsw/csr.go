package hnsw

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
)

// CSRGraph is a Compressed Sparse Row representation of an HNSW graph.
// This is the compact format used for storage — embeddings are pruned
// and recomputed on-demand during search.
//
// Format mirrors Python LEANN's convert_to_csr:
//
//	Header: magic, version, numNodes, dimensions, maxLevel, entryPoint, M
//	Per-level:
//	  LevelPtr[level]   -> offset into NodeOffsets
//	  NodeOffsets[node]  -> offset into NeighborsData
//	  NeighborsData      -> flat list of neighbor IDs
//
// Optional: embeddings for a fraction of nodes (entry point + high-degree nodes).
type CSRGraph struct {
	// Header fields
	NumNodes   int64
	Dimensions int
	MaxLevel   int
	EntryPoint int64
	M          int

	// Per-level CSR structure
	Levels []CSRLevel

	// Optional stored embeddings (for high-degree nodes kept during pruning).
	// Map from node ID to embedding vector.
	StoredEmbeddings map[int64][]float32

	// Node-to-internal ID mapping.
	IDMap []int64 // internal index -> external ID

	// Per-node level in the original HNSW graph.
	// NodeLevels[i] is the level of IDMap[i].
	NodeLevels []int
}

// CSRLevel represents one level of the HNSW graph in CSR format.
type CSRLevel struct {
	// NodeOffsets[i] gives the start index in Neighbors for node i.
	// NodeOffsets[i+1] - NodeOffsets[i] gives the number of neighbors.
	NodeOffsets []int64

	// Neighbors is a flat array of neighbor IDs.
	Neighbors []int64

	// NumNodes at this level.
	NumNodes int
}

// CSRMagic identifies a gleann CSR file.
const CSRMagic uint32 = 0x474C454E // "GLEN"

// CSRVersion is the current format version.
const CSRVersion uint32 = 1

// ConvertToCSR converts an HNSW graph to CSR format.
func ConvertToCSR(g *Graph) *CSRGraph {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Build ID map (sorted for deterministic output).
	idMap := g.AllNodeIDs()
	sort.Slice(idMap, func(i, j int) bool { return idMap[i] < idMap[j] })

	// Reverse map: external ID -> internal index.
	extToInt := make(map[int64]int, len(idMap))
	for i, id := range idMap {
		extToInt[id] = i
	}

	numNodes := int64(len(idMap))
	csr := &CSRGraph{
		NumNodes:         numNodes,
		Dimensions:       g.dimensions,
		MaxLevel:         g.maxLevel,
		EntryPoint:       g.entryPoint,
		M:                g.m,
		Levels:           make([]CSRLevel, g.maxLevel+1),
		StoredEmbeddings: make(map[int64][]float32),
		IDMap:            idMap,
		NodeLevels:       make([]int, len(idMap)),
	}

	// Record per-node levels.
	for i, id := range idMap {
		csr.NodeLevels[i] = g.nodes[id].Level
	}

	// Convert each level.
	for level := 0; level <= g.maxLevel; level++ {
		// Collect nodes that exist at this level.
		var levelNodeIDs []int64
		for _, id := range idMap {
			node := g.nodes[id]
			if node.Level >= level {
				levelNodeIDs = append(levelNodeIDs, id)
			}
		}

		csrLevel := CSRLevel{
			NumNodes:    len(levelNodeIDs),
			NodeOffsets: make([]int64, len(levelNodeIDs)+1),
		}

		offset := int64(0)
		for i, id := range levelNodeIDs {
			csrLevel.NodeOffsets[i] = offset
			node := g.nodes[id]
			if level < len(node.Neighbors) {
				neighbors := node.Neighbors[level]
				csrLevel.Neighbors = append(csrLevel.Neighbors, neighbors...)
				offset += int64(len(neighbors))
			}
		}
		csrLevel.NodeOffsets[len(levelNodeIDs)] = offset

		csr.Levels[level] = csrLevel
	}

	return csr
}

// PruneEmbeddings removes embeddings from the graph, keeping only a
// specified fraction. Always keeps the entry point and high-degree nodes.
// This is LEANN's core storage optimization.
//
// keepFraction: 0.0 = keep none (except entry point), 1.0 = keep all.
func PruneEmbeddings(g *Graph, csr *CSRGraph, keepFraction float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if keepFraction >= 1.0 {
		// Keep all embeddings — store them all.
		for id, node := range g.nodes {
			if len(node.Vector) > 0 {
				csr.StoredEmbeddings[id] = node.Vector
			}
		}
		return
	}

	// Always keep entry point embedding.
	if epNode, ok := g.nodes[g.entryPoint]; ok && len(epNode.Vector) > 0 {
		csr.StoredEmbeddings[g.entryPoint] = epNode.Vector
	}

	if keepFraction <= 0 {
		// Just keep entry point, prune everything else.
		for id, node := range g.nodes {
			if id != g.entryPoint {
				node.Vector = nil
			}
		}
		return
	}

	// Keep high-degree nodes (most connected = most traversed during search).
	type nodeDegree struct {
		id     int64
		degree int
	}
	degrees := make([]nodeDegree, 0, len(g.nodes))
	for id, node := range g.nodes {
		deg := 0
		for _, neighbors := range node.Neighbors {
			deg += len(neighbors)
		}
		degrees = append(degrees, nodeDegree{id: id, degree: deg})
	}

	sort.Slice(degrees, func(i, j int) bool {
		return degrees[i].degree > degrees[j].degree
	})

	keepCount := int(math.Ceil(float64(len(g.nodes)) * keepFraction))
	if keepCount < 1 {
		keepCount = 1
	}

	keepSet := make(map[int64]bool)
	keepSet[g.entryPoint] = true
	for _, nd := range degrees {
		if len(keepSet) >= keepCount {
			break
		}
		keepSet[nd.id] = true
	}

	// Store kept embeddings and prune the rest.
	for id, node := range g.nodes {
		if keepSet[id] && len(node.Vector) > 0 {
			csr.StoredEmbeddings[id] = make([]float32, len(node.Vector))
			copy(csr.StoredEmbeddings[id], node.Vector)
		} else {
			node.Vector = nil
		}
	}
}

// WriteTo serializes the CSR graph to a writer.
func (csr *CSRGraph) WriteTo(w io.Writer) (int64, error) {
	var written int64

	// Helper for writing binary data.
	write := func(data any) error {
		err := binary.Write(w, binary.LittleEndian, data)
		if err != nil {
			return err
		}
		switch v := data.(type) {
		case uint32:
			written += 4
		case int64:
			written += 8
		case int32:
			written += 4
		case float32:
			written += 4
		case []byte:
			written += int64(len(v))
		}
		return nil
	}

	// === Header ===
	if err := write(CSRMagic); err != nil {
		return written, fmt.Errorf("write magic: %w", err)
	}
	if err := write(CSRVersion); err != nil {
		return written, fmt.Errorf("write version: %w", err)
	}
	if err := write(csr.NumNodes); err != nil {
		return written, fmt.Errorf("write numNodes: %w", err)
	}
	if err := write(int32(csr.Dimensions)); err != nil {
		return written, fmt.Errorf("write dimensions: %w", err)
	}
	if err := write(int32(csr.MaxLevel)); err != nil {
		return written, fmt.Errorf("write maxLevel: %w", err)
	}
	if err := write(csr.EntryPoint); err != nil {
		return written, fmt.Errorf("write entryPoint: %w", err)
	}
	if err := write(int32(csr.M)); err != nil {
		return written, fmt.Errorf("write M: %w", err)
	}

	// Number of stored embeddings.
	numStored := int64(len(csr.StoredEmbeddings))
	if err := write(numStored); err != nil {
		return written, fmt.Errorf("write numStored: %w", err)
	}

	// === ID Map ===
	numIDs := int64(len(csr.IDMap))
	if err := write(numIDs); err != nil {
		return written, fmt.Errorf("write numIDs: %w", err)
	}
	for _, id := range csr.IDMap {
		if err := write(id); err != nil {
			return written, fmt.Errorf("write id: %w", err)
		}
	}

	// === Node Levels ===
	for _, lvl := range csr.NodeLevels {
		if err := write(int32(lvl)); err != nil {
			return written, fmt.Errorf("write nodeLevel: %w", err)
		}
	}

	// === Per-level CSR data ===
	numLevels := int32(len(csr.Levels))
	if err := write(numLevels); err != nil {
		return written, fmt.Errorf("write numLevels: %w", err)
	}

	for _, level := range csr.Levels {
		// Number of nodes at this level.
		nn := int64(level.NumNodes)
		if err := write(nn); err != nil {
			return written, fmt.Errorf("write level numNodes: %w", err)
		}

		// Node offsets (numNodes + 1 entries).
		for _, off := range level.NodeOffsets {
			if err := write(off); err != nil {
				return written, fmt.Errorf("write nodeOffset: %w", err)
			}
		}

		// Neighbor count.
		numNeighbors := int64(len(level.Neighbors))
		if err := write(numNeighbors); err != nil {
			return written, fmt.Errorf("write numNeighbors: %w", err)
		}

		// Neighbors data.
		for _, nid := range level.Neighbors {
			if err := write(nid); err != nil {
				return written, fmt.Errorf("write neighbor: %w", err)
			}
		}
	}

	// === Stored Embeddings ===
	for id, vec := range csr.StoredEmbeddings {
		if err := write(id); err != nil {
			return written, fmt.Errorf("write embedding id: %w", err)
		}
		for _, v := range vec {
			if err := write(v); err != nil {
				return written, fmt.Errorf("write embedding value: %w", err)
			}
		}
	}

	return written, nil
}

// ReadCSR deserializes a CSR graph from a reader.
func ReadCSR(r io.Reader) (*CSRGraph, error) {
	read := func(data any) error {
		return binary.Read(r, binary.LittleEndian, data)
	}

	// === Header ===
	var magic uint32
	if err := read(&magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	if magic != CSRMagic {
		return nil, fmt.Errorf("invalid magic: 0x%X (expected 0x%X)", magic, CSRMagic)
	}

	var version uint32
	if err := read(&version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	if version != CSRVersion {
		return nil, fmt.Errorf("unsupported version: %d", version)
	}

	csr := &CSRGraph{
		StoredEmbeddings: make(map[int64][]float32),
	}

	if err := read(&csr.NumNodes); err != nil {
		return nil, fmt.Errorf("read numNodes: %w", err)
	}

	var dims int32
	if err := read(&dims); err != nil {
		return nil, fmt.Errorf("read dimensions: %w", err)
	}
	csr.Dimensions = int(dims)

	var maxLevel int32
	if err := read(&maxLevel); err != nil {
		return nil, fmt.Errorf("read maxLevel: %w", err)
	}
	csr.MaxLevel = int(maxLevel)

	if err := read(&csr.EntryPoint); err != nil {
		return nil, fmt.Errorf("read entryPoint: %w", err)
	}

	var m int32
	if err := read(&m); err != nil {
		return nil, fmt.Errorf("read M: %w", err)
	}
	csr.M = int(m)

	var numStored int64
	if err := read(&numStored); err != nil {
		return nil, fmt.Errorf("read numStored: %w", err)
	}

	// === ID Map ===
	var numIDs int64
	if err := read(&numIDs); err != nil {
		return nil, fmt.Errorf("read numIDs: %w", err)
	}
	csr.IDMap = make([]int64, numIDs)
	for i := range csr.IDMap {
		if err := read(&csr.IDMap[i]); err != nil {
			return nil, fmt.Errorf("read id[%d]: %w", i, err)
		}
	}

	// === Node Levels ===
	csr.NodeLevels = make([]int, numIDs)
	for i := range csr.NodeLevels {
		var lvl int32
		if err := read(&lvl); err != nil {
			return nil, fmt.Errorf("read nodeLevel[%d]: %w", i, err)
		}
		csr.NodeLevels[i] = int(lvl)
	}

	// === Per-level CSR data ===
	var numLevels int32
	if err := read(&numLevels); err != nil {
		return nil, fmt.Errorf("read numLevels: %w", err)
	}
	csr.Levels = make([]CSRLevel, numLevels)

	for l := int32(0); l < numLevels; l++ {
		var nn int64
		if err := read(&nn); err != nil {
			return nil, fmt.Errorf("read level[%d] numNodes: %w", l, err)
		}

		level := CSRLevel{
			NumNodes:    int(nn),
			NodeOffsets: make([]int64, nn+1),
		}

		for i := int64(0); i <= nn; i++ {
			if err := read(&level.NodeOffsets[i]); err != nil {
				return nil, fmt.Errorf("read level[%d] nodeOffset[%d]: %w", l, i, err)
			}
		}

		var numNeighbors int64
		if err := read(&numNeighbors); err != nil {
			return nil, fmt.Errorf("read level[%d] numNeighbors: %w", l, err)
		}

		level.Neighbors = make([]int64, numNeighbors)
		for i := int64(0); i < numNeighbors; i++ {
			if err := read(&level.Neighbors[i]); err != nil {
				return nil, fmt.Errorf("read level[%d] neighbor[%d]: %w", l, i, err)
			}
		}

		csr.Levels[l] = level
	}

	// === Stored Embeddings ===
	for i := int64(0); i < numStored; i++ {
		var id int64
		if err := read(&id); err != nil {
			return nil, fmt.Errorf("read embedding id[%d]: %w", i, err)
		}
		vec := make([]float32, csr.Dimensions)
		for j := range vec {
			if err := read(&vec[j]); err != nil {
				return nil, fmt.Errorf("read embedding[%d][%d]: %w", i, j, err)
			}
		}
		csr.StoredEmbeddings[id] = vec
	}

	return csr, nil
}

// ToGraph reconstructs an HNSW Graph from a CSR representation.
// Nodes that had their embeddings pruned will have nil Vector fields.
func (csr *CSRGraph) ToGraph() *Graph {
	return csr.ToGraphWithDistance(nil)
}

// ToGraphWithDistance reconstructs an HNSW Graph with a custom distance function.
func (csr *CSRGraph) ToGraphWithDistance(distFunc DistanceFunc) *Graph {
	g := NewGraphWithOptions(csr.M, 200, csr.Dimensions, distFunc, true)
	g.entryPoint = csr.EntryPoint
	g.maxLevel = csr.MaxLevel
	g.nodeCount = csr.NumNodes

	// Build nodes with stored embeddings and correct levels.
	for i, id := range csr.IDMap {
		lvl := 0
		if i < len(csr.NodeLevels) {
			lvl = csr.NodeLevels[i]
		}
		node := &Node{
			ID:        id,
			Level:     lvl,
			Neighbors: make([][]int64, lvl+1),
		}
		for j := range node.Neighbors {
			node.Neighbors[j] = nil
		}
		if vec, ok := csr.StoredEmbeddings[id]; ok {
			node.Vector = vec
		}
		g.nodes[id] = node
	}

	// Reconstruct neighbor lists from CSR levels.
	for level, csrLevel := range csr.Levels {
		// Collect nodes at this level (those with Level >= level).
		nodesAtLevel := make([]int64, 0, csrLevel.NumNodes)
		for _, id := range csr.IDMap {
			node := g.nodes[id]
			if node.Level >= level {
				nodesAtLevel = append(nodesAtLevel, id)
			}
			if len(nodesAtLevel) >= csrLevel.NumNodes {
				break
			}
		}

		for i, id := range nodesAtLevel {
			node := g.nodes[id]
			// Ensure node has enough neighbor levels.
			for len(node.Neighbors) <= level {
				node.Neighbors = append(node.Neighbors, nil)
			}

			start := csrLevel.NodeOffsets[i]
			end := csrLevel.NodeOffsets[i+1]
			neighbors := make([]int64, end-start)
			copy(neighbors, csrLevel.Neighbors[start:end])
			node.Neighbors[level] = neighbors
		}
	}

	return g
}

// StorageStats returns statistics about CSR storage.
type StorageStats struct {
	NumNodes           int64
	NumLevels          int
	TotalEdges         int64
	StoredEmbeddings   int
	PrunedEmbeddings   int64
	GraphSizeBytes     int64
	EmbeddingSizeBytes int64
	TotalSizeBytes     int64
	CompressionRatio   float64
	OriginalSizeBytes  int64
}

// Stats computes storage statistics for the CSR graph.
func (csr *CSRGraph) Stats() StorageStats {
	stats := StorageStats{
		NumNodes:         csr.NumNodes,
		NumLevels:        len(csr.Levels),
		StoredEmbeddings: len(csr.StoredEmbeddings),
		PrunedEmbeddings: csr.NumNodes - int64(len(csr.StoredEmbeddings)),
	}

	// Count total edges.
	for _, level := range csr.Levels {
		stats.TotalEdges += int64(len(level.Neighbors))
	}

	// Estimate sizes in bytes.
	headerSize := int64(4 + 4 + 8 + 4 + 4 + 8 + 4 + 8 + 8) // header fields
	idMapSize := int64(len(csr.IDMap)) * 8

	graphSize := int64(0)
	for _, level := range csr.Levels {
		graphSize += 8                                 // numNodes
		graphSize += int64(len(level.NodeOffsets)) * 8 // offsets
		graphSize += 8                                 // numNeighbors
		graphSize += int64(len(level.Neighbors)) * 8   // neighbors
	}
	stats.GraphSizeBytes = headerSize + idMapSize + graphSize

	// Embedding storage.
	for _, vec := range csr.StoredEmbeddings {
		stats.EmbeddingSizeBytes += int64(len(vec)) * 4 // float32
		stats.EmbeddingSizeBytes += 8                   // id
	}

	stats.TotalSizeBytes = stats.GraphSizeBytes + stats.EmbeddingSizeBytes

	// Original size (all embeddings stored).
	stats.OriginalSizeBytes = stats.GraphSizeBytes + csr.NumNodes*int64(csr.Dimensions)*4
	if stats.OriginalSizeBytes > 0 {
		stats.CompressionRatio = 1.0 - float64(stats.TotalSizeBytes)/float64(stats.OriginalSizeBytes)
	}

	return stats
}
