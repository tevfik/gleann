//go:build eif

package gleann

// IsEIFSupported returns true if the binary was built with EIF runtime support.
func IsEIFSupported() bool {
	return true
}
