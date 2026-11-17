package store

// IsNetworkFS reports whether path lives on a network file system, using the
// platform detection (statfs on linux/darwin, UNC prefix on windows). It is
// exported so other SQLite stores in the repository (the memory store, RFC
// 003 section 3.6) apply the same WAL-to-DELETE fallback without duplicating
// the platform files.
func IsNetworkFS(path string) bool {
	return isNetworkFS(path)
}
