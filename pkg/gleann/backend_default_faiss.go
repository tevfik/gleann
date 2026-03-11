//go:build cgo && faiss

package gleann

// defaultBackend returns "faiss" when built with FAISS support.
func defaultBackend() string {
	return "faiss"
}
