package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("o", ".", "output directory for the JSON files")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: kuzu-memory-export [-o dir] <store path | index dir>")
		fmt.Fprintln(os.Stderr, "Exports gleann v1.9 <name>_memory stores to <name>.kuzu-export.json; load them with `gleann memory import-graph`.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *out); err != nil {
		fmt.Fprintln(os.Stderr, "kuzu-memory-export:", err)
		os.Exit(1)
	}
}

func run(arg, outDir string) error {
	fi, err := os.Stat(arg)
	if err != nil {
		return err
	}
	stores := []string{arg}
	// A directory that is not itself a <name>_memory store is an index directory.
	if fi.IsDir() && !isStorePath(arg) {
		stores, err = DiscoverStores(arg)
		if err != nil {
			return err
		}
		if len(stores) == 0 {
			return fmt.Errorf("no *_memory stores found in %s", arg)
		}
	}
	for _, s := range stores {
		ex, err := ExportStore(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
		p, err := WriteExport(ex, outDir)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d entities, %d relations, %d archive rows -> %s\n",
			s, len(ex.Entities), len(ex.Relations), len(ex.Archive), p)
	}
	return nil
}
