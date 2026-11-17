package indexer

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// DetectGoModule reads the module name from go.mod in the given directory.
// Falls back to filepath.Base(dir) if go.mod is absent or unreadable.
func DetectGoModule(dir string) string {
	goModPath := filepath.Join(dir, "go.mod")
	f, err := os.Open(goModPath)
	if err != nil {
		return filepath.Base(dir)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return filepath.Base(dir)
}
