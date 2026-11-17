package diskann

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Byte offsets of the header fields written by DiskIndex.WriteTo.
const (
	hdrNumNodes = 8
	hdrDims     = 16
	hdrR        = 20
	hdrMedoid   = 24
	hdrPQM      = 32
	hdrPQK      = 36
	hdrPQSubDim = 40
	hdrSize     = 44
)

// smallIndex builds a deterministic index small enough to be corrupted byte
// by byte: 12 nodes of 8 dimensions, 2 sub-quantizers of 4 centroids.
func smallIndex(t *testing.T) (*DiskIndex, []byte, [][]float32) {
	t.Helper()
	vecs := randomVectors(rand.New(rand.NewSource(7)), 12, 8)
	idx, err := BuildDiskIndex(vecs, DiskANNConfig{R: 4, L: 8, PQDim: 2, PQCentroids: 4})
	if err != nil {
		t.Fatalf("BuildDiskIndex: %v", err)
	}
	var buf bytes.Buffer
	n, err := idx.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != int64(buf.Len()) {
		t.Fatalf("WriteTo reported %d bytes, wrote %d", n, buf.Len())
	}
	return idx, buf.Bytes(), vecs
}

// failingWriter accepts limit bytes and then fails every write.
type failingWriter struct {
	limit, n int
}

var errDiskFull = errors.New("disk full")

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.limit {
		k := w.limit - w.n
		w.n = w.limit
		return k, errDiskFull
	}
	w.n += len(p)
	return len(p), nil
}

func TestDiskIndexWriteToReportsWriterFailureAtEveryOffset(t *testing.T) {
	idx, full, _ := smallIndex(t)
	for limit := 0; limit < len(full); limit++ {
		w := &failingWriter{limit: limit}
		n, err := idx.WriteTo(w)
		if !errors.Is(err, errDiskFull) {
			t.Fatalf("limit %d: err = %v, want the writer's error", limit, err)
		}
		if n > int64(limit) {
			t.Fatalf("limit %d: WriteTo claims %d bytes written", limit, n)
		}
	}
}

func TestReadDiskIndexRejectsEveryTruncation(t *testing.T) {
	_, full, _ := smallIndex(t)
	for l := 0; l < len(full); l++ {
		if _, err := ReadDiskIndex(bytes.NewReader(full[:l])); err == nil {
			t.Fatalf("ReadDiskIndex accepted a file truncated to %d of %d bytes", l, len(full))
		}
	}
	idx, err := ReadDiskIndex(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("ReadDiskIndex(full): %v", err)
	}
	if idx.NumNodes != 12 || idx.Dims != 8 || idx.PQM != 2 {
		t.Fatalf("header = %d nodes %d dims %d pqM", idx.NumNodes, idx.Dims, idx.PQM)
	}
}

