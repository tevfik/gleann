//go:build !eif

package gleann

// IsEIFSupported returns false if the binary was built without EIF runtime support.
func IsEIFSupported() bool {
	return false
}
