// Command buildbench times gleann's Builder (passage staging, then the HNSW
// vector build) with a lookup embedder, so no model is needed. Same harness on
// two checkouts compares their build cost.
//
//	cd bench/vectordb
//	taskset -c 16-19 go run ./cmd/buildbench -h
//
// With -queries N it also prints the sha256 of the built vector index and of
// the search results (IDs and distance bits) of N fixed queries at every -ef
// value, through the in-memory and the mmap searcher: two checkouts that print
// the same lines built the same index and search it the same way. Half of the
// queries lie in no cluster of the data, so that the narrow ef values miss
// neighbours and the lines of different ef values differ. -metric builds and
// searches with cosine or ip instead of the default l2.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/modules/hnsw"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

type lookup struct {
	dims int
	vecs map[string][]float32
}

func (e *lookup) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.vecs[t]
	}
	return out, nil
}
func (e *lookup) ComputeSingle(ctx context.Context, t string) ([]float32, error) {
	v, _ := e.Compute(ctx, []string{t})
	return v[0], nil
}
func (e *lookup) Dimensions() int   { return e.dims }
func (e *lookup) ModelName() string { return "lookup" }

func main() {
	n := flag.Int("n", 20000, "passages")
	dims := flag.Int("dims", 384, "dims")
	dir := flag.String("dir", "", "index dir")
	cpu := flag.String("cpuprofile", "", "cpu profile")
	mem := flag.String("memprofile", "", "alloc profile")
	backend := flag.String("backend", "hnsw", "vector backend: hnsw or diskann")
	queries := flag.Int("queries", 0, "hnsw only: print checksums of the index and of this many searches")
	efs := flag.String("ef", "10,64,200", "ef values of the -queries searches, comma separated")
	metric := flag.String("metric", "", "hnsw only: distance metric l2, cosine or ip (default: the index default)")
	flag.Parse()
	_ = os.RemoveAll(*dir)
	rng := rand.New(rand.NewSource(1))
	words := strings.Fields("func return error context string index passage vector search graph symbol call builder store query embed chunk file path name value type struct interface package import const var select range switch case default defer go chan map slice append len cap make new nil true false")
	centers := make([][]float32, 64)
	for i := range centers {
		centers[i] = rnd(rng, *dims)
	}
	emb := &lookup{dims: *dims, vecs: make(map[string][]float32, *n)}
	items := make([]gleann.Item, *n)
	for i := range items {
		var sb strings.Builder
		sb.WriteString("p" + strconv.Itoa(i) + " ")
		for w := 0; w < 120; w++ { // ~700 bytes
			sb.WriteString(words[rng.Intn(len(words))])
			sb.WriteByte(' ')
			if w%12 == 11 {
				sb.WriteByte('\n')
			}
		}
		t := sb.String()
		v := rnd(rng, *dims)
		c := centers[i%64]
		for j := range v {
			v[j] = c[j] + 0.5*v[j]
		}
		normalize(v)
		emb.vecs[t] = v
		items[i] = gleann.Item{Text: t, Metadata: map[string]any{"source": "/src/file" + strconv.Itoa(i/10) + ".go", "kind": "code", "line": i % 500}}
	}
	cfg := gleann.DefaultConfig()
	cfg.IndexDir, cfg.Backend = *dir, *backend
	if *metric != "" {
		cfg.HNSWConfig.DistanceMetric = gleann.DistanceMetric(*metric)
	}
	b, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		panic(err)
	}
	if *cpu != "" {
		f, _ := os.Create(*cpu)
		_ = pprof.StartCPUProfile(f)
	}
	t0 := time.Now()
	ids, err := b.BuildPassages("idx", items)
	if err != nil {
		panic(err)
	}
	t1 := time.Now()
	if err := b.BuildVectors(context.Background(), "idx", items, ids); err != nil {
		panic(err)
	}
	t2 := time.Now()
	if *cpu != "" {
		pprof.StopCPUProfile()
	}
	if *mem != "" {
		f, _ := os.Create(*mem)
		_ = pprof.Lookup("allocs").WriteTo(f, 0)
	}
	fmt.Printf("backend=%s n=%d passages=%s vectors=%s total=%s\n", *backend, *n, t1.Sub(t0).Round(time.Millisecond), t2.Sub(t1).Round(time.Millisecond), t2.Sub(t0).Round(time.Millisecond))
	if *queries > 0 && *backend == "hnsw" {
		if err := checksums(filepath.Join(*dir, "idx", "idx.index"), centers, *dims, *queries, *efs, *metric); err != nil {
			panic(err)
		}
	}
}

// checksums prints the sha256 of the index file and, per ef value, of the
// top-10 results of n queries, once through the in-memory searcher and once
// through the mmap searcher. Every other query is drawn like the passages (a
// centre plus noise); the ones between are random directions.
func checksums(path string, centers [][]float32, dims, n int, efs, metric string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading the index: %w", err)
	}
	fmt.Printf("index_sha256=%x bytes=%d\n", sha256.Sum256(data), len(data))
	rng := rand.New(rand.NewSource(2))
	qs := make([][]float32, n)
	for i := range qs {
		v := rnd(rng, dims)
		if c := centers[rng.Intn(len(centers))]; i%2 == 0 {
			for j := range v {
				v[j] = c[j] + 0.5*v[j]
			}
		}
		normalize(v)
		qs[i] = v
	}
	ctx := context.Background()
	for _, f := range strings.Split(efs, ",") {
		ef, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return fmt.Errorf("parsing -ef: %w", err)
		}
		cfg := hnsw.DefaultConfig()
		cfg.HNSWConfig.EfSearch = ef
		cfg.HNSWConfig.DistanceMetric = hnsw.DistanceMetric(metric)
		for _, mmap := range []bool{false, true} {
			cfg.HNSWConfig.UseMmap = mmap
			s := (&hnsw.Factory{}).NewSearcher(cfg)
			if ms, ok := s.(hnsw.MmapBackendSearcher); ok {
				err = ms.LoadFromFile(ctx, path)
			} else {
				err = s.Load(ctx, data, hnsw.IndexMeta{})
			}
			if err != nil {
				return fmt.Errorf("loading the index: %w", err)
			}
			h := sha256.New()
			hits := 0
			for _, q := range qs {
				ids, dists, err := s.Search(ctx, q, 10)
				if err != nil {
					return fmt.Errorf("searching: %w", err)
				}
				hits += len(ids)
				_ = binary.Write(h, binary.LittleEndian, ids)
				for _, d := range dists {
					_ = binary.Write(h, binary.LittleEndian, math.Float32bits(d))
				}
			}
			_ = s.Close()
			fmt.Printf("search_sha256 ef=%d mmap=%v queries=%d hits=%d %x\n", ef, mmap, n, hits, h.Sum(nil))
		}
	}
	return nil
}

func rnd(r *rand.Rand, d int) []float32 {
	v := make([]float32, d)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return v
}

func normalize(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x * x)
	}
	s = math.Sqrt(s)
	for i := range v {
		v[i] /= float32(s)
	}
}
