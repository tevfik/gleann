package hnsw

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Byte offsets of the CSR header fields (csr.go WriteTo).
const (
	csrOffDims     = 16
	csrOffMaxLevel = 20
	csrOffEntry    = 24
	csrOffStored   = 36
	csrHeaderV1    = 44 // numIDs follows
	csrHeaderV2    = 48 // dtypeCode inserted before numIDs
)

// buildGraph inserts vecs under firstID, firstID+1, ... into an M=4 graph,
// small enough that it has several levels.
func buildGraph(vecs [][]float32, firstID int64, distFunc DistanceFunc) *Graph {
	g := NewGraphWithDistance(4, 40, len(vecs[0]), distFunc)
	for i, v := range vecs {
		g.Insert(firstID+int64(i), v)
	}
	return g
}

// serialize converts g to CSR with the given dtype, keeps keepFraction of the
// embeddings and returns the file bytes.
func serialize(t *testing.T, g *Graph, dtype string, keepFraction float64) []byte {
	t.Helper()
	csr := ConvertToCSR(g)
	csr.VectorDType = dtype
	PruneEmbeddings(g, csr, keepFraction)
	var buf bytes.Buffer
	n, err := csr.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != int64(buf.Len()) {
		t.Fatalf("WriteTo reported %d bytes, wrote %d", n, buf.Len())
	}
	return buf.Bytes()
}

type failingWriter struct{ limit, n int }

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

func TestCSRWriteToReportsWriterFailureAtEveryOffset(t *testing.T) {
	for _, dtype := range []string{VectorDTypeF32, VectorDTypeF16} {
		t.Run(dtype, func(t *testing.T) {
			full := serialize(t, buildGraph(randVecs(12, 4, 1), 0, nil), dtype, 1)
			csr, err := ReadCSR(bytes.NewReader(full))
			if err != nil {
				t.Fatal(err)
			}
			for limit := 0; limit < len(full); limit++ {
				n, err := csr.WriteTo(&failingWriter{limit: limit})
				if !errors.Is(err, errDiskFull) {
					t.Fatalf("limit %d: err = %v, want the writer's error", limit, err)
				}
				if n > int64(limit) {
					t.Fatalf("limit %d: WriteTo claims %d bytes", limit, n)
				}
			}
		})
	}
}

// Every truncation of a valid file must be rejected by both readers, and
// none may panic.
func TestTruncatedIndexIsRejected(t *testing.T) {
	for _, dtype := range []string{VectorDTypeF32, VectorDTypeF16} {
		t.Run(dtype, func(t *testing.T) {
			full := serialize(t, buildGraph(randVecs(12, 4, 2), 0, nil), dtype, 1)
			for l := 0; l < len(full); l++ {
				if _, err := ReadCSR(bytes.NewReader(full[:l])); err == nil {
					t.Fatalf("ReadCSR accepted %d of %d bytes", l, len(full))
				}
			}
			// The mmap reader tolerates a cut in the stored embeddings (it
			// indexes the complete ones) but not in the header or topology.
			mg, err := OpenMmapGraphFromData(full)
			if err != nil {
				t.Fatal(err)
			}
			topoEnd := mg.offsetEmbeddings
			for l := 0; l < int(topoEnd); l++ {
				if _, err := OpenMmapGraphFromData(full[:l]); err == nil {
					t.Fatalf("OpenMmapGraphFromData accepted %d of %d topology bytes", l, topoEnd)
				}
			}
			cut, err := OpenMmapGraphFromData(full[:len(full)-1])
			if err != nil {
				t.Fatalf("cut in the last embedding: %v", err)
			}
			if got, want := len(cut.storedOffsets), len(mg.storedOffsets)-1; got != want {
				t.Fatalf("%d embeddings indexed from a file cut in the last one, want %d", got, want)
			}
		})
	}
}

