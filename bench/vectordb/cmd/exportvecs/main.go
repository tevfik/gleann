// Command exportvecs writes the stored embedding vectors of a gleann HNSW
// index to a .fvecs file, so the benchmark can run on real embeddings:
//
//	gleann index build myrepo --docs . --backend hnsw
//	cd bench/vectordb
//	go run ./cmd/exportvecs -index ~/.gleann/indexes/myrepo/myrepo.index -out /tmp/myrepo.fvecs
//	go run ./cmd/vectorbench -vectors /tmp/myrepo.fvecs
//
// The index must store its vectors (the default; not --storage recompute).
// Vectors are written in ascending ID order; float16 vectors are widened.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/tevfik/gleann/bench/vectordb"
	"github.com/tevfik/gleann/modules/hnsw"
)

func main() {
	index := flag.String("index", "", "path of the HNSW index file (<name>/<name>.index)")
	out := flag.String("out", "", "output .fvecs file")
	flag.Parse()
	if *index == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	vecs, err := readVectors(*index)
	if err != nil {
		fmt.Fprintf(os.Stderr, "exportvecs: %v\n", err)
		os.Exit(1)
	}
	if err := vectordb.SaveFvecs(*out, vecs); err != nil {
		fmt.Fprintf(os.Stderr, "exportvecs: writing %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d vectors of %d dimensions to %s\n", len(vecs), len(vecs[0]), *out)
}

func readVectors(path string) ([][]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening index: %w", err)
	}
	defer f.Close()
	g, err := hnsw.ReadCSR(f)
	if err != nil {
		return nil, fmt.Errorf("reading %s as an HNSW index: %w", path, err)
	}
	ids := make([]int64, 0, len(g.StoredEmbeddings)+len(g.StoredEmbeddingsF16))
	for id := range g.StoredEmbeddings {
		ids = append(ids, id)
	}
	for id := range g.StoredEmbeddingsF16 {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s stores no vectors (built with --storage recompute?)", path)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	vecs := make([][]float32, len(ids))
	for i, id := range ids {
		if v, ok := g.StoredEmbeddings[id]; ok {
			vecs[i] = v
		} else {
			vecs[i] = hnsw.Float16SliceToFloat32(g.StoredEmbeddingsF16[id])
		}
	}
	return vecs, nil
}
