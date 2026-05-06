//go:build cgo && faiss

package gleann

// IsFaissSupported returns true if the binary was built with FAISS support.
func IsFaissSupported() bool {
	return true
}