func TestReadDiskIndexRejectsCorruptHeader(t *testing.T) {
	_, full, _ := smallIndex(t)
	numNodes := int64(binary.LittleEndian.Uint64(full[hdrNumNodes:]))
	offsetsEnd := hdrSize + int((numNodes+1)*8)

	put32 := func(off int, v int32) func([]byte) {
		return func(b []byte) { binary.LittleEndian.PutUint32(b[off:], uint32(v)) }
	}
	put64 := func(off int, v int64) func([]byte) {
		return func(b []byte) { binary.LittleEndian.PutUint64(b[off:], uint64(v)) }
	}
	cases := []struct {
		name    string
		corrupt func([]byte)
		want    string
	}{
		{"version", put32(4, 99), "unsupported version"},
		{"negative node count", put64(hdrNumNodes, -1), "invalid numNodes"},
		{"zero dims", put32(hdrDims, 0), "invalid dims"},
		{"huge dims", put32(hdrDims, 1<<20), "invalid dims"},
		{"medoid past the end", put64(hdrMedoid, numNodes), "invalid medoid"},
		{"negative medoid", put64(hdrMedoid, -3), "invalid medoid"},
		{"zero sub-quantizers", put32(hdrPQM, 0), "invalid pq layout"},
		{"negative sub-quantizers", put32(hdrPQM, -2), "invalid pq layout"},
		{"sub-vectors do not cover dims", put32(hdrPQSubDim, 3), "invalid pq layout"},
		{"no centroids", put32(hdrPQK, 0), "invalid pq centroid count"},
		{"too many centroids for uint8 codes", put32(hdrPQK, 300), "invalid pq centroid count"},
		{"first offset not zero", put64(hdrSize, 1), "invalid first neighbor offset"},
		{"decreasing offsets", put64(hdrSize+8, -1), "invalid neighbor offset"},
		{"edge count above 2R per node", put64(offsetsEnd-8, numNodes*4*2+1), "invalid total edges"},
		{"zero R", put32(hdrR, 0), "invalid total edges"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := append([]byte(nil), full...)
			tc.corrupt(b)
			_, err := ReadDiskIndex(bytes.NewReader(b))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadDiskIndexRejectsOutOfRangePQCode(t *testing.T) {
	idx, full, _ := smallIndex(t)
	// The PQ codes are followed by the raw embeddings (N*dims float32).
	codesEnd := len(full) - int(idx.NumNodes)*idx.Dims*4
	b := append([]byte(nil), full...)
	b[codesEnd-1] = byte(idx.PQK) // one past the last centroid
	_, err := ReadDiskIndex(bytes.NewReader(b))
	if err == nil || !strings.Contains(err.Error(), "invalid pq code") {
		t.Fatalf("err = %v, want an invalid pq code error", err)
	}
}

// A corrupted index must fail to load, never panic in a later search: flip
// every byte of the header and topology in turn and search what still loads.
func TestCorruptIndexNeverPanics(t *testing.T) {
	idx, full, vecs := smallIndex(t)
	topoEnd := len(full) - int(idx.NumNodes)*idx.Dims*4
	for i := 8; i < topoEnd; i++ { // past magic and version
		b := append([]byte(nil), full...)
		b[i] ^= 0xA5
		got, err := ReadDiskIndex(bytes.NewReader(b))
		if err != nil {
			continue
		}
		SearchExact(got, vecs[0], 3, 8, L2DistanceSquared)
	}
}

// Counts read from the file must not size an allocation before the data is
// there: a header that claims 2^31-1 nodes, or an edge count that only a
// bogus R allows, used to allocate tens of gigabytes before failing on the
// short input.
func TestReadDiskIndexHugeCountsFailWithoutHugeAllocation(t *testing.T) {
	_, full, _ := smallIndex(t)
	numNodes := int64(binary.LittleEndian.Uint64(full[hdrNumNodes:]))
	offsetsEnd := hdrSize + int((numNodes+1)*8)
	cases := map[string]func([]byte){
		"node count": func(b []byte) { binary.LittleEndian.PutUint64(b[hdrNumNodes:], 1<<31-1) },
		"edge count": func(b []byte) {
			binary.LittleEndian.PutUint32(b[hdrR:], 1<<30)
			binary.LittleEndian.PutUint64(b[offsetsEnd-8:], 1<<33)
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			b := append([]byte(nil), full...)
			corrupt(b)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := ReadDiskIndex(bytes.NewReader(b))
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatal("loaded an index whose counts exceed the file")
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
				t.Fatalf("allocated %d MB before rejecting the file", grew>>20)
			}
		})
	}
}

func TestOpenDiskIndexMmapErrors(t *testing.T) {
	idx, full, _ := smallIndex(t)
	dir := t.TempDir()

	short := filepath.Join(dir, "short.index")
	// Topology complete, embeddings cut in half.
	cut := len(full) - int(idx.NumNodes)*idx.Dims*2
	if err := os.WriteFile(short, full[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDiskIndexMmap(short); err == nil || !strings.Contains(err.Error(), "too small") {
		t.Fatalf("truncated embeddings: err = %v", err)
	}

	empty := filepath.Join(dir, "empty.index")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDiskIndexMmap(empty); err == nil {
		t.Fatal("empty file opened without error")
	}

	if _, err := OpenDiskIndexMmap(dir); err == nil {
		t.Fatal("a directory opened as an index")
	}

	s := &Searcher{config: DefaultConfig()}
	if err := s.LoadFromFile(context.Background(), short); err == nil {
		t.Fatal("Searcher.LoadFromFile accepted a truncated file")
	}
	if err := s.Load(context.Background(), full[:hdrSize], IndexMeta{}); err == nil {
		t.Fatal("Searcher.Load accepted a truncated index")
	}
}

func TestMmapVectorsMatchInMemory(t *testing.T) {
	_, full, vecs := smallIndex(t)
	path := filepath.Join(t.TempDir(), "x.index")
	if err := os.WriteFile(path, full, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := OpenDiskIndexMmap(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range vecs {
		got := m.GetVector(int64(i))
		if len(got) != len(want) {
			t.Fatalf("node %d: %d dims", i, len(got))
		}
		for d := range want {
			if got[d] != want[d] {
				t.Fatalf("node %d dim %d: %v != %v", i, d, got[d], want[d])
			}
		}
	}
	for _, id := range []int64{-1, int64(len(vecs))} {
		if v := m.GetVector(id); v != nil {
			t.Fatalf("GetVector(%d) = %v, want nil", id, v)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if v := m.GetVector(0); v != nil {
		t.Fatal("GetVector after Close returned data")
	}
	var nilIdx *DiskIndex
	if nilIdx.GetVector(0) != nil || nilIdx.Close() != nil {
		t.Fatal("nil index must be inert")
	}
}

func TestSearcherReloadAndExtract(t *testing.T) {
	ctx := context.Background()
	_, full, vecs := smallIndex(t)
	path := filepath.Join(t.TempDir(), "x.index")
	if err := os.WriteFile(path, full, 0o644); err != nil {
		t.Fatal(err)
	}

	s := (&Factory{}).NewSearcher(DefaultConfig()).(*Searcher)
	if _, err := s.ExtractVectors(ctx, []int64{0}); err == nil {
		t.Fatal("ExtractVectors without an index succeeded")
	}
	if _, _, err := s.SearchWithRecompute(ctx, vecs[0], 1, nil); err == nil {
		t.Fatal("SearchWithRecompute without an index succeeded")
	}

	// Load into RAM, then replace with the mmapped file and back: each load
	// releases the previous index.
	if err := s.Load(ctx, full, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadFromFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(ctx, full, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadFromFile(ctx, path); err != nil {
		t.Fatal(err)
	}

	got, err := s.ExtractVectors(ctx, []int64{3, 0})
	if err != nil {
		t.Fatal(err)
	}
	if got[0][0] != vecs[3][0] || got[1][7] != vecs[0][7] {
		t.Fatalf("ExtractVectors returned other vectors")
	}
	// The returned vectors are copies, not views of the index.
	got[0][0] = -42
	if again, _ := s.ExtractVectors(ctx, []int64{3}); again[0][0] == -42 {
		t.Fatal("ExtractVectors returned a view of the index")
	}
	if _, err := s.ExtractVectors(ctx, []int64{0, 99}); err == nil || !strings.Contains(err.Error(), "node 99") {
		t.Fatalf("ExtractVectors of a missing node: err = %v", err)
	}

	if _, _, err := s.Search(ctx, []float32{1, 2}, 3); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("Search with a 2-dim query: err = %v", err)
	}
	if _, _, err := s.SearchWithRecompute(ctx, []float32{1, 2}, 3, nil); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("SearchWithRecompute with a 2-dim query: err = %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// SearchWithRecompute fetches a vector the index does not hold through the
// callback and reports the callback's failure instead of a degraded result.
func TestSearchWithRecomputeFallsBackToCallback(t *testing.T) {
	ctx := context.Background()
	idx, _, vecs := smallIndex(t)
	idx.Embeddings = nil // only the topology and PQ codes stay in RAM

	cfg := DefaultConfig()
	cfg.DiskANNConfig.SearchL = 0 // default beam width
	s := &Searcher{config: cfg, idx: idx}

	var asked int
	ids, dists, err := s.SearchWithRecompute(ctx, vecs[5], 1, func(_ context.Context, ids []int64) ([][]float32, error) {
		asked += len(ids)
		out := make([][]float32, len(ids))
		for i, id := range ids {
			out[i] = vecs[id]
		}
		return out, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked == 0 {
		t.Fatal("the callback was never asked for a vector")
	}
	if len(ids) != 1 || ids[0] != 5 || dists[0] != 0 {
		t.Fatalf("nearest = %v %v, want node 5 at distance 0", ids, dists)
	}

	boom := errors.New("embedder down")
	_, _, err = s.SearchWithRecompute(ctx, vecs[5], 1, func(context.Context, []int64) ([][]float32, error) {
		return nil, boom
	})
	if err == nil || !strings.Contains(err.Error(), "embedder down") {
		t.Fatalf("err = %v, want the embedder's error", err)
	}
	_, _, err = s.SearchWithRecompute(ctx, vecs[5], 1, func(context.Context, []int64) ([][]float32, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("an embedder that returns no vector must fail the search")
	}
}

func TestBuilderErrors(t *testing.T) {
	ctx := context.Background()
	b := (&Factory{}).NewBuilder(DefaultConfig())
	if _, err := b.Build(ctx, nil); err == nil {
		t.Fatal("Build of no embeddings succeeded")
	}
	// 9 dimensions cannot be split into the default 2 sub-quantizers.
	odd := randomVectors(rand.New(rand.NewSource(1)), 5, 9)
	if _, err := b.Build(ctx, odd); err == nil || !strings.Contains(err.Error(), "divisible") {
		t.Fatalf("Build of 9-dim vectors: err = %v", err)
	}
	if _, err := b.AddVectors(ctx, []byte("not an index"), odd, 0); err == nil || !strings.Contains(err.Error(), "load index") {
		t.Fatalf("AddVectors on garbage: err = %v", err)
	}
	if _, err := BuildDiskIndex(nil, DiskANNConfig{}); err == nil {
		t.Fatal("BuildDiskIndex of no embeddings succeeded")
	}
}

// Appending vectors keeps the old ones under their IDs and makes the new
// ones searchable.
func TestAddVectorsKeepsIDsAndFindsNewVectors(t *testing.T) {
	ctx := context.Background()
	cfg := Config{DiskANNConfig: DiskANNConfig{R: 8, L: 16, PQDim: 2, PQCentroids: 8}}
	b := &Builder{config: cfg}
	rng := rand.New(rand.NewSource(3))
	first := randomVectors(rng, 20, 8)
	more := randomVectors(rng, 5, 8)
	data, err := b.Build(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	data, err = b.AddVectors(ctx, data, more, 20)
	if err != nil {
		t.Fatal(err)
	}
	s := &Searcher{config: cfg}
	if err := s.Load(ctx, data, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, v := range more {
		ids, _, err := s.Search(ctx, v, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != int64(20+i) {
			t.Fatalf("new vector %d: nearest = %v, want %d", i, ids, 20+i)
		}
	}
	got, err := s.ExtractVectors(ctx, []int64{0})
	if err != nil || got[0][0] != first[0][0] {
		t.Fatalf("old vector 0 changed: %v %v", got, err)
	}
}

func TestDefaults(t *testing.T) {
	var c DiskANNConfig
	c.Defaults(2)
	if c.R != 64 || c.L != 100 || c.Alpha != 1.2 || c.PQDim != 1 || c.PQCentroids != 256 || c.SearchL != 100 {
		t.Fatalf("Defaults(2) = %+v", c)
	}
	c = DiskANNConfig{PQDim: 32, PQCentroids: 1000}
	c.Defaults(16)
	if c.PQDim != 16 {
		t.Fatalf("PQDim not clamped to dims: %d", c.PQDim)
	}
	if c.PQCentroids != 256 {
		t.Fatalf("PQCentroids not clamped to 256: %d", c.PQCentroids)
	}
	c = DiskANNConfig{R: 8, L: 20, Alpha: 1.5, PQDim: 4, PQCentroids: 16, SearchL: 30}
	want := c
	c.Defaults(16)
	if c != want {
		t.Fatalf("Defaults changed explicit values: %+v", c)
	}
}

func TestCosineDistanceProperties(t *testing.T) {
	// 11 dimensions exercise the 4-way unrolled loop and the tail.
	a := []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	scaled := make([]float32, len(a))
	opposite := make([]float32, len(a))
	for i, v := range a {
		scaled[i] = 3 * v
		opposite[i] = -v
	}
	if d := CosineDistance(a, scaled); d > 1e-6 || d < -1e-6 {
		t.Fatalf("parallel vectors: %v, want 0", d)
	}
	if d := CosineDistance(a, opposite); d < 2-1e-6 || d > 2+1e-6 {
		t.Fatalf("opposite vectors: %v, want 2", d)
	}
	zero := make([]float32, len(a))
	if d := CosineDistance(a, zero); d != 1 {
		t.Fatalf("zero vector: %v, want 1", d)
	}
	orth := []float32{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	orth2 := []float32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	if d := CosineDistance(orth, orth2); d != 1 {
		t.Fatalf("orthogonal vectors: %v, want 1", d)
	}
}

// With the cosine metric the index ranks by angle, not by length.
func TestCosineIndexRanksByAngle(t *testing.T) {
	ctx := context.Background()
	vecs := randomVectors(rand.New(rand.NewSource(11)), 30, 8)
	for i := range vecs {
		for d := range vecs[i] {
			vecs[i][d] -= 0.5
		}
	}
	cfg := Config{DiskANNConfig: DiskANNConfig{R: 8, L: 16, PQDim: 2, PQCentroids: 8, DistanceMetric: DistanceCosine}}
	data, err := (&Builder{config: cfg}).Build(ctx, vecs)
	if err != nil {
		t.Fatal(err)
	}
	s := &Searcher{config: cfg}
	if err := s.Load(ctx, data, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	q := make([]float32, 8)
	for d := range q {
		q[d] = 10 * vecs[17][d] // same direction, ten times longer
	}
	ids, dists, err := s.Search(ctx, q, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 17 || dists[0] > 1e-5 {
		t.Fatalf("nearest = %v %v, want 17 at ~0", ids, dists)
	}
}

func TestADCDistanceSortedOrdersByApproximateDistance(t *testing.T) {
	vecs := randomVectors(rand.New(rand.NewSource(5)), 40, 8)
	pq, err := TrainPQ(vecs, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	codes := pq.EncodeAll(vecs)
	table := pq.BuildDistanceTable(vecs[9], L2DistanceSquared)
	got := ADCDistanceSorted(table, []int64{0, 9, 20, 31}, codes)
	if len(got) != 4 {
		t.Fatalf("%d candidates", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Distance < got[i-1].Distance {
			t.Fatalf("not sorted: %+v", got)
		}
	}
	for _, c := range got {
		if want := ADCDistance(table, codes[c.ID]); c.Distance != want {
			t.Fatalf("candidate %d: %v, want %v", c.ID, c.Distance, want)
		}
	}
	// The query's own code is the one with the smallest table entries.
	if got[0].ID != 9 {
		t.Fatalf("closest = %d, want the query's own vector 9", got[0].ID)
	}
}

func TestVamanaGraphAccessors(t *testing.T) {
	g := NewVamanaGraph(0, 0, 4, 0, nil) // every parameter defaulted
	if g.r != 64 || g.l != 100 || g.alpha != 1.2 || g.distFunc == nil {
		t.Fatalf("defaults: r=%d l=%d alpha=%v", g.r, g.l, g.alpha)
	}
	vecs := randomVectors(rand.New(rand.NewSource(2)), 10, 4)
	g.Build(vecs)
	ids := g.AllNodeIDs()
	if len(ids) != 10 {
		t.Fatalf("AllNodeIDs = %v", ids)
	}
	for i, id := range ids {
		if id != int64(i) {
			t.Fatalf("AllNodeIDs not in insertion order: %v", ids)
		}
	}
	if v := g.GetVector(4); len(v) != 4 || v[0] != vecs[4][0] {
		t.Fatalf("GetVector(4) = %v", v)
	}
	for _, id := range []int64{-1, 10} {
		if g.GetVector(id) != nil || g.GetNeighbors(id) != nil {
			t.Fatalf("out-of-range id %d returned data", id)
		}
	}
}
