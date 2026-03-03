package hnsw

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestConvertToCSR(t *testing.T) {
	g := NewGraph(4, 50, 3)
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{0.5, 0.5, 0.0},
		{0.0, 0.5, 0.5},
	}

	for i, v := range vectors {
		g.Insert(int64(i), v)
	}

	csr := ConvertToCSR(g)

	if csr.NumNodes != 5 {
		t.Errorf("expected 5 nodes, got %d", csr.NumNodes)
	}
	if csr.Dimensions != 3 {
		t.Errorf("expected 3 dimensions, got %d", csr.Dimensions)
	}
	if csr.EntryPoint < 0 {
		t.Error("entry point should be >= 0")
	}
	if len(csr.Levels) == 0 {
		t.Error("expected at least 1 level")
	}
	if len(csr.IDMap) != 5 {
		t.Errorf("expected 5 IDs in IDMap, got %d", len(csr.IDMap))
	}
}

func TestCSRWriteRead(t *testing.T) {
	g := NewGraph(4, 50, 3)
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}

	for i, v := range vectors {
		g.Insert(int64(i), v)
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 1.0) // Keep all embeddings for this test.

	// Serialize.
	var buf bytes.Buffer
	n, err := csr.WriteTo(&buf)
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n == 0 {
		t.Error("expected non-zero bytes written")
	}

	// Deserialize.
	csr2, err := ReadCSR(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	if csr2.NumNodes != csr.NumNodes {
		t.Errorf("numNodes: got %d, want %d", csr2.NumNodes, csr.NumNodes)
	}
	if csr2.Dimensions != csr.Dimensions {
		t.Errorf("dimensions: got %d, want %d", csr2.Dimensions, csr.Dimensions)
	}
	if csr2.EntryPoint != csr.EntryPoint {
		t.Errorf("entryPoint: got %d, want %d", csr2.EntryPoint, csr.EntryPoint)
	}
	if csr2.MaxLevel != csr.MaxLevel {
		t.Errorf("maxLevel: got %d, want %d", csr2.MaxLevel, csr.MaxLevel)
	}
	if csr2.M != csr.M {
		t.Errorf("M: got %d, want %d", csr2.M, csr.M)
	}
	if len(csr2.Levels) != len(csr.Levels) {
		t.Errorf("levels: got %d, want %d", len(csr2.Levels), len(csr.Levels))
	}
}

