//go:build !(linux || darwin || windows)

package gleann

// diskSpace is not reported on this platform.
func diskSpace(string) (total, free uint64) { return 0, 0 }
