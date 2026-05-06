//go:build !native

package gleann

// IsNativeSupported returns false if the binary was built without Native embedding engine support.
func IsNativeSupported() bool {
	return false
}