func TestReadCSRRejectsCorruptHeader(t *testing.T) {
	full := serialize(t, buildGraph(randVecs(12, 4, 3), 0, nil), VectorDTypeF32, 1)
	numIDs := int64(binary.LittleEndian.Uint64(full[csrHeaderV1:]))
	levelsOff := csrHeaderV1 + 8 + int(numIDs)*8
	numLevelsOff := levelsOff + int(numIDs)*4
	level0 := numLevelsOff + 4

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
		{"magic", put32(0, 0x12345678), "invalid magic"},
		{"version", put32(4, 7), "unsupported version"},
		{"negative dims", put32(csrOffDims, -1), "invalid dimensions"},
		{"negative stored count", put64(csrOffStored, -1), "invalid numStored"},
		{"negative id count", put64(csrHeaderV1, -1), "invalid numIDs"},
		{"entry point not a node", put64(csrOffEntry, 999), "entry point"},
		{"negative node level", put32(levelsOff, -1), "invalid level"},
		{"absurd node level", put32(levelsOff, 1000), "invalid level"},
		{"max level without stored level", put32(csrOffMaxLevel, 40), "max level"},
		{"negative level count", put32(numLevelsOff, -1), "invalid numLevels"},
		{"too many levels", put32(numLevelsOff, 1000), "invalid numLevels"},
		{"level with more nodes than the graph", put64(level0, numIDs+1), "numNodes"},
		{"negative level node count", put64(level0, -1), "numNodes"},
		{"first offset not zero", put64(level0+8, 5), "node offsets"},
		{"decreasing offsets", put64(level0+16, -4), "node offsets"},
		{"negative neighbor count", put64(level0+8+int(numIDs+1)*8, -1), "numNeighbors"},
		{"offsets past the neighbors", put64(level0+8+int(numIDs+1)*8, 0), "node offsets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := append([]byte(nil), full...)
			tc.corrupt(b)
			_, err := ReadCSR(bytes.NewReader(b))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A count far beyond the input fails on the short input; it must not be
// allocated up front.
func TestReadCSRHugeCountFailsWithoutAllocating(t *testing.T) {
	full := serialize(t, buildGraph(randVecs(8, 4, 4), 0, nil), VectorDTypeF32, 1)
	b := append([]byte(nil), full...)
	binary.LittleEndian.PutUint64(b[csrHeaderV1:], 1<<40)
	// Allocating the 8 TiB up front would abort the test binary.
	if _, err := ReadCSR(bytes.NewReader(b)); err == nil || !strings.Contains(err.Error(), "read ids") {
		t.Fatalf("err = %v, want a short read of the ID map", err)
	}
}

// The mmap reader sized its stored-embedding map with the header's
// NumStored: one flipped high byte of that count allocated tens of
// gigabytes. A negative count is rejected; a count above what the file holds
// indexes the embeddings that are there (like a cut file) and allocates
// nothing for the rest.
func TestMmapStoredCountBeyondFile(t *testing.T) {
	full := serialize(t, buildGraph(randVecs(12, 4, 5), 0, nil), VectorDTypeF32, 1)
	mg, err := OpenMmapGraphFromData(full)
	if err != nil {
		t.Fatal(err)
	}
	want := len(mg.storedOffsets)

	b := append([]byte(nil), full...)
	binary.LittleEndian.PutUint64(b[csrOffStored:], ^uint64(0))
	if _, err := OpenMmapGraphFromData(b); err == nil || !strings.Contains(err.Error(), "invalid numStored") {
		t.Fatalf("negative NumStored: err = %v", err)
	}

	for _, n := range []int64{1 << 40, int64(want) + 1} {
		b := append([]byte(nil), full...)
		binary.LittleEndian.PutUint64(b[csrOffStored:], uint64(n))
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		got, err := OpenMmapGraphFromData(b)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatalf("NumStored %d: %v", n, err)
		}
		if len(got.storedOffsets) != want {
			t.Fatalf("NumStored %d: indexed %d embeddings, want %d", n, len(got.storedOffsets), want)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
			t.Fatalf("NumStored %d: allocated %d MB", n, grew>>20)
		}
	}
}

// Flip every header and topology byte in turn: what still loads must be
// searchable without a panic, in RAM and through the mmap reader.
func TestCorruptIndexNeverPanics(t *testing.T) {
	vecs := randVecs(12, 4, 5)
	full := serialize(t, buildGraph(vecs, 0, nil), VectorDTypeF32, 1)
	mg, err := OpenMmapGraphFromData(full)
	if err != nil {
		t.Fatal(err)
	}
	topoEnd := int(mg.offsetEmbeddings)
	ctx := context.Background()
	for i := 8; i < topoEnd; i++ {
		b := append([]byte(nil), full...)
		b[i] ^= 0x5A
		if csr, err := ReadCSR(bytes.NewReader(b)); err == nil {
			csr.ToGraph().Search(vecs[0], 3, 8)
		}
		s := &MmapSearcher{config: DefaultConfig()}
		if err := s.Load(ctx, b, IndexMeta{}); err == nil {
			_, _, _ = s.Search(ctx, vecs[0], 3)
		}
	}
}

func TestOpenMmapGraphErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenMmapGraph(filepath.Join(dir, "missing.csr")); err == nil {
		t.Fatal("opened a missing file")
	}
	if _, err := OpenMmapGraph(dir); err == nil {
		t.Fatal("mapped a directory")
	}
	empty := filepath.Join(dir, "empty.csr")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMmapGraph(empty); err == nil {
		t.Fatal("mapped an empty file")
	}
	junk := filepath.Join(dir, "junk.csr")
	if err := os.WriteFile(junk, bytes.Repeat([]byte{0xFF}, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMmapGraph(junk); err == nil || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("junk file: err = %v", err)
	}
	s := &MmapSearcher{}
	if err := s.LoadFromFile(context.Background(), junk); err == nil {
		t.Fatal("MmapSearcher.LoadFromFile accepted junk")
	}
	if err := s.Load(context.Background(), []byte("short"), IndexMeta{}); err == nil {
		t.Fatal("MmapSearcher.Load accepted junk")
	}
}

func TestMmapRejectsMaxLevelWithoutLevel(t *testing.T) {
	full := serialize(t, buildGraph(randVecs(12, 4, 6), 0, nil), VectorDTypeF32, 1)
	b := append([]byte(nil), full...)
	binary.LittleEndian.PutUint32(b[csrOffMaxLevel:], 40)
	if _, err := OpenMmapGraphFromData(b); err == nil || !strings.Contains(err.Error(), "max level") {
		t.Fatalf("err = %v, want a max level error", err)
	}
}

// The mmap reader must return, at every level, the same neighbor lists as
// the in-memory graph. Upper levels list only the nodes that reach them, and
// the lists hold external IDs: both used to be read as internal indices,
// which sent the upper-level descent of the mmap search through unrelated
// nodes.
func TestMmapNeighborsMatchGraphAtEveryLevel(t *testing.T) {
	for _, firstID := range []int64{0, 1000} {
		vecs := randVecs(300, 8, 7)
		g := buildGraph(vecs, firstID, nil)
		if g.MaxLevel() < 2 {
			t.Fatalf("graph has only %d levels; the test needs upper levels", g.MaxLevel()+1)
		}
		mg, err := OpenMmapGraphFromData(serialize(t, g, VectorDTypeF32, 1))
		if err != nil {
			t.Fatal(err)
		}
		toInt := func(ext int64) int64 { return mg.internalIndex(ext) }
		upper := 0
		for ext, n := range g.nodes.all() {
			idx := toInt(ext)
			if mg.GetExternalID(idx) != ext {
				t.Fatalf("firstID %d: node %d maps to index %d and back to %d", firstID, ext, idx, mg.GetExternalID(idx))
			}
			if mg.GetNodeLevel(idx) != n.Level {
				t.Fatalf("firstID %d: node %d level %d, mmap says %d", firstID, ext, n.Level, mg.GetNodeLevel(idx))
			}
			for l := 0; l <= n.Level; l++ {
				got := mg.GetNeighbors(idx, l)
				want := n.Neighbors[l]
				if len(got) != len(want) {
					t.Fatalf("firstID %d: node %d level %d: %d neighbors, want %d", firstID, ext, l, len(got), len(want))
				}
				for i := range want {
					if got[i] != toInt(want[i]) {
						t.Fatalf("firstID %d: node %d level %d: neighbors %v, want %v", firstID, ext, l, got, want)
					}
				}
				if l > 0 {
					upper++
				}
			}
			// A node has no list above its own level.
			if got := mg.GetNeighbors(idx, n.Level+1); got != nil {
				t.Fatalf("node %d has a list at level %d", ext, n.Level+1)
			}
		}
		if upper == 0 {
			t.Fatal("no upper-level list was compared")
		}
		for _, bad := range []int64{-1, int64(len(vecs))} {
			if mg.GetExternalID(bad) != -1 || mg.GetNodeLevel(bad) != -1 || mg.GetNeighbors(bad, 0) != nil {
				t.Fatalf("index %d out of range returned data", bad)
			}
		}
		if mg.GetNeighbors(0, -1) != nil || mg.GetNeighbors(0, 99) != nil {
			t.Fatal("an absent level returned data")
		}
		if mg.internalIndex(firstID+int64(len(vecs))) != -1 || mg.internalIndex(-5) != -1 {
			t.Fatal("an unknown external ID mapped to an index")
		}
	}
}

// With IDs that are not 0..n-1 (an index grown with AddVectors from a
// non-zero start), the mmap searcher must return the same external IDs as the
// in-memory searcher.
func TestMmapSearchWithOffsetIDsMatchesRAM(t *testing.T) {
	ctx := context.Background()
	vecs := randVecs(200, 8, 8)
	g := buildGraph(vecs, 5000, nil)
	data := serialize(t, g, VectorDTypeF32, 1)

	cfg := DefaultConfig()
	ram := &Searcher{config: cfg}
	if err := ram.Load(ctx, data, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	mm := &MmapSearcher{config: cfg}
	if err := mm.Load(ctx, data, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	defer mm.Close()
	for q := 0; q < 20; q++ {
		want, _, err := ram.Search(ctx, vecs[q*7], 5)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := mm.Search(ctx, vecs[q*7], 5)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != int64(5000+q*7) {
			t.Fatalf("query %d: nearest %d, want its own vector %d", q, got[0], 5000+q*7)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("query %d: mmap %v, RAM %v", q, got, want)
			}
		}
	}
}

func TestMmapCloseIsIdempotent(t *testing.T) {
	data := serialize(t, buildGraph(randVecs(10, 4, 9), 0, nil), VectorDTypeF32, 1)
	path := filepath.Join(t.TempDir(), "x.csr")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mg, err := OpenMmapGraph(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := mg.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mg.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// A graph over caller memory owns no mapping.
	fromData, err := OpenMmapGraphFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := fromData.Close(); err != nil {
		t.Fatal(err)
	}
	var s MmapSearcher
	if err := s.Close(); err != nil {
		t.Fatalf("Close of an empty searcher: %v", err)
	}
}