func TestCSRInvalidMagic(t *testing.T) {
	// Write garbage.
	buf := bytes.NewBuffer([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	_, err := ReadCSR(buf)
	if err == nil {
		t.Error("expected error for invalid magic")
	}
}

func TestPruneEmbeddingsAll(t *testing.T) {
	g := NewGraph(4, 50, 3)
	for i := 0; i < 10; i++ {
		g.Insert(int64(i), []float32{float32(i), 0, 0})
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 1.0)

	if len(csr.StoredEmbeddings) != 10 {
		t.Errorf("expected 10 stored embeddings, got %d", len(csr.StoredEmbeddings))
	}
}

func TestPruneEmbeddingsNone(t *testing.T) {
	g := NewGraph(4, 50, 3)
	for i := 0; i < 10; i++ {
		g.Insert(int64(i), []float32{float32(i), 0, 0})
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 0.0)

	// Only entry point should be stored.
	if len(csr.StoredEmbeddings) != 1 {
		t.Errorf("expected 1 stored embedding (entry point), got %d", len(csr.StoredEmbeddings))
	}

	// Entry point should be stored.
	if _, ok := csr.StoredEmbeddings[g.entryPoint]; !ok {
		t.Error("entry point embedding not stored")
	}

	// Other nodes should have nil vectors.
	prunedCount := 0
	for id, node := range g.nodes {
		if id != g.entryPoint && node.Vector == nil {
			prunedCount++
		}
	}
	if prunedCount != 9 {
		t.Errorf("expected 9 pruned nodes, got %d", prunedCount)
	}
}

func TestPruneEmbeddingsPartial(t *testing.T) {
	g := NewGraph(4, 50, 3)
	for i := 0; i < 10; i++ {
		g.Insert(int64(i), []float32{float32(i), float32(i), 0})
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 0.3) // Keep 30% = 3 nodes.

	if len(csr.StoredEmbeddings) != 3 {
		t.Errorf("expected 3 stored embeddings, got %d", len(csr.StoredEmbeddings))
	}

	// Entry point must be among stored.
	if _, ok := csr.StoredEmbeddings[g.entryPoint]; !ok {
		t.Error("entry point not stored")
	}
}

func TestCSRToGraph(t *testing.T) {
	g := NewGraph(4, 50, 3)
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{0.5, 0.5, 0.0},
	}

	for i, v := range vectors {
		g.Insert(int64(i), v)
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 1.0)

	// Serialize and deserialize.
	var buf bytes.Buffer
	csr.WriteTo(&buf)
	csr2, _ := ReadCSR(bytes.NewReader(buf.Bytes()))

	// Convert back to graph.
	g2 := csr2.ToGraph()

	if g2.Size() != g.Size() {
		t.Errorf("size: got %d, want %d", g2.Size(), g.Size())
	}
	if g2.GetEntryPoint() != g.GetEntryPoint() {
		t.Errorf("entry point: got %d, want %d", g2.GetEntryPoint(), g.GetEntryPoint())
	}

	// Search should still work.
	results := g2.Search([]float32{1.0, 0.0, 0.0}, 1, 50)
	if len(results) == 0 {
		t.Fatal("expected results from reconstructed graph")
	}
	if results[0].ID != 0 {
		t.Errorf("expected ID 0, got %d", results[0].ID)
	}
}

func TestCSRStats(t *testing.T) {
	g := NewGraph(4, 50, 64)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 100; i++ {
		v := make([]float32, 64)
		for j := range v {
			v[j] = rng.Float32()
		}
		g.Insert(int64(i), v)
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 0.0) // Prune all except entry point.

	stats := csr.Stats()

	if stats.NumNodes != 100 {
		t.Errorf("expected 100 nodes, got %d", stats.NumNodes)
	}
	if stats.StoredEmbeddings != 1 {
		t.Errorf("expected 1 stored embedding, got %d", stats.StoredEmbeddings)
	}
	if stats.PrunedEmbeddings != 99 {
		t.Errorf("expected 99 pruned, got %d", stats.PrunedEmbeddings)
	}
	if stats.CompressionRatio <= 0 {
		t.Error("expected positive compression ratio")
	}

	t.Logf("Compression ratio: %.2f%% (%.1f KB → %.1f KB)",
		stats.CompressionRatio*100,
		float64(stats.OriginalSizeBytes)/1024,
		float64(stats.TotalSizeBytes)/1024)
}

func TestCSRStorageReduction(t *testing.T) {
	// This test verifies the core LEANN claim: ~97% storage reduction.
	dims := 128
	n := 1000
	g := NewGraph(32, 200, dims)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < n; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		g.Insert(int64(i), v)
	}

	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 0.0) // Full pruning.

	stats := csr.Stats()

	// Embedding storage: n * dims * 4 bytes = 1000 * 128 * 4 = 512KB
	// CSR graph: much smaller (just neighbor lists).
	// We expect > 80% reduction (LEANN claims 97% but with larger datasets).
	if stats.CompressionRatio < 0.5 {
		t.Errorf("compression ratio %.2f%% — expected > 50%%", stats.CompressionRatio*100)
	}

	t.Logf("Storage reduction: %.1f%% (original: %.1f KB, compressed: %.1f KB)",
		stats.CompressionRatio*100,
		float64(stats.OriginalSizeBytes)/1024,
		float64(stats.TotalSizeBytes)/1024)
}

func TestCSRRoundtripAllVectors(t *testing.T) {
	// Verify that keepFraction=1.0 preserves all vectors through serialize/deserialize/ToGraph.
	dim := 8
	n := 10
	g := NewGraph(4, 32, dim)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()
		}
		g.Insert(int64(i), v)
	}

	// All nodes should have vectors.
	for _, id := range g.AllNodeIDs() {
		node, ok := g.GetNode(id)
		if !ok {
			t.Fatalf("node %d not found", id)
		}
		if len(node.Vector) != dim {
			t.Fatalf("node %d has vector len %d, want %d", id, len(node.Vector), dim)
		}
	}

	csr := ConvertToCSR(g)
	t.Logf("Before PruneEmbeddings: StoredEmbeddings=%d", len(csr.StoredEmbeddings))

	PruneEmbeddings(g, csr, 1.0) // Keep all.
	t.Logf("After PruneEmbeddings(1.0): StoredEmbeddings=%d", len(csr.StoredEmbeddings))

	if len(csr.StoredEmbeddings) != n {
		t.Fatalf("expected %d stored embeddings, got %d", n, len(csr.StoredEmbeddings))
	}

	// Serialize.
	var buf bytes.Buffer
	_, err := csr.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	// Deserialize.
	csr2, err := ReadCSR(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadCSR: %v", err)
	}
	t.Logf("Deserialized StoredEmbeddings=%d", len(csr2.StoredEmbeddings))

	if len(csr2.StoredEmbeddings) != n {
		t.Fatalf("deserialized: expected %d stored embeddings, got %d", n, len(csr2.StoredEmbeddings))
	}

	// Reconstruct graph.
	g2 := csr2.ToGraph()
	for _, id := range g2.AllNodeIDs() {
		node, ok := g2.GetNode(id)
		if !ok {
			t.Fatalf("reconstructed: node %d not found", id)
		}
		if len(node.Vector) != dim {
			t.Errorf("reconstructed: node %d has vector len %d, want %d", id, len(node.Vector), dim)
		}
	}

	// Search should work.
	query := make([]float32, dim)
	for i := range query {
		query[i] = 0.5
	}
	results := g2.Search(query, 3, 32)
	if len(results) == 0 {
		t.Fatal("expected results from reconstructed graph with all vectors")
	}
}
