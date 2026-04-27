//go:build !(cgo && faiss)

package gleann

// defaultBackend returns "diskann" (pure-Go Vamana) when FAISS is not available.
// DiskANN provides disk-resident search with PQ prefiltering, using ~2.7x less
// RAM than HNSW for large datasets.
func defaultBackend() string {
	return "diskann"
}
