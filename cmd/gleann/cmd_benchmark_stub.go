//go:build !treesitter

package main

import (
	"fmt"
	"os"
)

func cmdBenchmark(args []string) {
	fmt.Fprintln(os.Stderr, "⚠️  Benchmark command requires CGo (build with -tags treesitter).")
	os.Exit(1)
}
