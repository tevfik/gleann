//go:build linux

package server

import (
	"os"
	"path/filepath"
	"strings"
)

// openFilesUnderDir lists this process's open file descriptors for files under
// dir, read from /proc/self/fd.
func openFilesUnderDir(dir string) []string {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && strings.HasPrefix(target, dir+string(filepath.Separator)) {
			out = append(out, target)
		}
	}
	return out
}
