//go:build !(cgo && faiss)

package gleann

// defaultBackend returns "hnsw" when FAISS is not available.
func defaultBackend() string {
	return "hnsw"
}
