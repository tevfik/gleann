//go:build !windows

package memory

import "os"

// fileAttributes is zero where files have no Windows attribute word.
func fileAttributes(os.FileInfo) uint32 { return 0 }
