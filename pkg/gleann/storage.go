package gleann

import (
	"fmt"
	"os"
	"path/filepath"
)

// Vector storage modes.
//
//   - "store": the vectors are kept in the index file. Search embeds only the
//     query (milliseconds). Costs about passages × dims × 4 bytes of disk.
//   - "recompute" (LEANN style): vectors are not stored; every search
//     re-embeds the graph neighbours it visits, i.e. hundreds of embedding
//     calls per query (measured ~10-15 s per query on 6k passages). The index
//     is small, the passage text is not.
const (
	StorageStore     = "store"
	StorageRecompute = "recompute"
)

// VectorStorage describes how an index keeps its vectors.
type VectorStorage struct {
	// Mode is "store", "recompute", "none" (lexical only / no vector file) or
	// "unknown".
	Mode string
	// IndexBytes is the size of the vector index file.
	IndexBytes int64
	// FullBytes is what the index would weigh with all vectors (float32).
	FullBytes int64
	// PassageBytes is the size of the passage store.
	PassageBytes int64
	// Convertible reports whether the mode can be switched with
	// `gleann index storage <name> --set ...` (the DiskANN backend always
	// stores its vectors).
	Convertible bool
	// Note is a one-line explanation for UIs.
	Note string
}

// InspectVectorStorage derives the storage mode of an index from its files.
func InspectVectorStorage(indexDir string, meta IndexMeta) VectorStorage {
	base := filepath.Join(indexDir, meta.Name, meta.Name)
	vs := VectorStorage{Convertible: meta.Backend != "diskann"}
	if fi, err := os.Stat(base + ".passages.db"); err == nil {
		vs.PassageBytes = fi.Size()
	}
	vs.FullBytes = int64(meta.NumPassages) * int64(meta.Dimensions) * 4
	fi, err := os.Stat(base + ".index")
	if err != nil || meta.LexicalOnly || !meta.VectorReady && meta.Dimensions == 0 {
		vs.Mode, vs.Convertible = "none", false
		vs.Note = "no vector index (keyword search only)"
		return vs
	}
	vs.IndexBytes = fi.Size()
	switch {
	case meta.Backend == "diskann":
		vs.Mode = StorageStore
		vs.Note = "DiskANN always stores its vectors"
	case vs.FullBytes == 0:
		vs.Mode = "unknown"
	case vs.IndexBytes >= vs.FullBytes*7/10:
		vs.Mode = StorageStore
		vs.Note = "fast search (query embedding only)"
	default:
		vs.Mode = StorageRecompute
		vs.Note = "small index, but every search re-embeds hundreds of passages (seconds per query)"
	}
	return vs
}

// ApplyStorageMode maps Config.StorageMode onto the HNSW pruning settings.
// An empty StorageMode leaves them untouched.
func (c *Config) ApplyStorageMode() error {
	switch c.StorageMode {
	case "":
	case StorageStore:
		c.HNSWConfig.PruneEmbeddings = false
	case StorageRecompute:
		c.HNSWConfig.PruneEmbeddings = true
		c.HNSWConfig.PruneKeepFraction = 0
	default:
		return fmt.Errorf("unknown storage mode %q (want %s|%s)", c.StorageMode, StorageStore, StorageRecompute)
	}
	return nil
}
