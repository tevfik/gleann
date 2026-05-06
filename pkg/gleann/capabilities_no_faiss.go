//go:build !(cgo && faiss)

package gleann

// IsFaissSupported returns false if the binary was built without FAISS support.
func IsFaissSupported() bool {
	return false
}
