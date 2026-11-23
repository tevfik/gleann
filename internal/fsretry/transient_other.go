//go:build !windows

package fsretry

// isTransient is always false off Windows: renaming over an open file works.
func isTransient(error) bool { return false }
