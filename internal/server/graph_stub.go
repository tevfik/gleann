//go:build !treesitter

package server

import "fmt"

func openGraphDB(dbPath string) (graphDBHandle, error) {
	return nil, fmt.Errorf("graph database requires CGo (build with -tags treesitter)")
}

func runGraphIndex(name, docsDir, indexDir, module string) error {
	return fmt.Errorf("graph indexing requires CGo (build with -tags treesitter)")
}
