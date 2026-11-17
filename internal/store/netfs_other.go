//go:build !linux && !darwin && !windows

package store

// Other platforms assume a local file system (RFC 003 section 3.6).
func isNetworkFS(path string) bool {
	return false
}
