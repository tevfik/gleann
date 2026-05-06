//go:build native

package gleann

// IsNativeSupported returns true if the binary was built with Native embedding engine support.
func IsNativeSupported() bool {
	return true
}
