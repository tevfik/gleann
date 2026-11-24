//go:build !linux

package server

// openFilesUnderDir has no portable implementation; the check runs on Linux.
func openFilesUnderDir(string) []string { return nil }
