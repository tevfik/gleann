//go:build !treesitter

package server

import "fmt"

func openGraphDB(dbPath string) (graphDBHandle, error) {
	return nil, errGraphUnsupported
}

func runGraphIndex(name, docsDir, indexDir, module string) error {
	return fmt.Errorf("graph indexing requires CGo (build with -tags treesitter)")
}
