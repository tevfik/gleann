//go:build windows

package store

import "strings"

// isNetworkFS detects UNC paths (\\server\share). Mapped network drives are
// not detected here; that detection is an open point (RFC 003 9.9).
func isNetworkFS(path string) bool {
	return strings.HasPrefix(path, `\\`)
}
