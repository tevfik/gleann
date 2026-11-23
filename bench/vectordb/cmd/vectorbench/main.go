// Command vectorbench runs the vector database benchmark (package vectordb).
//
//	cd bench/vectordb
//	taskset -c 0-3 go run ./cmd/vectorbench -sizes 10000 -dims 384
//
// Candidate stores are compared against exact ground truth and side by side.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/tevfik/gleann/bench/vectordb"
)

func main() {
	var (
		stores   = flag.String("stores", "", "comma-separated stores to run (default: every registered one)")
		sizesStr = flag.String("sizes", "10000", "comma-separated vector sizes (e.g. 10000,100000)")
		dims     = flag.Int("dims", 384, "vector dimensions")
		queries  = flag.Int("queries", 1000, "number of queries")
		k        = flag.Int("k", 10, "top-k nearest neighbors")
		results  = flag.String("results", "results", "directory for the JSON and markdown report")
		workdir  = flag.String("workdir", "", "parent directory of the scratch databases (default: system temp)")
		efsStr   = flag.String("efs", "", "comma-separated search widths to sweep; 0 = the store's default (default: 16,32,64,128,256)")
		list     = flag.Bool("list", false, "list the registered stores and exit")
		dup      = flag.Float64("dup", 0, "share of vectors replaced by near-copies of 20 base vectors (0-1), e.g. 0.3")
		vectors  = flag.String("vectors", "", "real embedding vectors (.fvecs, see cmd/exportvecs) instead of synthetic data; -sizes and -dims come from the file")
	)
	flag.Parse()

	if *list {
		fmt.Println(strings.Join(vectordb.Names(), "\n"))
		return
	}

	sizes, err := parseSizes(*sizesStr)
	if err != nil {
		fatal("-sizes: %v", err)
	}

	var efs []int
	if *efsStr != "" {
		efs, err = parseSizesAllowZero(*efsStr)
		if err != nil {
			fatal("-efs: %v", err)
		}
	}

	names := vectordb.Names()
	if *stores != "" {
		names = strings.Split(*stores, ",")
		for i := range names {
			names[i] = strings.TrimSpace(names[i])
		}
	}

	if runtime.NumCPU() > 4 {
		fmt.Fprintf(os.Stderr, "note: %d CPUs visible; for comparable numbers pin the run, e.g. `taskset -c 0-3 ...`\n", runtime.NumCPU())
	}

	cfg := vectordb.Config{
		Sizes:       sizes,
		Dims:        *dims,
		Queries:     *queries,
		K:           *k,
		Metric:      "cosine",
		Stores:      names,
		WorkDir:     *workdir,
		DupFraction: *dup,
		VectorsFile: *vectors,
		Efs:         efs,
		Log: func(f string, a ...any) {
			fmt.Fprintf(os.Stderr, f+"\n", a...)
		},
	}

	rep, err := vectordb.Run(context.Background(), cfg)
	if rep != nil {
		rep.Command = strings.Join(os.Args, " ")
		fmt.Println(rep.Markdown())
		if j, m, werr := rep.Write(*results); werr != nil {
			fmt.Fprintf(os.Stderr, "writing report: %v\n", werr)
		} else {
			fmt.Fprintf(os.Stderr, "wrote %s and %s\n", j, m)
		}
	}
	if err != nil {
		fatal("%v", err)
	}
	if rep.Failed() {
		fmt.Fprintln(os.Stderr, "FAIL: a store failed or had recall@10 < 0.90")
		os.Exit(1)
	}
}

func parseSizes(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid size %q: %w", p, err)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return []int{10000}, nil
	}
	return out, nil
}

func parseSizesAllowZero(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid width %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vectorbench: "+format+"\n", args...)
	os.Exit(2)
}
