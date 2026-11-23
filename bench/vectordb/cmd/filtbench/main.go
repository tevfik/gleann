// Command filtbench measures gleann's REAL filtered search (pkg/gleann:
// widen-until-k plus the exact SQL pre-selection) against exact filtered
// ground truth on the vectordb benchmark dataset. Unlike the adapters in
// adapters/, it builds a format-2 index through gleann.NewBuilder and queries
// it through gleann.LeannSearcher.Search with metadata filters.
//
//	cd bench/vectordb
//	taskset -c 8-11 go run ./cmd/filtbench -size 10000 -dims 384 -dir /tmp/filt
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann/bench/vectordb"
	"github.com/tevfik/gleann/bench/vectordb/gleannsearch"
)

func main() {
	var (
		size    = flag.Int("size", 10000, "vectors")
		dims    = flag.Int("dims", 384, "dimensions")
		queries = flag.Int("queries", 1000, "queries")
		k       = flag.Int("k", 10, "top-k")
		dir     = flag.String("dir", "", "index directory (kept; an existing index is reused)")
		alpha   = flag.Float64("alpha", 1.0, "hybrid alpha (1 = vector only)")
		ef      = flag.Int("ef", 0, "search width: HNSW EfSearch or DiskANN SearchL (0 = default)")
		backend = flag.String("backend", "hnsw", "vector backend: hnsw or diskann")
		buildL  = flag.Int("build-l", 0, "DiskANN build list size (0 = default 100)")
		noMmap  = flag.Bool("no-mmap", false, "load the RAM searcher instead of the mmap one")
		only    = flag.String("only", "", "comma-separated rows to run: all,1%,10%,50% (default: every row)")
	)
	prof := flag.String("cpuprofile", "", "write a CPU profile of the query phase")
	vectors := flag.String("vectors", "", "real embedding vectors (.fvecs, see cmd/exportvecs) instead of synthetic data")
	flag.Parse()
	if *dir == "" {
		fatal("-dir is required")
	}
	ctx := context.Background()

	ds := vectordb.GenerateDataset(*size, *dims, *queries, 42)
	if *vectors != "" {
		vecs, err := vectordb.LoadFvecs(*vectors)
		if err != nil {
			fatal("-vectors: %v", err)
		}
		if ds, err = vectordb.DatasetFromVectors(vecs, *queries, 42, *vectors); err != nil {
			fatal("-vectors: %v", err)
		}
		*size, *dims = len(ds.IDs), ds.Dims
	}
	t0 := time.Now()
	s, built, err := gleannsearch.Open(ctx, *dir, ds, gleannsearch.Options{Backend: *backend, EfSearch: *ef, NoMmap: *noMmap, BuildL: *buildL})
	if err != nil {
		fatal("%v", err)
	}
	defer s.Close()
	if built {
		fmt.Fprintf(os.Stderr, "built %d in %s\n", *size, time.Since(t0).Round(time.Second))
	}

	truth, err := vectordb.LoadOrComputeGroundTruth(*dir+"-truth", ds, "cosine")
	if err != nil {
		fatal("ground truth: %v", err)
	}
	tests := []struct {
		name  string
		field string // empty: no filter
		gt    [][]int64
	}{{"all", "", truth.ExactTop100}, {"1%", "t1", truth.Filtered1Pct}, {"10%", "t10", truth.Filtered10Pct}, {"50%", "t50", truth.Filtered50Pct}}

	if *prof != "" {
		f, err := os.Create(*prof)
		if err != nil {
			fatal("cpuprofile: %v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fatal("cpuprofile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}
	fmt.Printf("backend=%s size=%d dims=%d queries=%d k=%d alpha=%.1f ef=%d\n", *backend, *size, *dims, *queries, *k, *alpha, *ef)
	fmt.Println("selectivity | recall@k | p50 ms | p95 ms | queries<k")
	for _, tc := range tests {
		if *only != "" && !strings.Contains(","+*only+",", ","+tc.name+",") {
			continue
		}
		var got [][]int64
		var lat []float64
		short := 0
		for i := range ds.Queries {
			t0 := time.Now()
			ids, err := gleannsearch.Query(ctx, s, i, *k, float32(*alpha), tc.field)
			lat = append(lat, float64(time.Since(t0).Microseconds())/1000)
			if err != nil {
				fatal("%v", err)
			}
			if len(ids) < *k {
				short++
			}
			got = append(got, ids)
		}
		sort.Float64s(lat)
		fmt.Printf("%-11s | %.4f | %.2f | %.2f | %d\n", tc.name, vectordb.AverageRecallAtK(tc.gt, got, *k),
			lat[len(lat)/2], lat[len(lat)*95/100], short)
	}
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "filtbench: "+f+"\n", a...)
	os.Exit(2)
}
