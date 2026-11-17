package indexer

import "strings"

// PackagePrefix is the FQN prefix of the package (directory) containing
// relPath, the part buildFQNOf puts before "." (empty for a root file without
// a module).
func PackagePrefix(module, relPath string) string {
	return buildPkgPrefixOf(strings.TrimSuffix(module, "/"), relPath)
}
