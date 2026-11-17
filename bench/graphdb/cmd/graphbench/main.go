// Command graphbench runs the graph database benchmark (package graphdb).
//
//	cd bench/graphdb
//	taskset -c 0-3 go run ./cmd/graphbench -edges 10000,100000
//	taskset -c 0-3 go run -tags kuzu ./cmd/graphbench -backends sqlite,kuzu -edges 1000000
//
// The reference backend (sqlite) always runs first; candidates are compared
// against it. Adapters that need CGo are compiled in with their build tag.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/tevfik/gleann/bench/graphdb"
)

func main() {
	var (
		edges    = flag.String("edges", envOr("GLEANN_GRAPH_BENCH_EDGES", "10000"), "comma-separated edge counts (symbols = edges/5, files = edges/500)")
		queries  = flag.Int("queries", 200, "sampled start symbols per query type")
		paths    = flag.Int("paths", 20, "shortest-path pairs")
		backends = flag.String("backends", "", "comma-separated backends to run (default: every registered one); the reference always runs")
		results  = flag.String("results", "results", "directory for the JSON and markdown report")
		workdir  = flag.String("workdir", "", "parent directory of the scratch databases (default: system temp)")
		list     = flag.Bool("list", false, "list the registered backends and exit")
	)
	flag.Parse()

	if *list {
		fmt.Println(strings.Join(graphdb.Names(), "\n"))
		return
	}
	sizes, err := graphdb.ParseSizes(*edges)
	if err != nil {
		fatal("-edges: %v", err)
	}
	names := graphdb.Names()
	if *backends != "" {
		names = strings.Split(*backends, ",")
	}
	if runtime.NumCPU() > 4 {
		fmt.Fprintf(os.Stderr, "note: %d CPUs visible; for comparable numbers pin the run, e.g. `taskset -c 0-3 ...`\n", runtime.NumCPU())
	}

	rep, err := graphdb.Run(context.Background(), graphdb.Config{
		Sizes: sizes, Queries: *queries, Paths: *paths, Backends: names, WorkDir: *workdir,
		Log: func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
	})
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
		fmt.Fprintln(os.Stderr, "FAIL: a candidate disagreed with the reference or failed (see parity table)")
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "graphbench: "+format+"\n", args...)
	os.Exit(2)
}
